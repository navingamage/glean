/**
 * glean in production: a scheduled scan and a private dashboard.
 *
 * A Worker rather than a Pages project for one reason. Pages Functions cannot be
 * scheduled, and the entire premise of glean is that it looks for this money
 * without being asked. A deployment that only ran when someone visited it would
 * give that back.
 */
import { authorize } from "./auth";
import { Store } from "./db";
import { Extractor, DEFAULT_CHEAP_MODEL, DEFAULT_PRIME_MODEL, usageString } from "./extract/openrouter";
import { collapse } from "./dedupe";
import { Assessment, assess, questions, rank, totalAtStake } from "./match";
import { Claim, ClaimState, Holding, Settlement, defaultProfile, validKind } from "./model";
import { normalizeEntity, normalizeProvince, PROVINCES } from "./normalize";
import { authorityMap, enabledSources, loadSources } from "./sources";
import { defaultOptions, runScan, summarize } from "./scan";
import {
  deadlineClass, deadlineText, esc, layout, questionCard, valueWord, verdictWord,
} from "./views";

export interface Env {
  DB: D1Database;
  OPENROUTER_API_KEY?: string;
  GLEAN_ACCESS_KEY?: string;
  GLEAN_WEBHOOK?: string;
  GLEAN_MODEL_CHEAP?: string;
  GLEAN_MODEL_PRIME?: string;
  GLEAN_PER_SOURCE?: string;
}

function extractorFor(env: Env): Extractor | undefined {
  if (!env.OPENROUTER_API_KEY) return undefined;
  return new Extractor(
    env.OPENROUTER_API_KEY,
    env.GLEAN_MODEL_CHEAP || DEFAULT_CHEAP_MODEL,
    env.GLEAN_MODEL_PRIME || DEFAULT_PRIME_MODEL,
  );
}

/** One settlement per proceeding, plus a map from any record's id to its primary. */
async function primaries(store: Store): Promise<{ list: Settlement[]; canonical: Map<string, string> }> {
  const all = await store.settlements();
  const groups = collapse(all, authorityMap(loadSources()));
  const list: Settlement[] = [];
  const canonical = new Map<string, string>();
  for (const g of groups) {
    list.push(g.primary);
    canonical.set(g.primary.id, g.primary.id);
    for (const dup of g.also) canonical.set(dup.id, g.primary.id);
  }
  return { list, canonical };
}

async function assessAll(store: Store) {
  const { list, canonical } = await primaries(store);
  const profile = await store.profile();
  const byId = new Map(list.map((s) => [s.id, s]));
  const as = list.map((s) => assess(s, profile));
  rank(as, byId);
  return { as, byId, canonical, profile };
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const auth = authorize(req, env.GLEAN_ACCESS_KEY);
    if (!auth.ok) return auth.response;

    const store = new Store(env.DB);
    const url = new URL(req.url);

    try {
      if (req.method === "POST" && url.pathname === "/answer") return await postAnswer(req, store);
      if (req.method === "POST" && url.pathname === "/claim") return await postClaim(req, store);
      if (url.pathname.startsWith("/s/")) return await showPage(store, url.pathname.slice(3));
      if (url.pathname === "/") return await indexPage(store);
      return new Response("not found", { status: 404 });
    } catch (e) {
      return new Response(`glean: ${e}`, { status: 500 });
    }
  },

  async scheduled(_event: ScheduledEvent, env: Env, ctx: ExecutionContext): Promise<void> {
    ctx.waitUntil(scan(env));
  },
};

async function scan(env: Env): Promise<void> {
  const store = new Store(env.DB);
  const extractor = extractorFor(env);
  const sources = enabledSources(loadSources());
  const opts = defaultOptions();
  const per = parseInt(env.GLEAN_PER_SOURCE ?? "", 10);
  if (per > 0) opts.perSource = per;

  const res = await runScan(store, sources, extractor, opts);
  const line = summarize(res) +
    (extractor ? `\n${usageString(extractor.usage, extractor.models())}` : "");
  await store.event("scan", "", { summary: line, errors: res.errors.slice(0, 20) });

  if (!env.GLEAN_WEBHOOK) return;
  const digest = await digestText(store, new Set(res.newIds));
  if (!digest) return;
  try {
    await fetch(env.GLEAN_WEBHOOK, {
      method: "POST",
      headers: { "Content-Type": "text/plain; charset=utf-8" },
      body: digest + "\n\n" + line,
    });
  } catch { /* a broken webhook must not fail the scan */ }
}

