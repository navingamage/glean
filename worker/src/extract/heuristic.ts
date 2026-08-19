/**
 * What regular expressions can see: dates, amounts, stage, proof.
 *
 * Free, offline, and right about deadlines far more often than it has any
 * business being, because notices are written to a template. It also decides
 * whether a page is a notice at all, which is the gate that keeps the model from
 * being handed several hundred partner biographies a day.
 */
import { Settlement, Stage } from "../model";

/** How much a page looks like a settlement notice. At or below zero, skip it. */
export function relevance(text: string): number {
  const low = text.toLowerCase();
  let score = 0;
  const signals: Record<string, number> = {
    "class action": 5, "class proceeding": 5, "action collective": 5,
    "recours collectif": 5, settlement: 4, "class member": 4,
    "claim form": 5, "claims administrator": 5, "claim deadline": 6,
    "opt out": 3, "opt-out": 3, "class period": 5, certified: 2,
    eligible: 2, compensation: 2, "notice of": 2, "court approved": 3,
    "file a claim": 5, "submit a claim": 5,
  };
  for (const [phrase, weight] of Object.entries(signals)) {
    const n = low.split(phrase).length - 1;
    if (n > 0) {
      score += weight;
      if (n > 3) score += Math.floor(weight / 2);
    }
  }
  // A page can mention class actions without being one. The firm's practice page
  // and its careers page both do.
  const noise: Record<string, number> = {
    "practice areas": 4, "our lawyers": 4, "join our team": 5,
    "privacy policy": 6, "terms of use": 6, cookie: 3,
  };
  for (const [phrase, weight] of Object.entries(noise)) {
    if (low.includes(phrase)) score -= weight;
  }
  if (text.split(/\s+/).length < 80) score -= 4;
  return score;
}

const MONTHS: Record<string, number> = {
  january: 1, jan: 1, janvier: 1,
  february: 2, feb: 2, fevrier: 2, "février": 2,
  march: 3, mar: 3, mars: 3,
  april: 4, apr: 4, avril: 4,
  may: 5, mai: 5,
  june: 6, jun: 6, juin: 6,
  july: 7, jul: 7, juillet: 7,
  august: 8, aug: 8, aout: 8, "août": 8,
  september: 9, sep: 9, sept: 9, septembre: 9,
  october: 10, oct: 10, octobre: 10,
  november: 11, nov: 11, novembre: 11,
  december: 12, dec: 12, decembre: 12, "décembre": 12,
};

/**
 * The window a cue may look in for its value: up to three lines, none containing
 * a sentence-ending period.
 *
 * Notices lay deadlines out as label and value in separate table cells and divs
 * at least as often as they write them in a sentence, and those become separate
 * lines in the extracted text. A cue that cannot cross a line break reads
 * "Opt-out deadline" and stops, one line above the date it wanted.
 */
const NEAR = String.raw`[^.\n]{0,60}(?:\n[^.\n]{0,60}){0,2}`;

const RE_MONTH_DAY = /\b([a-zéûôàA-ZÉÛÔÀ]{3,10})\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(\d{4})\b/;
const RE_DAY_MONTH = /\b(\d{1,2})(?:st|nd|rd|th)?\s+(?:de\s+)?([a-zéûôàA-ZÉÛÔÀ]{3,10})\.?,?\s+(\d{4})\b/;
const RE_ISO = /\b(\d{4})-(\d{2})-(\d{2})\b/;

/**
 * The hyphen is not decoration: notices and the press write "$26-million" about
 * as often as "$26 million", and reading the suffix as absent turns twenty-six
 * million dollars into twenty-six.
 */
const RE_MONEY = /\$\s?([\d,]+(?:\.\d+)?)\s*[-‐-―]?\s*(million|billion|m\b|b\b|k\b)?/i;

const cue = (body: string) => new RegExp(body + NEAR, "gi");

const DEADLINE_CUES = [
  cue(String.raw`claims?[- ]deadline`),
  cue(String.raw`deadline (?:to|for) (?:file|submit|apply)`),
  cue(String.raw`(?:claims?|claim forms?) must be (?:submitted|received|postmarked|filed)`),
  cue(String.raw`(?:file|submit) (?:your |a )?claim (?:by|before|on or before)`),
  cue(String.raw`claims? period (?:closes|ends)`),
  cue(String.raw`on or before`),
  cue(String.raw`deadline`),
];
const OPT_OUT_CUES = [
  cue(String.raw`opt[- ]?out deadline`),
  cue(String.raw`deadline (?:to|for) opt(?:ing)?[- ]out`),
  cue(String.raw`(?:to )?opt out`),
  cue(String.raw`exclude yourself`),
];
const PAYOUT_CUES = [
  cue(String.raw`up to \$\s?[\d,]+(?:\.\d+)?`),
  cue(String.raw`between \$\s?[\d,]+(?:\.\d+)? and \$\s?[\d,]+(?:\.\d+)?`),
  cue(String.raw`(?:each|per) (?:class member|claimant|eligible)`),
];
const FUND_CUES = [
  cue(String.raw`\$\s?[\d,.]+\s*(?:million|billion)?[- ]?(?:dollar )?settlement`),
  cue(String.raw`settlement (?:fund|amount)`),
  cue(String.raw`(?:agreed|settled) (?:to pay|for)`),
];

function valid(y: number, m: number, d: number): boolean {
  return y >= 1980 && y <= 2100 && m >= 1 && m <= 12 && d >= 1 && d <= 31;
}

const pad = (n: number) => String(n).padStart(2, "0");

