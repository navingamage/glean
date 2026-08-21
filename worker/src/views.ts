/**
 * The dashboard, rendered on the server.
 *
 * No client framework and no API: the whole application is one person's list of
 * a few dozen settlements, and every screen is a form post away from the next.
 * The pages that matter are the question queue, which is how the profile fills
 * in, and the per-settlement page, which shows the reasoning rather than only
 * the verdict. The value of knowing you are probably in a class is entirely in
 * being able to check why.
 *
 * The styling is one inline sheet. It is a Worker with no build step and no
 * asset pipeline, so a stylesheet would be a second request for two kilobytes,
 * and a font from a CDN would put a third party in front of a private page.
 */
import { Assessment, Question } from "./match";
import { Mute, Settlement, daysUntil, expired } from "./model";

export function esc(s: unknown): string {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]!));
}

const STYLE = `
:root{
  --bg:#f7f6f3; --surface:#fff; --surface-2:#f2f0eb;
  --ink:#161513; --ink-2:#3f3c37; --muted:#75706a;
  --line:#e5e1d9; --line-2:#d6d1c7;
  --accent:#0c6b4e; --accent-ink:#fff; --accent-soft:#e3f1ea;
  --warn:#9a5610; --warn-soft:#fbeedd;
  --bad:#8d2f1e; --bad-soft:#fbe7e2;
  --shadow:0 1px 2px rgba(20,18,14,.05), 0 4px 14px rgba(20,18,14,.05);
  --radius:14px;
}
@media (prefers-color-scheme:dark){
  :root{
    --bg:#111110; --surface:#1a1a18; --surface-2:#222220;
    --ink:#f0eeea; --ink-2:#cbc7c0; --muted:#948f87;
    --line:#2b2a27; --line-2:#3a3835;
    --accent:#4fbf94; --accent-ink:#08130f; --accent-soft:#16302a;
    --warn:#e2a763; --warn-soft:#2e2418;
    --bad:#e5867a; --bad-soft:#31201d;
    --shadow:0 1px 2px rgba(0,0,0,.4), 0 6px 18px rgba(0,0,0,.28);
  }
}
*,*::before,*::after{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--ink);
  font:400 15.5px/1.6 ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
  -webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility}
a{color:var(--accent);text-decoration:none}
a:hover{text-decoration:underline}
:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:4px}

/* Top bar */
.bar{position:sticky;top:0;z-index:20;background:color-mix(in srgb,var(--bg) 88%,transparent);
  backdrop-filter:saturate(1.4) blur(10px);border-bottom:1px solid var(--line)}
.bar-in{max-width:62rem;margin:0 auto;padding:.65rem 1.15rem;display:flex;align-items:center;gap:1.1rem}
.mark{font-weight:650;letter-spacing:-.02em;font-size:1.05rem;color:var(--ink)}
.mark:hover{text-decoration:none}
.bar nav{display:flex;gap:.25rem;margin-right:auto}
.bar nav a{color:var(--muted);font-size:.88rem;padding:.3rem .6rem;border-radius:8px}
.bar nav a:hover{background:var(--surface-2);color:var(--ink);text-decoration:none}
.bar nav a[aria-current]{background:var(--surface);color:var(--ink);box-shadow:var(--shadow)}

main{max-width:62rem;margin:0 auto;padding:2.2rem 1.15rem 5rem}
h1{font-size:1.75rem;line-height:1.2;letter-spacing:-.022em;font-weight:650;margin:0 0 .35rem}
h2{font-size:.76rem;text-transform:uppercase;letter-spacing:.1em;color:var(--muted);
  font-weight:600;margin:2.6rem 0 .85rem;display:flex;align-items:baseline;gap:.5rem}
h2 .count{color:var(--ink);font-variant-numeric:tabular-nums}
h3{margin:0;font-size:1.02rem;font-weight:600;letter-spacing:-.01em}
p{margin:0 0 1rem}
.sub{color:var(--muted);font-size:.93rem;margin:0 0 1.6rem}
.lede{color:var(--ink-2);font-size:1rem}

/* Numbers across the top */
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(9.5rem,1fr));gap:.65rem;margin:1.6rem 0 .4rem}
.stat{background:var(--surface);border:1px solid var(--line);border-radius:var(--radius);
  padding:.85rem .95rem;box-shadow:var(--shadow)}
.stat .n{font-size:1.45rem;font-weight:650;letter-spacing:-.02em;font-variant-numeric:tabular-nums;
  display:block;line-height:1.15}
.stat .l{color:var(--muted);font-size:.78rem;margin-top:.15rem;display:block}
.stat.go .n{color:var(--accent)}
.stat.soon .n{color:var(--warn)}

/* Cards */
.card{background:var(--surface);border:1px solid var(--line);border-radius:var(--radius);
  padding:1rem 1.1rem;margin-bottom:.7rem;box-shadow:var(--shadow);
  transition:transform .12s ease,border-color .12s ease}
.card:hover{transform:translateY(-1px);border-color:var(--line-2)}
.card .top{display:flex;justify-content:space-between;align-items:baseline;gap:1rem}
.card h3 a{color:var(--ink)}
.amount{font-variant-numeric:tabular-nums;font-weight:650;white-space:nowrap;letter-spacing:-.01em}
.meta{color:var(--muted);font-size:.84rem;display:flex;flex-wrap:wrap;align-items:center;
  gap:.4rem .7rem;margin-top:.55rem}
.actions{display:flex;flex-wrap:wrap;gap:.4rem;margin-top:.75rem;align-items:center}

/* Chips */
.chip{display:inline-flex;align-items:center;gap:.3rem;background:var(--surface-2);
  border-radius:999px;padding:.14rem .58rem;font-size:.76rem;color:var(--ink-2);white-space:nowrap}
.chip.yes{background:var(--accent-soft);color:var(--accent);font-weight:600}
.chip.soon{background:var(--warn-soft);color:var(--warn);font-weight:600}
.chip.past{background:var(--bad-soft);color:var(--bad)}
.yes{color:var(--accent);font-weight:600}
.soon{color:var(--warn);font-weight:600}
.past{color:var(--bad)}

/* Controls */
button,.btn{font:inherit;font-size:.88rem;background:var(--surface);color:var(--ink);
  border:1px solid var(--line-2);border-radius:9px;padding:.36rem .8rem;cursor:pointer;
  line-height:1.4;transition:background .12s ease,border-color .12s ease,color .12s ease}
button:hover,.btn:hover{background:var(--surface-2);border-color:var(--accent);text-decoration:none}
button.primary{background:var(--accent);color:var(--accent-ink);border-color:var(--accent);font-weight:600}
button.primary:hover{filter:brightness(1.08);background:var(--accent)}
button.quiet{background:transparent;border-color:transparent;color:var(--muted)}
button.quiet:hover{background:var(--surface-2);color:var(--ink);border-color:transparent}
button.tiny{font-size:.78rem;padding:.16rem .5rem}
form.inline{display:inline}
label{display:block;font-size:.8rem;color:var(--muted);margin:0 0 .25rem}
input[type=text],input[type=password],input[type=number],select{font:inherit;font-size:.92rem;
  width:100%;padding:.45rem .65rem;background:var(--bg);color:var(--ink);
  border:1px solid var(--line-2);border-radius:9px}
input:focus,select:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)}

/* Questions */
.q{background:var(--surface);border:1px solid var(--line);border-radius:var(--radius);
  padding:.85rem 1.1rem;margin-bottom:.55rem;display:flex;justify-content:space-between;
  align-items:center;gap:1rem;flex-wrap:wrap;box-shadow:var(--shadow)}
.q .ask{font-size:.98rem}
.q .why{color:var(--muted);font-size:.8rem;margin-top:.2rem}
.q .buttons{display:flex;gap:.35rem;flex-shrink:0}

/* Panels, on the preferences page */
.panel{background:var(--surface);border:1px solid var(--line);border-radius:var(--radius);
  padding:1.1rem 1.2rem;margin-bottom:.7rem;box-shadow:var(--shadow)}
.panel h3{margin-bottom:.2rem}
.panel .sub{margin-bottom:1rem;font-size:.87rem}
.tags{display:flex;flex-wrap:wrap;gap:.4rem;margin:0 0 .9rem}
.tag{display:inline-flex;align-items:center;gap:.4rem;background:var(--surface-2);
  border:1px solid var(--line);border-radius:999px;padding:.2rem .35rem .2rem .7rem;font-size:.85rem}
.tag.no{color:var(--muted)}
.tag form{display:flex}
.tag button{background:transparent;border:none;color:var(--muted);padding:0 .25rem;
  font-size:1rem;line-height:1;border-radius:999px}
.tag button:hover{color:var(--bad);background:transparent}
.add{display:flex;gap:.45rem;align-items:flex-end;flex-wrap:wrap}
.add .grow{flex:1 1 14rem}
.row2{display:grid;grid-template-columns:repeat(auto-fit,minmax(10rem,1fr));gap:.7rem;align-items:end}

/* Tables */
.scroll{overflow-x:auto;border:1px solid var(--line);border-radius:var(--radius);
  background:var(--surface);box-shadow:var(--shadow)}
table{width:100%;border-collapse:collapse;font-size:.9rem}
td,th{text-align:left;padding:.6rem .8rem;border-bottom:1px solid var(--line);white-space:nowrap}
td.wrap{white-space:normal;min-width:16rem}
tr:last-child td{border-bottom:none}
th{color:var(--muted);font-weight:600;font-size:.74rem;text-transform:uppercase;
  letter-spacing:.07em;background:var(--surface-2)}
tbody tr:hover td{background:var(--surface-2)}

/* Odds and ends */
.crit{font-size:.88rem;margin:.2rem 0;color:var(--muted)}
.crit b{color:var(--ink);font-weight:600}
blockquote{margin:.6rem 0 1rem;padding:.7rem 1rem;border-left:3px solid var(--line-2);
  background:var(--surface-2);border-radius:0 9px 9px 0;color:var(--ink-2);font-size:.9rem}
.empty{background:var(--surface);border:1px dashed var(--line-2);border-radius:var(--radius);
  padding:1.4rem;color:var(--muted);font-size:.92rem;text-align:center}
.note{background:var(--accent-soft);border-radius:var(--radius);padding:.8rem 1rem;
  font-size:.88rem;color:var(--ink-2);margin-bottom:1rem}
.bad-note{background:var(--bad-soft);color:var(--bad);border-radius:9px;padding:.55rem .8rem;
  font-size:.87rem;margin:0 0 .9rem}
footer{color:var(--muted);font-size:.8rem;margin-top:4rem;border-top:1px solid var(--line);
  padding-top:1.1rem;line-height:1.6}

/* Sign in */
.login{max-width:21rem;margin:5rem auto 0}
.login .field{margin-bottom:.85rem}
.login button{width:100%;padding:.5rem}
.login h1{text-align:center}
.login .sub{text-align:center}

@media (max-width:640px){
  main{padding:1.5rem .95rem 4rem}
  h1{font-size:1.45rem}
  .q{align-items:flex-start}
  .q .buttons{width:100%}
  .q .buttons form{flex:1}
  .q .buttons button{width:100%}
}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
`;