/**
 * Reminders fire when a deadline crosses thirty, fourteen, seven, three or one
 * day out. Thirty is the one that matters: a claim needing a receipt from 2009
 * is not something anyone can act on the afternoon it closes.
 */
const REMINDER_DAYS = [30, 14, 7, 3, 1, 0];

async function digestText(store: Store, newIds: Set<string>): Promise<string> {
  const { as, byId } = await assessAll(store);
  const claims = new Map((await store.claims()).map((c) => [c.settlement_id, c]));
  const lines: string[] = [];

  for (const a of as) {
    const s = byId.get(a.settlementId);
    if (!s || a.verdictStr === "no") continue;
    const filed = claims.get(s.id)?.state;
    const settled = filed && filed !== "new" && filed !== "interested";
    const isNew = newIds.has(s.id);
    const crossing = REMINDER_DAYS.includes(a.daysLeft) && !settled;
    if (!isNew && !crossing) continue;

    const why = isNew
      ? (a.verdictStr === "yes" ? "new, and you are in the class"
        : `new, ${Math.round(a.probability * 100)}% chance you qualify`)
      : a.daysLeft <= 0 ? "closes today" : `closes in ${a.daysLeft} days`;
    lines.push(`- ${s.title}\n  ${why}` +
      (s.claim_deadline ? ` | deadline ${s.claim_deadline}` : "") +
      (a.payout > 0 ? ` | about $${a.payout.toFixed(0)}` : "") +
      `\n  ${s.claim_url || s.url}`);
  }
  return lines.length ? "glean\n\n" + lines.join("\n") : "";
}

async function indexPage(store: Store): Promise<Response> {
  const { as, byId, canonical } = await assessAll(store);
  const qs = questions(as).slice(0, 6);
  const atStake = totalAtStake(questions(as), as);

  const claimable = as.filter((a) => a.verdictStr === "yes" && a.actionable);
  const closing = as.filter((a) => a.daysLeft <= 60 && a.daysLeft >= 0 &&
    a.verdictStr !== "no" && !claimable.includes(a));

  const card = (a: Assessment) => {
    const s = byId.get(a.settlementId)!;
    return `<div class="card"><div class="row">
      <h3><a href="/s/${esc(s.id)}">${esc(s.title)}</a></h3>
      <span class="amount">${a.payout > 0 ? "$" + a.payout.toFixed(0) : "?"}</span></div>
      <div class="meta">
        <span class="chip">${esc(verdictWord(a))}</span>
        ${s.claim_deadline ? `<span class="${deadlineClass(s)}">deadline ${esc(deadlineText(s))}</span>` : ""}
        <span>about ${s.effort_minutes} min</span>
        ${s.proof_required ? "<span>proof required</span>" : ""}
        ${s.claim_url ? `<a href="${esc(s.claim_url)}" rel="noreferrer noopener" target="_blank">file a claim</a>` : ""}
      </div></div>`;
  };

  const claimsRows = (await store.claims())
    .filter((c) => c.state !== "new")
    .map((c) => {
      const s = byId.get(canonical.get(c.settlement_id) ?? c.settlement_id);
      if (!s) return "";
      return `<tr><td><a href="/s/${esc(s.id)}">${esc(s.title)}</a></td>
        <td>${esc(c.state)}</td><td>${esc(c.filed_on)}</td>
        <td class="amount">${c.amount > 0 ? "$" + c.amount.toFixed(2) : ""}</td></tr>`;
    }).join("");

  const rows = as.map((a) => {
    const s = byId.get(a.settlementId)!;
    return `<tr><td class="${a.verdictStr === "yes" ? "yes" : ""}">${esc(verdictWord(a))}</td>
      <td class="amount">${esc(valueWord(a))}</td>
      <td class="${deadlineClass(s)}">${esc(deadlineText(s))}</td>
      <td>${esc(s.stage)}</td>
      <td><a href="/s/${esc(s.id)}">${esc(s.title)}</a></td></tr>`;
  }).join("");

  const headline = [
    closing.length ? `${closing.length} closing soon` : "",
    qs.length ? `${qs.length} questions worth about $${atStake.toFixed(0)}` : "",
  ].filter(Boolean).join(" &middot; ") || "Nothing needs you right now.";

  const body = `
<h1>${claimable.length ? `${claimable.length} claim${claimable.length === 1 ? "" : "s"} waiting for you` : "Nothing to claim today"}</h1>
<p class="sub">${headline}</p>
${qs.length ? `<h2>Answer these first</h2>${qs.map((q) => questionCard(q)).join("")}` : ""}
${claimable.length ? `<h2>Claim these</h2>${claimable.map(card).join("")}` : ""}
${closing.length ? `<h2>Closing soon</h2>${closing.slice(0, 8).map(card).join("")}` : ""}
${claimsRows ? `<h2>Your claims</h2><table><tr><th>Settlement</th><th>State</th><th>Filed</th><th>Amount</th></tr>${claimsRows}</table>` : ""}
<h2>Everything tracked</h2>
${rows ? `<table><tr><th>You</th><th>Value</th><th>Deadline</th><th>Stage</th><th>Settlement</th></tr>${rows}</table>`
    : `<p class="sub">Nothing yet. The next scheduled scan will fill this in.</p>`}`;

  return html(layout("Dashboard", body, `glean &middot; ${as.length} proceedings tracked`));
}

