/**
 * The dashboard, rendered on the server.
 *
 * No client framework and no API: the whole application is one person's list of
 * a few dozen settlements. The pages that matter are the question queue, which
 * is how the profile fills in, and the per-settlement page, which shows the
 * reasoning rather than only the verdict. The value of knowing you are probably
 * in a class is entirely in being able to check why.
 */
import { Assessment, Question } from "./match";
import { Settlement, daysUntil, expired } from "./model";

export function esc(s: unknown): string {
  return String(s ?? "").replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]!));
}

const STYLE = `
:root{--bg:#fbfaf8;--panel:#fff;--ink:#1b1a17;--muted:#6b6862;--line:#e4e0d9;
  --accent:#0f6d4f;--warn:#a8560f;--bad:#93321f;--chip:#f0ede7}
@media (prefers-color-scheme:dark){:root{--bg:#141412;--panel:#1c1c19;--ink:#eceae5;
  --muted:#98938a;--line:#2e2d29;--accent:#5fbf95;--warn:#e0a15c;--bad:#e08272;--chip:#26251f}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);
  font:15px/1.55 ui-sans-serif,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif}
main{max-width:60rem;margin:0 auto;padding:2rem 1.25rem 5rem}
a{color:var(--accent)}
h1{font-size:1.5rem;margin:0 0 .25rem;letter-spacing:-.01em}
h2{font-size:.8rem;text-transform:uppercase;letter-spacing:.09em;color:var(--muted);
  margin:2.5rem 0 .75rem;font-weight:600}
.sub{color:var(--muted);margin:0 0 2rem;font-size:.9rem}
.card{background:var(--panel);border:1px solid var(--line);border-radius:10px;
  padding:.9rem 1rem;margin-bottom:.6rem}
.card h3{margin:0 0 .3rem;font-size:1rem;font-weight:600}
.meta{color:var(--muted);font-size:.83rem;display:flex;flex-wrap:wrap;gap:.5rem .9rem}
.chip{background:var(--chip);border-radius:999px;padding:.1rem .55rem;font-size:.75rem}
.yes{color:var(--accent);font-weight:600}
.soon{color:var(--warn);font-weight:600}
.past{color:var(--bad)}
.row{display:flex;justify-content:space-between;align-items:baseline;gap:1rem}
.amount{font-variant-numeric:tabular-nums;font-weight:600;white-space:nowrap}
form.inline{display:inline}
button{font:inherit;background:var(--chip);color:var(--ink);border:1px solid var(--line);
  border-radius:6px;padding:.25rem .7rem;cursor:pointer}
button:hover{border-color:var(--accent)}
button.primary{background:var(--accent);color:var(--bg);border-color:var(--accent)}
.q{background:var(--panel);border:1px solid var(--line);border-radius:10px;
  padding:.8rem 1rem;margin-bottom:.5rem;display:flex;justify-content:space-between;
  align-items:center;gap:1rem;flex-wrap:wrap}
label{display:block;font-size:.8rem;color:var(--muted);margin:0 0 .2rem}
input[type=text],input[type=password]{font:inherit;width:100%;padding:.45rem .6rem;
  background:var(--bg);color:var(--ink);border:1px solid var(--line);border-radius:6px}
input:focus{outline:none;border-color:var(--accent)}
.login{max-width:20rem;margin:4rem auto 0}
.login .field{margin-bottom:.8rem}
.login button{width:100%;padding:.45rem}
.bad{color:var(--bad);font-size:.86rem;margin:0 0 .8rem}
table{width:100%;border-collapse:collapse;font-size:.9rem}
td,th{text-align:left;padding:.4rem .6rem;border-bottom:1px solid var(--line)}
th{color:var(--muted);font-weight:600;font-size:.78rem;text-transform:uppercase;letter-spacing:.06em}
.crit{font-size:.86rem;margin:.15rem 0;color:var(--muted)}
.crit b{color:var(--ink);font-weight:600}
blockquote{margin:.5rem 0;padding-left:.9rem;border-left:3px solid var(--line);
  color:var(--muted);font-size:.9rem}
footer{color:var(--muted);font-size:.8rem;margin-top:4rem;border-top:1px solid var(--line);padding-top:1rem}
`;

export function layout(title: string, body: string, footer: string): string {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>${esc(title)} &middot; glean</title><style>${STYLE}</style></head>
<body><main>${body}
<footer>${footer}<br>Everything here is read off public web pages by a program.
Check the official notice before you rely on a date.</footer></main></body></html>`;
}

export function deadlineText(s: Settlement): string {
  if (!s.claim_deadline) return "unknown";
  const d = daysUntil(s.claim_deadline);
  if (d < 0) return `${s.claim_deadline} (past)`;
  if (d === 0) return `${s.claim_deadline} (today)`;
  if (d === 1) return `${s.claim_deadline} (1 day)`;
  return `${s.claim_deadline} (${d}d)`;
}

export function deadlineClass(s: Settlement): string {
  if (expired(s)) return "past";
  if (s.claim_deadline && daysUntil(s.claim_deadline) <= 30) return "soon";
  return "";
}

export function verdictWord(a: Assessment): string {
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

export function questionCard(q: Question, back = ""): string {
  const hidden = back ? `<input type="hidden" name="back" value="${esc(back)}">` : "";
  const form = (answer: string, cls: string, label: string) =>
    `<form class="inline" method="post" action="/answer">
      <input type="hidden" name="id" value="${esc(q.id)}">
      <input type="hidden" name="label" value="${esc(q.label)}">
      <input type="hidden" name="a" value="${answer}">${hidden}
      <button class="${cls}">${label}</button></form>`;
  return `<div class="q"><div>${esc(q.ask)}
    <div class="meta"><span>${esc(worthWord(q))}</span></div></div>
    <div>${form("yes", "primary", "Yes")} ${form("no", "", "No")}</div></div>`;
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
  ${opts.failed ? `<p class="bad">That username and password did not match.</p>` : ""}
  <input type="hidden" name="next" value="${esc(next)}">
  <div class="field"><label for="username">Username</label>
    <input id="username" name="username" type="text" autocomplete="username"
      autocapitalize="none" autocorrect="off" spellcheck="false" required autofocus></div>
  <div class="field"><label for="password">Password</label>
    <input id="password" name="password" type="password"
      autocomplete="current-password" required></div>
  <button class="primary">Sign in</button>
  ${opts.hint ? `<p class="sub" style="margin:.9rem 0 0;text-align:center">test / test</p>` : ""}
</form>`;
  return layout("Sign in", body, "glean");
}
