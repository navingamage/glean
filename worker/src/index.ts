/**
 * glean in production: a scheduled scan and a private dashboard.
 *
 * A Worker rather than a Pages project for one reason. Pages Functions cannot be
 * scheduled, and the entire premise of glean is that it looks for this money
 * without being asked. A deployment that only ran when someone visited it would
 * give that back.
 */
import { identify } from "./access";
import {
  checkLogin, clearedCookie, credentials, isDefault, sessionCookie, signedIn,
} from "./auth";
import { Store } from "./db";
import {
  DEFAULT_BASE_URL, DEFAULT_CHEAP_MODEL, DEFAULT_PRIME_MODEL, Extractor, newUsage, usageString,
} from "./extract/openrouter";
import { collapse } from "./dedupe";
import { Assessment, assess, questions, rank, totalAtStake } from "./match";
import { Claim, ClaimState, Holding, Profile, Settlement, validKind } from "./model";
import { addMute, mutedBy, removeMute, suggestTerm } from "./mute";
import { normalizeEntity, normalizeProvince, PROVINCES } from "./normalize";
import { authorityMap, enabledSources, loadSources } from "./sources";
import { defaultOptions, runScan, summarize } from "./scan";
import {
  action, deadlineClass, deadlineText, esc, layout, loginPage, money, muteTags,
  questionCard, settlementCard, valueWord, verdictShort, verdictWord,
} from "./views";

export interface Env {
  DB: D1Database;
  OPENROUTER_API_KEY?: string;
  /**
   * The dashboard sign-in. Both default to "test" when unset, which is what
   * makes a fresh deployment openable; set them with `wrangler secret put`
   * before the store holds a profile worth protecting.
   */
  GLEAN_USERNAME?: string;
  GLEAN_PASSWORD?: string;
  GLEAN_WEBHOOK?: string;
  /**
   * Where the model calls go. Unset means OpenRouter directly; set it to a
   * Cloudflare AI Gateway's OpenRouter endpoint to get token counts, costs,
   * retries and an optional cache without changing the request itself.
   */
  GLEAN_OPENROUTER_BASE?: string;
  GLEAN_MODEL_CHEAP?: string;
  GLEAN_MODEL_PRIME?: string;
  GLEAN_PER_SOURCE?: string;
  /**
   * Outbound requests one invocation may make. The platform cap is 50 on the
   * free plan and 1000 on paid; this stays under whichever applies.
   */
  GLEAN_REQUEST_BUDGET?: string;
  /**
   * Wall-clock a single pass may take. The real constraint on Workers, not
   * subrequests: a prime extraction takes five to twenty seconds, so a pass
   * measured in minutes is killed before it can commit its summary.
   */
  GLEAN_DEADLINE_MS?: string;
  /** Extractions per pass: the bound in the unit that actually costs time. */
  GLEAN_MAX_EXTRACTIONS?: string;
  // Set these to scope profiles per person behind Cloudflare Access. Without
  // them every signed-in request is the single owner identity, which is correct
  // when the shared key is the only gate.
  GLEAN_ACCESS_TEAM?: string;
  GLEAN_ACCESS_AUD?: string;
}