async function showPage(store: Store, id: string): Promise<Response> {
  const s = await store.settlement(id);
  if (!s) return new Response("not found", { status: 404 });
  const a = assess(s, await store.profile());
  const claim = await store.claim(id);

  const groupsHTML = a.groups.map((g) => {
    if (g.criteria.length > 1) {
      return `<p class="crit"><b>any one of</b> &mdash; ${esc(g.valueStr)}</p>` +
        g.criteria.map((c) =>
          `<p class="crit" style="padding-left:1.2rem"><b>${esc(c.valueStr)}</b> ${esc(c.reason)}</p>`).join("");
    }
    return g.criteria.map((c) =>
      `<p class="crit"><b>${esc(c.valueStr)}</b> ${esc(c.reason)}</p>`).join("");
  }).join("");

  const states: ClaimState[] = ["interested", "filed", "paid", "skipped"];
  const body = `
<p class="sub"><a href="/">back</a></p>
<h1>${esc(s.title)}</h1>
<p class="sub">${esc(s.summary)}</p>
<div class="card"><div class="meta">
  <span class="chip">${esc(s.stage)}</span>
  <span class="${deadlineClass(s)}">deadline ${esc(deadlineText(s))}</span>
  ${a.payout > 0 ? `<span>about $${a.payout.toFixed(0)} to you</span>` : ""}
  <span>about ${s.effort_minutes} min</span>
  ${s.proof_required ? `<span>proof: ${esc(s.proof_note || "required")}</span>` : ""}
  ${s.administrator ? `<span>${esc(s.administrator)}</span>` : ""}
</div><p style="margin:.8rem 0 0">
  ${s.claim_url ? `<a href="${esc(s.claim_url)}" rel="noreferrer noopener" target="_blank">Official claim portal</a> &middot; ` : ""}
  <a href="${esc(s.url)}" rel="noreferrer noopener" target="_blank">Page glean read</a></p></div>
<h2>Are you in the class? ${esc(verdictWord(a))}</h2>
${a.note ? `<p class="crit">${esc(a.note)}</p>` : ""}
${s.class_text ? `<blockquote>${esc(s.class_text)}</blockquote>` : ""}
${groupsHTML}
${a.questions.length ? `<h2>Resolve it</h2>${a.questions.map((q) => questionCard(q, "/s/" + s.id)).join("")}` : ""}
<h2>Your claim</h2>
<div class="q"><div>Currently: <b>${esc(claim?.state ?? "new")}</b></div><div>
${states.map((st) => `<form class="inline" method="post" action="/claim">
  <input type="hidden" name="id" value="${esc(s.id)}">
  <input type="hidden" name="state" value="${st}"><button>${st}</button></form>`).join(" ")}
</div></div>
<h2>Provenance</h2>
<table>
  <tr><td>source</td><td>${esc(s.source_id)}</td></tr>
  <tr><td>extracted by</td><td>${esc(s.extractor)}, confidence ${Math.round(s.confidence * 100)}%</td></tr>
  <tr><td>first seen</td><td>${esc(s.first_seen)}</td></tr>
  <tr><td>last seen</td><td>${esc(s.last_seen)}</td></tr>
  ${s.case_name ? `<tr><td>case</td><td>${esc(s.case_name)}</td></tr>` : ""}
  ${s.court ? `<tr><td>court</td><td>${esc(s.court)}</td></tr>` : ""}
  ${s.settlement_fund ? `<tr><td>fund</td><td>$${s.settlement_fund.toFixed(0)}</td></tr>` : ""}
</table>`;

  return html(layout(s.title, body, `glean &middot; ${esc(s.id)}`));
}