/** The first date in a fragment of prose, as YYYY-MM-DD, or "". */
export function parseDatePhrase(s: string): string {
  const iso = RE_ISO.exec(s);
  if (iso) {
    const [, y, m, d] = iso;
    if (valid(+y!, +m!, +d!)) return `${y}-${m}-${d}`;
  }
  const md = RE_MONTH_DAY.exec(s);
  if (md) {
    const m = MONTHS[md[1]!.toLowerCase()];
    if (m && valid(+md[3]!, m, +md[2]!)) return `${md[3]}-${pad(m)}-${pad(+md[2]!)}`;
  }
  const dm = RE_DAY_MONTH.exec(s);
  if (dm) {
    const m = MONTHS[dm[2]!.toLowerCase()];
    if (m && valid(+dm[3]!, m, +dm[1]!)) return `${dm[3]}-${pad(m)}-${pad(+dm[1]!)}`;
  }
  return "";
}

export function parseMoney(s: string): number {
  const m = RE_MONEY.exec(s);
  if (!m) return 0;
  let v = parseFloat(m[1]!.replace(/,/g, ""));
  if (!isFinite(v)) return 0;
  switch ((m[2] ?? "").toLowerCase().trim()) {
    case "billion": case "b": v *= 1e9; break;
    case "million": case "m": v *= 1e6; break;
    case "k": v *= 1e3; break;
  }
  return v;
}

function firstMatch(text: string, cues: RegExp[], pick: (frag: string) => number | string,
  accept: (v: number | string) => boolean): number | string | undefined {
  for (const re of cues) {
    re.lastIndex = 0;
    const frags = text.match(re) ?? [];
    for (const frag of frags.slice(0, 4)) {
      const v = pick(frag);
      if (accept(v)) return v;
    }
  }
  return undefined;
}

function firstDate(text: string, cues: RegExp[]): string {
  return (firstMatch(text, cues, parseDatePhrase, (v) => !!v) as string) ?? "";
}

function firstMoneyOver(text: string, cues: RegExp[], floor: number): number {
  return (firstMatch(text, cues, parseMoney, (v) => (v as number) >= floor) as number) ?? 0;
}

function payoutRange(text: string): [number, number] {
  for (const re of PAYOUT_CUES) {
    re.lastIndex = 0;
    for (const frag of (text.match(re) ?? []).slice(0, 4)) {
      const all = [...frag.matchAll(new RegExp(RE_MONEY.source, "gi"))].slice(0, 2);
      if (all.length === 0) continue;
      const v = parseMoney(frag);
      if (v <= 0 || v > 1_000_000) continue; // a fund total caught by a payout cue
      if (all.length >= 2) {
        const second = parseMoney("$" + all[1]![1] + " " + (all[1]![2] ?? ""));
        if (second > 0 && second <= 1_000_000) return [v, second];
      }
      return [0, v]; // "up to $X" gives a ceiling and no floor
    }
  }
  return [0, 0];
}

function guessStage(text: string, s: Settlement): Stage {
  const low = text.toLowerCase();
  const has = (x: string) => low.includes(x);
  if (has("claims period has closed") || has("deadline has passed") || has("no longer accepting claims")) {
    return "claims-closed";
  }
  if (has("file a claim") || has("submit a claim") || has("claim form") || has("claims process is open")) {
    if (s.claim_deadline && Date.parse(s.claim_deadline) < Date.now() - 86_400_000) return "claims-closed";
    return "claims-open";
  }
  if (has("settlement has been approved") || has("settlement agreement") || has("approval hearing")) return "settled";
  if (has("has been certified") || has("certification order")) return "certified";
  if (has("statement of claim") || has("has been commenced") || has("was filed")) return "filed";
  return "unknown";
}

function mentionsProof(text: string): boolean {
  const low = text.toLowerCase();
  return ["proof of purchase", "supporting document", "receipts", "documentation is required",
    "must provide proof", "provide documentation", "medical records", "statutory declaration",
  ].some((p) => low.includes(p));
}

/**
 * Fill only the fields still empty. After a model extraction this recovers
 * deadlines the model left blank because they were in a sidebar; before one,
 * with an empty record, it does the whole job. It never overwrites: a regular
 * expression that finds "October 15" in a sentence about a court hearing has no
 * way of knowing it is not the claims deadline.
 */
export function backfill(s: Settlement, text: string): void {
  if (!s.claim_deadline) s.claim_deadline = firstDate(text, DEADLINE_CUES);
  if (!s.opt_out_deadline) s.opt_out_deadline = firstDate(text, OPT_OUT_CUES);
  if (!s.settlement_fund) s.settlement_fund = firstMoneyOver(text, FUND_CUES, 100_000);
  if (!s.payout_low && !s.payout_high) {
    const [lo, hi] = payoutRange(text);
    s.payout_low = lo;
    s.payout_high = hi;
  }
  if (!s.stage || s.stage === "unknown") s.stage = guessStage(text, s);
  if (s.effort_minutes <= 0) s.effort_minutes = s.proof_required ? 45 : 15;
}

/**
 * Build the whole record from text alone. This is what runs with no API key:
 * glean still tracks deadlines and stages, and simply has no class definition to
 * match against, so every settlement reports as unresolved rather than eligible.
 */
export function heuristic(s: Settlement, text: string): void {
  backfill(s, text);
  s.proof_required = mentionsProof(text);
  if (s.proof_required && s.effort_minutes < 45) s.effort_minutes = 45;
  s.extractor = "heuristic";
  s.confidence = 0.35;
}