function extractorFor(env: Env): Extractor | undefined {
  if (!env.OPENROUTER_API_KEY) return undefined;
  return new Extractor(
    env.OPENROUTER_API_KEY,
    env.GLEAN_MODEL_CHEAP || DEFAULT_CHEAP_MODEL,
    env.GLEAN_MODEL_PRIME || DEFAULT_PRIME_MODEL,
    newUsage(),
    env.GLEAN_OPENROUTER_BASE || DEFAULT_BASE_URL,
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

/**
 * Everything on the store, judged against one profile and split into what the
 * reader has asked to see and what they have not.
 *
 * The split happens once, here, rather than in each page, because the queue is
 * only worth reading if the same settlement is absent from all of it. A muted
 * case that still contributed questions would be hidden in name only.
 */
export interface View {
  /** In view: not muted, not put aside, not under the reader's own floor. */
  shown: Assessment[];
  /** Hidden by a term, with the term that did it. */
  muted: { a: Assessment; term: string }[];
  /** Put aside one case at a time. */
  skipped: Assessment[];
  /** Real, but below the payout floor or over the effort ceiling. */
  belowBar: Assessment[];
  byId: Map<string, Settlement>;
  canonical: Map<string, string>;
  profile: Profile;
  claims: Map<string, Claim>;
}

/** Why a settlement is not worth an evening, in the reader's own terms. */
export function underBar(s: Settlement, a: Assessment, p: Profile): string {
  if (a.payout > 0 && p.min_payout > 0 && a.payout < p.min_payout) {
    return `pays about $${a.payout.toFixed(0)}, under your $${p.min_payout.toFixed(0)} floor`;
  }
  if (p.max_effort_minutes > 0 && s.effort_minutes > p.max_effort_minutes) {
    return `takes about ${s.effort_minutes} minutes, over your ${p.max_effort_minutes} minute limit`;
  }
  return "";
}

async function viewOf(store: Store, user: string): Promise<View> {
  const { list, canonical } = await primaries(store);
  const profile = await store.profile(user);
  const byId = new Map(list.map((s) => [s.id, s]));
  const all = list.map((s) => assess(s, profile));
  rank(all, byId);

  const claims = new Map((await store.claims(user)).map((c) => [c.settlement_id, c]));
  const v: View = {
    shown: [], muted: [], skipped: [], belowBar: [], byId, canonical, profile, claims,
  };
  for (const a of all) {
    const s = byId.get(a.settlementId)!;
    const term = mutedBy(s, profile);
    if (term) { v.muted.push({ a, term }); continue; }
    if (claims.get(s.id)?.state === "skipped") { v.skipped.push(a); continue; }
    if (underBar(s, a, profile)) { v.belowBar.push(a); continue; }
    v.shown.push(a);
  }
  return v;
}

export default {
  async fetch(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(req.url);
    const creds = credentials(env);

    if (url.pathname === "/login") {
      if (req.method === "POST") {
        const form = await req.clone().formData();
        const next = String(form.get("next") ?? "/");
        if (await checkLogin(req, creds)) {
          return new Response(null, {
            status: 303,
            headers: {
              Location: next.startsWith("/") && !next.startsWith("//") ? next : "/",
              "Set-Cookie": await sessionCookie(creds),
            },
          });
        }
        return html(loginPage({ failed: true, hint: isDefault(creds), next }), 401);
      }
      if (await signedIn(req, creds)) return redirect("/");
      return html(loginPage({ hint: isDefault(creds), next: url.searchParams.get("next") ?? "/" }));
    }

    if (req.method === "POST" && url.pathname === "/logout") {
      return new Response(null, {
        status: 303,
        headers: { Location: "/login", "Set-Cookie": clearedCookie() },
      });
    }

    if (!(await signedIn(req, creds))) {
      // Sending an unauthenticated request to the form, carrying where it was
      // going, so a bookmarked settlement page survives a signed-out visit.
      const next = url.pathname + (url.search || "");
      if (req.method !== "GET") return html(loginPage({ hint: isDefault(creds) }), 401);
      return new Response(null, {
        status: 303,
        headers: { Location: `/login?next=${encodeURIComponent(next)}` },
      });
    }

    const store = new Store(env.DB);
    const user = await identify(req, env.GLEAN_ACCESS_TEAM, env.GLEAN_ACCESS_AUD);

    try {
      if (req.method === "POST" && url.pathname === "/scan") {
        // A scan takes minutes, so it outlives the response. waitUntil keeps the
        // invocation alive without making the caller hold the connection open.
        ctx.waitUntil(scan(env));
        return redirect("/");
      }
      if (req.method === "POST") {
        switch (url.pathname) {
          case "/answer": return await postAnswer(req, store, user);
          case "/claim": return await postClaim(req, store, user);
          case "/mute": return await postMute(req, store, user);
          case "/unmute": return await postUnmute(req, store, user);
          case "/forget": return await postForget(req, store, user);
          case "/province": return await postProvince(req, store, user);
          case "/thresholds": return await postThresholds(req, store, user);
        }
        return new Response("not found", { status: 404 });
      }
      if (url.pathname.startsWith("/s/")) return await showPage(store, user, url.pathname.slice(3));
      if (url.pathname === "/preferences") return await preferencesPage(store, user);
      if (url.pathname === "/export.json") return await exportJson(store, user);
      if (url.pathname === "/") return await indexPage(store, user);
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
  const budget = parseInt(env.GLEAN_REQUEST_BUDGET ?? "", 10);
  if (budget > 0) opts.requestBudget = budget;
  const ms = parseInt(env.GLEAN_DEADLINE_MS ?? "", 10);
  if (ms > 0) opts.deadlineMs = ms;
  const maxEx = parseInt(env.GLEAN_MAX_EXTRACTIONS ?? "", 10);
  if (maxEx > 0) opts.maxExtractions = maxEx;

  // Recorded before the work, not only after it.
  //
  // A pass runs inside waitUntil, which the platform may cut short, and the
  // first deployments wrote their summary at the very end. When they were cut
  // off they left no trace at all, so a scan that had quietly stored six
  // settlements was indistinguishable from one that never ran. Bracketing the
  // pass means the log always says at least that it started.
  const startedAt = Date.now();
  await store.event("scan-started", "", { at: new Date().toISOString() });

  const res = await runScan(store, sources, extractor, opts);
  const line = `${summarize(res)}, ${Math.round((Date.now() - startedAt) / 1000)}s` +
    (extractor
      ? `\n${usageString(extractor.usage, extractor.models())}` +
        (extractor.proxied() ? " via gateway" : "")
      : "");
  // Written before the digest, because the digest reads the whole store and is
  // the more likely of the two to be cut off. Losing the summary of a pass that
  // did real work is how the first deployment looked like it had done nothing.
  await store.event("scan", "", { summary: line, errors: res.errors.slice(0, 20) });

  if (!env.GLEAN_WEBHOOK) return;
  // One digest per person, because what is newly claimable depends on whose
  // profile is being matched against. With no Access in front there is exactly
  // one identity and this is a single message.
  const users = await store.users();
  for (const user of users.length ? users : ["owner"]) {
    const digest = await digestText(store, user, new Set(res.newIds));
    if (!digest) continue;
    try {
      await fetch(env.GLEAN_WEBHOOK, {
        method: "POST",
        headers: { "Content-Type": "text/plain; charset=utf-8" },
        body: digest + "\n\n" + line,
      });
    } catch { /* a broken webhook must not fail the scan */ }
  }
}

/**
 * Reminders fire when a deadline crosses thirty, fourteen, seven, three or one
 * day out. Thirty is the one that matters: a claim needing a receipt from 2009
 * is not something anyone can act on the afternoon it closes.
 */
const REMINDER_DAYS = [30, 14, 7, 3, 1, 0];

async function digestText(store: Store, user: string, newIds: Set<string>): Promise<string> {
  // The shown list, not everything: a subject somebody has asked not to see
  // should not reach them by phone at seven in the morning either.
  const { shown: as, byId, claims } = await viewOf(store, user);
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

async function indexPage(store: Store, user: string): Promise<Response> {
  const v = await viewOf(store, user);
  const open = questions(v.shown);
  const qs = open.slice(0, 5);
  // The stake on the five being shown, not on all of them. A heading that
  // prices the whole backlog above five questions is a promise the five cannot
  // keep.
  const atStake = totalAtStake(qs, v.shown);

  const claimable = v.shown.filter((a) => a.verdictStr === "yes" && a.actionable);
  const closing = v.shown.filter((a) => a.daysLeft <= 60 && a.daysLeft >= 0 &&
    a.verdictStr !== "no" && !claimable.includes(a));
  const live = v.shown.filter((a) => a.verdictStr !== "no" && a.daysLeft >= 0);
  const worth = live.reduce((sum, a) => sum + a.expectedValue, 0);

  const card = (a: Assessment) => {
    const s = v.byId.get(a.settlementId)!;
    return settlementCard(a, s, { suggest: suggestTerm(s) });
  };

  const filed = [...v.claims.values()].filter((c) => c.state !== "new" && c.state !== "skipped");
  const claimsRows = filed.map((c) => {
    const s = v.byId.get(v.canonical.get(c.settlement_id) ?? c.settlement_id);
    if (!s) return "";
    return `<tr><td class="wrap"><a href="/s/${esc(s.id)}">${esc(s.title)}</a></td>
      <td><span class="chip ${c.state === "paid" ? "yes" : ""}">${esc(c.state)}</span></td>
      <td>${esc(c.filed_on || "&mdash;")}</td>
      <td class="amount">${c.amount > 0 ? "$" + c.amount.toFixed(2) : ""}</td></tr>`;
  }).join("");

  const tracked = [...v.shown, ...v.belowBar];
  const rows = tracked.map((a) => {
    const s = v.byId.get(a.settlementId)!;
    const why = underBar(s, a, v.profile);
    return `<tr><td class="${a.verdictStr === "yes" ? "yes" : ""}">${esc(verdictShort(a))}</td>
      <td class="amount">${esc(valueWord(a))}</td>
      <td class="${deadlineClass(s)}">${esc(deadlineText(s))}</td>
      <td>${esc(s.stage)}</td>
      <td class="wrap"><a href="/s/${esc(s.id)}">${esc(s.title)}</a>
        ${why ? `<div class="why" style="color:var(--muted);font-size:.8rem">${esc(why)}</div>` : ""}</td></tr>`;
  }).join("");

  const hiddenCount = v.muted.length + v.skipped.length;
  const stat = (n: string, l: string, cls = "") =>
    `<div class="stat ${cls}"><span class="n">${n}</span><span class="l">${esc(l)}</span></div>`;

  const body = `
<h1>${claimable.length
    ? `${claimable.length} claim${claimable.length === 1 ? "" : "s"} waiting for you`
    : "Nothing to claim today"}</h1>
<p class="sub lede">${qs.length
    ? `${open.length} question${open.length === 1 ? "" : "s"} stand between you and knowing
       about the rest. The ${qs.length === 1 ? "one below is" : `${qs.length} below are`}
       the ones worth answering first.`
    : "Everything glean can decide on its own has been decided."}</p>

<div class="stats">
  ${stat(String(claimable.length), "ready to claim", claimable.length ? "go" : "")}
  ${stat(money(worth), "expected value in play")}
  ${stat(String(closing.length), "closing within 60 days", closing.length ? "soon" : "")}
  ${stat(String(open.length), "questions open")}
</div>

${qs.length ? `<h2>Answer these first <span class="count">worth about ${esc(money(atStake))}</span></h2>
${qs.map((q) => questionCard(q)).join("")}` : ""}

${claimable.length ? `<h2>Claim these <span class="count">${claimable.length}</span></h2>
${claimable.map(card).join("")}` : ""}

${closing.length ? `<h2>Closing soon <span class="count">${closing.length}</span></h2>
${closing.slice(0, 8).map(card).join("")}` : ""}

${claimsRows ? `<h2>Your claims <span class="count">${filed.length}</span></h2>
<div class="scroll"><table><thead><tr><th>Settlement</th><th>State</th><th>Filed</th>
<th>Amount</th></tr></thead><tbody>${claimsRows}</tbody></table></div>` : ""}

<h2>Everything tracked <span class="count">${tracked.length}</span></h2>
${rows ? `<div class="scroll"><table><thead><tr><th>You</th><th>Value</th><th>Deadline</th>
<th>Stage</th><th>Settlement</th></tr></thead><tbody>${rows}</tbody></table></div>`
    : `<div class="empty">Nothing yet. A scan runs every ten minutes and works through the
       registry a few pages at a time, so a cold store fills in over a day.</div>`}

${hiddenCount ? `<p class="sub" style="margin-top:1rem">${hiddenCount} settlement${
      hiddenCount === 1 ? " is" : "s are"} hidden by your preferences.
  <a href="/preferences">Review them</a>.</p>` : ""}

<div style="margin-top:1.6rem;display:flex;gap:.45rem;align-items:center;flex-wrap:wrap">
  <form class="inline" method="post" action="/scan"><button>Scan now</button></form>
  <span class="sub" style="margin:0">Reads every source. Costs cents on a cold store and
    almost nothing after that.</span>
</div>`;

  const who = user === "owner" ? "" : ` &middot; ${esc(user)}`;
  return html(layout("Dashboard", body, {
    nav: "today",
    footer: `glean &middot; ${v.byId.size} proceedings tracked${who}`,
  }));
}

async function showPage(store: Store, user: string, id: string): Promise<Response> {
  const s = await store.settlement(id);
  if (!s) return new Response("not found", { status: 404 });
  const profile = await store.profile(user);
  const a = assess(s, profile);
  const claim = await store.claim(user, id);
  const term = mutedBy(s, profile);
  const under = underBar(s, a, profile);
  const back = "/s/" + s.id;

  const groupsHTML = a.groups.map((g) => {
    if (g.criteria.length > 1) {
      return `<p class="crit"><b>any one of</b>, currently ${esc(g.valueStr)}</p>` +
        g.criteria.map((c) =>
          `<p class="crit" style="padding-left:1.2rem"><b>${esc(c.valueStr)}</b> ${esc(c.reason)}</p>`).join("");
    }
    return g.criteria.map((c) =>
      `<p class="crit"><b>${esc(c.valueStr)}</b> ${esc(c.reason)}</p>`).join("");
  }).join("");

  const states: ClaimState[] = ["interested", "filed", "paid", "skipped"];
  const suggestion = suggestTerm(s);

  const body = `
<p class="sub" style="margin-bottom:.6rem"><a href="/">&larr; Back to today</a></p>
<h1>${esc(s.title)}</h1>
<p class="sub lede">${esc(s.summary)}</p>

${term ? `<p class="note">Hidden from your lists by the term <b>${esc(term)}</b>.
  ${action("/unmute", { term, back }, "Stop hiding it", "tiny")}</p>` : ""}
${under ? `<p class="note">Kept out of your action lists: ${esc(under)}.
  <a href="/preferences">Change your thresholds</a>.</p>` : ""}

<div class="card">
  <div class="meta" style="margin-top:0">
    <span class="chip">${esc(s.stage)}</span>
    <span class="chip ${deadlineClass(s)}">${esc(deadlineText(s))}</span>
    ${a.payout > 0 ? `<span class="chip">about $${a.payout.toFixed(0)} to you</span>` : ""}
    <span>${s.effort_minutes || 15} min</span>
    ${s.proof_required ? `<span>proof: ${esc(s.proof_note || "required")}</span>` : ""}
    ${s.administrator ? `<span>${esc(s.administrator)}</span>` : ""}
  </div>
  <div class="actions">
    ${s.claim_url ? `<a class="btn" href="${esc(s.claim_url)}" rel="noreferrer noopener"
      target="_blank">Official claim portal</a>` : ""}
    <a class="btn" href="${esc(s.url)}" rel="noreferrer noopener" target="_blank">Page glean read</a>
    ${suggestion && !term
      ? action("/mute", { term: suggestion, back: "/" }, `Hide anything about ${suggestion}`, "quiet")
      : ""}
  </div>
</div>

<h2>Are you in the class?</h2>
<p style="margin-bottom:.6rem"><span class="chip ${a.verdictStr === "yes" ? "yes" : ""}">${
    esc(verdictWord(a))}</span></p>
${a.note ? `<p class="crit">${esc(a.note)}</p>` : ""}
${s.class_text ? `<blockquote>${esc(s.class_text)}</blockquote>` : ""}
${groupsHTML}

${a.questions.length ? `<h2>Resolve it</h2>${
    a.questions.map((q) => questionCard(q, back)).join("")}` : ""}

<h2>Your claim</h2>
<div class="q"><div class="ask">Currently <b>${esc(claim?.state ?? "new")}</b>${
    claim?.filed_on ? `<div class="why">filed ${esc(claim.filed_on)}</div>` : ""}</div>
  <div class="buttons">${states.map((st) =>
    action("/claim", { id: s.id, state: st, back }, st, st === claim?.state ? "primary" : "")).join(" ")}
  </div></div>

<h2>Provenance</h2>
<div class="scroll"><table><tbody>
  <tr><td>source</td><td class="wrap">${esc(s.source_id)}</td></tr>
  <tr><td>extracted by</td><td class="wrap">${esc(s.extractor)}, confidence ${
    Math.round(s.confidence * 100)}%</td></tr>
  <tr><td>first seen</td><td>${esc(s.first_seen)}</td></tr>
  <tr><td>last seen</td><td>${esc(s.last_seen)}</td></tr>
  ${s.case_name ? `<tr><td>case</td><td class="wrap">${esc(s.case_name)}</td></tr>` : ""}
  ${s.court ? `<tr><td>court</td><td class="wrap">${esc(s.court)}</td></tr>` : ""}
  ${s.settlement_fund ? `<tr><td>fund</td><td>$${s.settlement_fund.toFixed(0)}</td></tr>` : ""}
</tbody></table></div>`;

  return html(layout(s.title, body, { nav: "today", footer: `glean &middot; ${esc(s.id)}` }));
}

/**
 * Preferences: the answers on file, the thresholds, and the subjects to stop
 * showing.
 *
 * It doubles as the page that says where any of this is kept, because a program
 * holding a list of somebody's banks and shops owes them a plain answer to
 * that, and a link that hands the whole thing back.
 */
async function preferencesPage(store: Store, user: string): Promise<Response> {
  const v = await viewOf(store, user);
  const p = v.profile;
  const back = "/preferences";

  const answered = p.holdings.map((h) =>
    `<span class="tag ${h.negative ? "no" : ""}">${h.negative ? "not " : ""}${esc(h.label || h.entity)}
      <form method="post" action="/forget">
        <input type="hidden" name="kind" value="${esc(h.kind)}">
        <input type="hidden" name="entity" value="${esc(h.entity)}">
        <input type="hidden" name="back" value="${esc(back)}">
        <button title="Forget this answer" aria-label="Forget this answer">&times;</button>
      </form></span>`).join("");

  const provinces = p.residences.map((r) =>
    `<span class="tag">${esc(PROVINCES[r.province.toUpperCase()] ?? r.province)}
      <form method="post" action="/province">
        <input type="hidden" name="code" value="${esc(r.province)}">
        <input type="hidden" name="drop" value="1">
        <input type="hidden" name="back" value="${esc(back)}">
        <button title="Remove" aria-label="Remove">&times;</button>
      </form></span>`).join("");

  const options = Object.entries(PROVINCES)
    .map(([code, name]) => `<option value="${esc(code)}">${esc(name)}</option>`).join("");

  const hiddenCards = v.muted.map(({ a, term }) => {
    const s = v.byId.get(a.settlementId)!;
    return `<div class="card"><div class="top">
      <h3><a href="/s/${esc(s.id)}">${esc(s.title)}</a></h3>
      <span class="chip">${esc(term)}</span></div>
      <div class="actions">${action("/unmute", { term, back }, `Stop hiding "${term}"`, "quiet")}</div>
    </div>`;
  }).join("");

  const asideCards = v.skipped.map((a) =>
    settlementCard(a, v.byId.get(a.settlementId)!, { skipped: true })).join("");

  const belowRows = v.belowBar.map((a) => {
    const s = v.byId.get(a.settlementId)!;
    return `<tr><td class="wrap"><a href="/s/${esc(s.id)}">${esc(s.title)}</a></td>
      <td>${esc(underBar(s, a, p))}</td></tr>`;
  }).join("");

  const body = `
<h1>Preferences</h1>
<p class="sub lede">What glean knows about you, and what it should stop bringing up.</p>

<h2>Hide a subject</h2>
<div class="panel">
  <h3>Muted terms</h3>
  <p class="sub">A term hides a settlement from the queue and from the digest that goes to
    your phone. It is matched on whole words against the title, the summary and the class
    definition, so <b>hockey</b> catches the major junior case through its criteria even
    though the word is nowhere in its title, while <b>gm</b> does not hide everything that
    happens to mention management.</p>
  ${muteTags(p.mutes ?? [], back)}
  <form class="add" method="post" action="/mute">
    <input type="hidden" name="back" value="${esc(back)}">
    <div class="grow"><label for="term">Term to hide</label>
      <input id="term" name="term" type="text" placeholder="hockey" autocapitalize="none"
        spellcheck="false" required></div>
    <button class="primary">Hide it</button>
  </form>
  ${v.muted.length ? `<p class="sub" style="margin:.9rem 0 0">Hiding ${v.muted.length}
    settlement${v.muted.length === 1 ? "" : "s"} right now, listed below.</p>` : ""}
</div>

<h2>Answers on file <span class="count">${p.holdings.length}</span></h2>
<div class="panel">
  <h3>What you have told glean</h3>
  <p class="sub">Each of these came from a Yes or No in the queue. Removing one puts its
    question back.</p>
  ${answered ? `<div class="tags">${answered}</div>`
    : `<p class="sub" style="margin:0">Nothing answered yet.</p>`}
</div>
<div class="panel">
  <h3>Where you have lived</h3>
  <p class="sub">Provincial classes turn on this. Canada on its own cannot answer a class
    restricted to one province, so add the ones that apply.</p>
  ${provinces ? `<div class="tags">${provinces}</div>`
    : `<p class="sub" style="margin:0 0 .9rem">No province on file, so every provincial
       class is unknown rather than yours.</p>`}
  <form class="add" method="post" action="/province">
    <input type="hidden" name="back" value="${esc(back)}">
    <div class="grow"><label for="code">Add a province</label>
      <select id="code" name="code">${options}</select></div>
    <button class="primary">Add</button>
  </form>
</div>

<h2>What is worth your evening</h2>
<div class="panel">
  <h3>Thresholds</h3>
  <p class="sub">A claim worth eleven dollars that wants a receipt from 2009 is a real
    settlement and a poor use of an evening. Anything under the floor or over the limit
    stays in the tracked table and out of the lists that ask you to act.</p>
  <form method="post" action="/thresholds">
    <input type="hidden" name="back" value="${esc(back)}">
    <div class="row2">
      <div><label for="min_payout">Ignore claims paying less than</label>
        <input id="min_payout" name="min_payout" type="number" min="0" step="1"
          value="${p.min_payout}"></div>
      <div><label for="max_effort">Ignore claims taking longer than (minutes)</label>
        <input id="max_effort" name="max_effort" type="number" min="0" step="5"
          value="${p.max_effort_minutes}"></div>
      <div><button class="primary">Save</button></div>
    </div>
  </form>
  ${belowRows ? `<div class="scroll" style="margin-top:1rem"><table><thead><tr>
    <th>Kept out of your lists</th><th>Why</th></tr></thead><tbody>${belowRows}</tbody></table></div>` : ""}
</div>

${hiddenCards ? `<h2>Hidden by a term <span class="count">${v.muted.length}</span></h2>${hiddenCards}` : ""}
${asideCards ? `<h2>Put aside <span class="count">${v.skipped.length}</span></h2>${asideCards}` : ""}

<h2>Your history</h2>
<div class="panel">
  <h3>Where this is kept</h3>
  <p class="sub">All of it is in glean's own D1 database on Cloudflare, in three tables,
    each row tagged with the identity you signed in as (<b>${esc(user)}</b>).</p>
  <div class="scroll"><table><thead><tr><th>Table</th><th>Holds</th><th>Yours</th></tr></thead>
  <tbody>
    <tr><td>profile</td><td class="wrap">Your answers, provinces, thresholds and muted terms</td>
      <td>${p.holdings.length} answer${p.holdings.length === 1 ? "" : "s"}</td></tr>
    <tr><td>claims</td><td class="wrap">Interested, filed, paid, skipped, and the dates</td>
      <td>${v.claims.size} record${v.claims.size === 1 ? "" : "s"}</td></tr>
    <tr><td>events</td><td class="wrap">The audit trail: what was discovered, what moved,
      what you answered</td><td><a href="/export.json">export</a></td></tr>
  </tbody></table></div>
  <p class="sub" style="margin:1rem 0 0">Settlements are deliberately not yours: they are
    public facts about proceedings, and scanning once for everybody is what makes it cheap.
    Nothing from your profile is ever sent to a model. Last updated ${esc(p.updated_on)}.</p>
  <div class="actions"><a class="btn" href="/export.json">Download everything as JSON</a></div>
</div>`;

  return html(layout("Preferences", body, {
    nav: "preferences", footer: `glean &middot; signed in as ${esc(user)}`,
  }));
}

/** The reader's own rows, handed back whole. */
async function exportJson(store: Store, user: string): Promise<Response> {
  const [profile, claims] = await Promise.all([store.profile(user), store.claims(user)]);
  const body = JSON.stringify({
    exported_at: new Date().toISOString(),
    user,
    profile,
    claims,
    note: "Settlements are not included: they are public records, not yours.",
  }, null, 2);
  return new Response(body, {
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Content-Disposition": 'attachment; filename="glean-export.json"',
      "Cache-Control": "no-store",
      "X-Robots-Tag": "noindex, nofollow",
    },
  });
}

async function postAnswer(req: Request, store: Store, user: string): Promise<Response> {
  const form = await req.formData();
  const raw = String(form.get("id") ?? "");
  const [kindStr, entity] = raw.split(":");
  if (!kindStr || !entity || !validKind(kindStr)) {
    return new Response("malformed question id", { status: 400 });
  }
  const negative = String(form.get("a") ?? "") !== "yes";
  const label = String(form.get("label") ?? "") || entity;
  const profile = await store.profile(user);

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

  await store.saveProfile(user, profile);
  await store.event("answered", "", { kind: kindStr, entity, negative }, user);
  return redirect(String(form.get("back") ?? "/"));
}

async function postClaim(req: Request, store: Store, user: string): Promise<Response> {
  const form = await req.formData();
  const id = String(form.get("id") ?? "");
  if (!(await store.settlement(id))) return new Response("no such settlement", { status: 404 });
  const state = String(form.get("state") ?? "") as ClaimState;
  if (!["new", "interested", "filed", "paid", "rejected", "skipped"].includes(state)) {
    return new Response("unknown state", { status: 400 });
  }
  const prev = await store.claim(user, id);
  const c: Claim = prev ?? {
    settlement_id: id, state: "new", filed_on: "", paid_on: "",
    amount: 0, updated_on: "",
  };
  c.state = state;
  const day = new Date().toISOString().slice(0, 10);
  if (state === "filed" && !c.filed_on) c.filed_on = day;
  if (state === "paid") c.paid_on = day;
  await store.setClaim(user, c);
  return redirect(String(form.get("back") ?? "") || "/s/" + id);
}

async function postMute(req: Request, store: Store, user: string): Promise<Response> {
  const form = await req.formData();
  const term = String(form.get("term") ?? "");
  const profile = await store.profile(user);
  if (addMute(profile, term)) {
    await store.saveProfile(user, profile);
    await store.event("muted", "", { term }, user);
  }
  return redirect(String(form.get("back") ?? "/preferences"));
}

async function postUnmute(req: Request, store: Store, user: string): Promise<Response> {
  const form = await req.formData();
  const term = String(form.get("term") ?? "");
  const profile = await store.profile(user);
  if (removeMute(profile, term)) {
    await store.saveProfile(user, profile);
    await store.event("unmuted", "", { term }, user);
  }
  return redirect(String(form.get("back") ?? "/preferences"));
}

/**
 * Drop an answer. The question it settled comes back into the queue, which is
 * the point: an answer given by mistake is otherwise permanent, and the profile
 * is the one thing here nobody else can reconstruct.
 */
async function postForget(req: Request, store: Store, user: string): Promise<Response> {
  const form = await req.formData();
  const kind = String(form.get("kind") ?? "");
  const key = normalizeEntity(String(form.get("entity") ?? ""));
  const profile = await store.profile(user);
  const before = profile.holdings.length;
  profile.holdings = profile.holdings.filter(
    (h) => !(h.kind === kind && normalizeEntity(h.entity) === key));
  if (profile.holdings.length !== before) {
    await store.saveProfile(user, profile);
    await store.event("forgot", "", { kind, entity: key }, user);
  }
  return redirect(String(form.get("back") ?? "/preferences"));
}

async function postProvince(req: Request, store: Store, user: string): Promise<Response> {
  const form = await req.formData();
  const code = normalizeProvince(String(form.get("code") ?? ""));
  if (!code) return new Response("unknown province", { status: 400 });
  const drop = String(form.get("drop") ?? "") === "1";
  const profile = await store.profile(user);
  profile.residences = profile.residences.filter((r) => r.province.toUpperCase() !== code);
  // Naming a province supersedes the bare "Canada" a profile starts with, since
  // keeping both leaves the vague one answering questions the specific one can
  // now answer properly.
  if (!drop) {
    if (code !== "CA") profile.residences = profile.residences.filter((r) => r.province !== "CA");
    profile.residences.push({ province: code, window: { from: "", until: "" } });
  }
  await store.saveProfile(user, profile);
  await store.event("residence", "", { code, drop }, user);
  return redirect(String(form.get("back") ?? "/preferences"));
}

async function postThresholds(req: Request, store: Store, user: string): Promise<Response> {
  const form = await req.formData();
  const profile = await store.profile(user);
  const min = Number(form.get("min_payout"));
  const effort = Number(form.get("max_effort"));
  if (Number.isFinite(min) && min >= 0) profile.min_payout = Math.min(min, 1_000_000);
  if (Number.isFinite(effort) && effort >= 0) profile.max_effort_minutes = Math.min(effort, 10_000);
  await store.saveProfile(user, profile);
  return redirect(String(form.get("back") ?? "/preferences"));
}

/** Only same-site paths, so a crafted form cannot make this an open redirect. */
function redirect(to: string): Response {
  const safe = to.startsWith("/") && !to.startsWith("//") ? to : "/";
  return new Response(null, { status: 303, headers: { Location: safe } });
}

function html(body: string, status = 200): Response {
  return new Response(body, {
    status,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Robots-Tag": "noindex, nofollow",
      "Referrer-Policy": "no-referrer",
      "X-Content-Type-Options": "nosniff",
    },
  });
}