async function postAnswer(req: Request, store: Store): Promise<Response> {
  const form = await req.formData();
  const raw = String(form.get("id") ?? "");
  const [kindStr, entity] = raw.split(":");
  if (!kindStr || !entity || !validKind(kindStr)) {
    return new Response("malformed question id", { status: 400 });
  }
  const negative = String(form.get("a") ?? "") !== "yes";
  const label = String(form.get("label") ?? "") || entity;
  const profile = await store.profile();

  if (kindStr === "residency") {
    const code = normalizeProvince(entity);
    if (!code) return new Response("unknown province", { status: 400 });
    // A specific province supersedes the bare "Canada" a profile starts with,
    // since keeping both leaves the vague one answering questions the specific
    // one can now answer properly.
    profile.residences = profile.residences.filter(
      (r) => r.province !== code && !(r.province === "CA" && code !== "CA"));
    if (!negative) profile.residences.push({ province: code, window: { from: "", until: "" } });
  } else {
    const key = normalizeEntity(entity);
    const h: Holding = {
      kind: kindStr, entity: key, label, window: { from: "", until: "" },
      negative, added_on: new Date().toISOString().slice(0, 10),
    };
    const at = profile.holdings.findIndex(
      (x) => x.kind === h.kind && normalizeEntity(x.entity) === key);
    if (at >= 0) profile.holdings[at] = h;
    else profile.holdings.push(h);
  }

  await store.saveProfile(profile);
  await store.event("answered", "", { kind: kindStr, entity, negative });
  return redirect(String(form.get("back") ?? "/"));
}

async function postClaim(req: Request, store: Store): Promise<Response> {
  const form = await req.formData();
  const id = String(form.get("id") ?? "");
  if (!(await store.settlement(id))) return new Response("no such settlement", { status: 404 });
  const state = String(form.get("state") ?? "") as ClaimState;
  if (!["new", "interested", "filed", "paid", "rejected", "skipped"].includes(state)) {
    return new Response("unknown state", { status: 400 });
  }
  const prev = await store.claim(id);
  const c: Claim = prev ?? {
    settlement_id: id, state: "new", filed_on: "", paid_on: "",
    amount: 0, updated_on: "",
  };
  c.state = state;
  const day = new Date().toISOString().slice(0, 10);
  if (state === "filed" && !c.filed_on) c.filed_on = day;
  if (state === "paid") c.paid_on = day;
  await store.setClaim(c);
  return redirect("/s/" + id);
}

/** Only same-site paths, so a crafted form cannot make this an open redirect. */
function redirect(to: string): Response {
  const safe = to.startsWith("/") && !to.startsWith("//") ? to : "/";
  return new Response(null, { status: 303, headers: { Location: safe } });
}

function html(body: string): Response {
  return new Response(body, {
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Robots-Tag": "noindex, nofollow",
      "Referrer-Policy": "no-referrer",
      "X-Content-Type-Options": "nosniff",
    },
  });
}