export interface PageOpts {
  /** Which nav item is the current page. */
  nav?: "today" | "preferences";
  footer?: string;
  /** Sign-in and other pages that should not show the navigation. */
  bare?: boolean;
}

export function layout(title: string, body: string, opts: PageOpts = {}): string {
  const link = (href: string, label: string, key: string) =>
    `<a href="${href}"${opts.nav === key ? ' aria-current="page"' : ""}>${label}</a>`;
  const bar = opts.bare ? "" : `<header class="bar"><div class="bar-in">
  <a class="mark" href="/">glean</a>
  <nav>${link("/", "Today", "today")}${link("/preferences", "Preferences", "preferences")}</nav>
  <form method="post" action="/logout"><button class="quiet">Sign out</button></form>
</div></header>`;
  return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta name="color-scheme" content="light dark">
<title>${esc(title)} &middot; glean</title><style>${STYLE}</style></head>
<body>${bar}<main>${body}
<footer>${opts.footer ?? "glean"}<br>Everything here is read off public web pages by a
program. Check the official notice before you rely on a date.</footer></main></body></html>`;
}

export function deadlineText(s: Settlement): string {
  if (!s.claim_deadline) return "no deadline given";
  const d = daysUntil(s.claim_deadline);
  if (d < 0) return `closed ${s.claim_deadline}`;
  if (d === 0) return "closes today";
  if (d === 1) return "closes tomorrow";
  if (d <= 45) return `${d} days left`;
  return `closes ${s.claim_deadline}`;
}

export function deadlineClass(s: Settlement): string {
  if (expired(s)) return "past";
  if (s.claim_deadline && daysUntil(s.claim_deadline) <= 30) return "soon";
  return "";
}

export function verdictWord(a: Assessment): string {
  if (a.verdictStr === "yes") return "you are in the class";
  if (a.verdictStr === "no") return "not you";
  return a.probability > 0 ? `${Math.round(a.probability * 100)}% likely yours` : "eligibility unknown";
}

/** The same judgement in one word, for a table cell. */
export function verdictShort(a: Assessment): string {
  if (a.verdictStr === "yes") return "yes";
  if (a.verdictStr === "no") return "no";
  return a.probability > 0 ? `${Math.round(a.probability * 100)}%` : "?";
}

export function valueWord(a: Assessment): string {
  if (a.payout <= 0) return "?";
  return a.verdictStr === "yes"
    ? `$${a.payout.toFixed(0)}`
    : `~$${a.expectedValue.toFixed(0)}`;
}

/**
 * "unlocks about $0" reads as a payout of zero rather than as a payout the
 * notice never stated, which is a different and more discouraging claim.
 */
export function worthWord(q: Question): string {
  const n = q.unlocks.length;
  const plural = n === 1 ? "" : "s";
  return q.worth > 0
    ? `unlocks about $${q.worth.toFixed(0)} across ${n} settlement${plural}`
    : `settles ${n} settlement${plural}, none of which states a payout`;
}

export function money(n: number): string {
  return n >= 1000 ? `$${(n / 1000).toFixed(1)}k` : `$${n.toFixed(0)}`;
}

function hidden(name: string, value: string): string {
  return `<input type="hidden" name="${esc(name)}" value="${esc(value)}">`;
}

/** A one-button form, which is how every action on these pages is taken. */
export function action(
  path: string, fields: Record<string, string>, label: string, cls = "",
): string {
  const inputs = Object.entries(fields).map(([k, v]) => hidden(k, v)).join("");
  return `<form class="inline" method="post" action="${esc(path)}">${inputs}
    <button class="${cls}">${esc(label)}</button></form>`;
}

export function questionCard(q: Question, back = ""): string {
  const fields: Record<string, string> = { id: q.id, label: q.label };
  if (back) fields.back = back;
  return `<div class="q">
  <div><div class="ask">${esc(q.ask)}</div>
    <div class="why">${esc(worthWord(q))}</div></div>
  <div class="buttons">
    ${action("/answer", { ...fields, a: "yes" }, "Yes", "primary")}
    ${action("/answer", { ...fields, a: "no" }, "No")}
  </div></div>`;
}

export interface CardOpts {
  /** Offer a term to mute alongside the settlement itself. */
  suggest?: string;
  /** A settlement already put aside shows the way back instead. */
  skipped?: boolean;
}

export function settlementCard(a: Assessment, s: Settlement, opts: CardOpts = {}): string {
  const dClass = deadlineClass(s);
  return `<div class="card">
  <div class="top"><h3><a href="/s/${esc(s.id)}">${esc(s.title)}</a></h3>
    <span class="amount">${a.payout > 0 ? "$" + a.payout.toFixed(0) : "?"}</span></div>
  <div class="meta">
    <span class="chip ${a.verdictStr === "yes" ? "yes" : ""}">${esc(verdictWord(a))}</span>
    <span class="chip ${dClass}">${esc(deadlineText(s))}</span>
    <span>${s.effort_minutes || 15} min</span>
    ${s.proof_required ? "<span>proof required</span>" : ""}
  </div>
  <div class="actions">
    ${s.claim_url ? `<a class="btn" href="${esc(s.claim_url)}" rel="noreferrer noopener" target="_blank">File a claim</a>` : ""}
    <a class="btn" href="/s/${esc(s.id)}">Details</a>
    ${opts.skipped
      ? action("/claim", { id: s.id, state: "new", back: "/preferences" }, "Put it back", "quiet")
      : action("/claim", { id: s.id, state: "skipped", back: "/" }, "Not interested", "quiet")}
    ${opts.suggest && !opts.skipped
      ? action("/mute", { term: opts.suggest, back: "/" }, `Hide anything about ${opts.suggest}`, "quiet")
      : ""}
  </div></div>`;
}

export function muteTags(mutes: Mute[], back: string): string {
  if (!mutes.length) {
    return `<p class="sub" style="margin:0 0 .9rem">Nothing is hidden yet.</p>`;
  }
  return `<div class="tags">${mutes.map((m) =>
    `<span class="tag">${esc(m.term)}
      <form method="post" action="/unmute">${hidden("term", m.term)}${hidden("back", back)}
        <button title="Stop hiding ${esc(m.term)}" aria-label="Stop hiding ${esc(m.term)}">&times;</button>
      </form></span>`).join("")}</div>`;
}

/**
 * The sign-in page.
 *
 * Deliberately says nothing about which half of a wrong guess was wrong, and
 * says the default credentials out loud only while they are still the
 * defaults, so a demo is openable without a hint sitting on a real deployment.
 */
export function loginPage(opts: { failed?: boolean; hint?: boolean; next?: string } = {}): string {
  const next = opts.next && opts.next.startsWith("/") && !opts.next.startsWith("//")
    ? opts.next : "/";
  const body = `<form class="login" method="post" action="/login">
  <h1>glean</h1>
  <p class="sub">Sign in to see what you can claim.</p>
  ${opts.failed ? `<p class="bad-note">That username and password did not match.</p>` : ""}
  ${hidden("next", next)}
  <div class="field"><label for="username">Username</label>
    <input id="username" name="username" type="text" autocomplete="username"
      autocapitalize="none" autocorrect="off" spellcheck="false" required autofocus></div>
  <div class="field"><label for="password">Password</label>
    <input id="password" name="password" type="password"
      autocomplete="current-password" required></div>
  <button class="primary">Sign in</button>
  ${opts.hint ? `<p class="sub" style="margin:1rem 0 0">test / test</p>` : ""}
</form>`;
  return layout("Sign in", body, { bare: true, footer: "glean" });
}
