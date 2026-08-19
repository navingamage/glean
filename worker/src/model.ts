/** Domain types, matching the Go implementation's JSON shape byte for byte. */

/**
 * A calendar date with no clock and no zone, as "YYYY-MM-DD". The empty string
 * is "unbounded" rather than "year zero": class periods are frequently open at
 * one end, and a deadline that has not been announced is not one in the past.
 */
export type DateStr = string;

export interface Window {
  from: DateStr;
  until: DateStr;
}

export const EMPTY_WINDOW: Window = { from: "", until: "" };

export function today(): DateStr {
  return new Date().toISOString().slice(0, 10);
}

export function isZeroDate(d: DateStr): boolean {
  return !d;
}

/** Days from today to d. Negative means it has passed. */
export function daysUntil(d: DateStr): number {
  if (!d) return 0;
  const ms = Date.parse(d + "T00:00:00Z") - Date.parse(today() + "T00:00:00Z");
  return Math.round(ms / 86_400_000);
}

export function windowZero(w: Window): boolean {
  return !w.from && !w.until;
}

export function windowString(w: Window): string {
  if (windowZero(w)) return "any time";
  if (!w.from) return "up to " + w.until;
  if (!w.until) return w.from + " onward";
  return w.from + " to " + w.until;
}

/**
 * A clause that can be dropped into a question. windowString is for tables;
 * gluing it onto "between" produces "between up to 2012-03-12", which is the
 * kind of thing that makes a reader distrust everything else on the screen.
 */
export function windowPhrase(w: Window): string {
  if (windowZero(w)) return "";
  if (!w.from) return "on or before " + w.until;
  if (!w.until) return "at any time since " + w.from;
  return "between " + w.from + " and " + w.until;
}

/**
 * Whether two windows share at least one day. This is the operation eligibility
 * needs: a CIBC account held 2015 to 2020 against a class period of 2018 to 2022
 * overlaps, even though neither contains the other.
 */
export function windowsOverlap(a: Window, b: Window): boolean {
  if (a.until && b.from && a.until < b.from) return false;
  if (b.until && a.from && b.until < a.from) return false;
  return true;
}

export type Stage =
  | "filed" | "certified" | "settled"
  | "claims-open" | "claims-closed" | "concluded" | "unknown";

/** Only some stages are worth anything. claims-open is money on a table. */
export function stageActionable(s: Stage): boolean {
  return s === "claims-open" || s === "settled";
}

export type CriterionKind =
  | "residency" | "purchase" | "account" | "service"
  | "employment" | "vehicle" | "device" | "medical" | "other";

const KINDS: CriterionKind[] = [
  "residency", "purchase", "account", "service",
  "employment", "vehicle", "device", "medical", "other",
];

export function validKind(k: string): k is CriterionKind {
  return (KINDS as string[]).includes(k);
}

export interface Criterion {
  kind: CriterionKind;
  entity: string;
  label: string;
  aliases?: string[];
  window: Window;
  text: string;
  /**
   * Criteria sharing a group are alternatives, OR'd together. Without it the
   * matcher demands you shopped at every banner in the class definition at once.
   */
  group?: string;
  /** Share of Canadian adults satisfying this. Ranking only, never the verdict. */
  prevalence: number;
}

export interface Settlement {
  id: string;
  title: string;
  case_name?: string;
  defendants?: string[];
  court?: string;
  jurisdiction?: string[];
  stage: Stage;
  summary: string;
  class_text?: string;
  criteria?: Criterion[];
  claims_open_on: DateStr;
  claim_deadline: DateStr;
  opt_out_deadline: DateStr;
  settlement_fund: number;
  payout_low: number;
  payout_high: number;
  payout_note?: string;
  estimated_class_size: number;
  proof_required: boolean;
  proof_note?: string;
  effort_minutes: number;
  url: string;
  claim_url?: string;
  administrator?: string;
  source_id: string;
  extractor: string;
  confidence: number;
  first_seen: DateStr;
  last_seen: DateStr;
  content_hash?: string;
}

/** Days until the deadline; a huge number when unknown, so those sort last. */
export function daysLeft(s: Settlement): number {
  if (!s.claim_deadline) return 1 << 30;
  return daysUntil(s.claim_deadline);
}

export function expired(s: Settlement): boolean {
  if (s.stage === "claims-closed" || s.stage === "concluded") return true;
  return !!s.claim_deadline && daysUntil(s.claim_deadline) < 0;
}

/**
 * Criteria partitioned into alternative groups. An empty group name means the
 * clause is a requirement in its own right.
 */
export function groups(s: Settlement): Criterion[][] {
  const out: Criterion[][] = [];
  const index = new Map<string, number>();
  for (const c of s.criteria ?? []) {
    if (!c.group) {
      out.push([c]);
      continue;
    }
    const at = index.get(c.group);
    if (at !== undefined) {
      out[at]!.push(c);
      continue;
    }
    index.set(c.group, out.length);
    out.push([c]);
  }
  return out;
}

export interface Residence {
  province: string;
  window: Window;
}

export interface Holding {
  kind: CriterionKind;
  entity: string;
  label: string;
  window: Window;
  negative?: boolean;
  note?: string;
  added_on: DateStr;
}

export interface Profile {
  name?: string;
  email?: string;
  residences: Residence[];
  holdings: Holding[];
  min_payout: number;
  max_effort_minutes: number;
  updated_on: DateStr;
}

export function defaultProfile(): Profile {
  return {
    residences: [{ province: "CA", window: { ...EMPTY_WINDOW } }],
    holdings: [],
    min_payout: 5,
    max_effort_minutes: 60,
    updated_on: today(),
  };
}

export type ClaimState =
  | "new" | "interested" | "filed" | "paid" | "rejected" | "skipped";

export interface Claim {
  settlement_id: string;
  state: ClaimState;
  filed_on: DateStr;
  paid_on: DateStr;
  amount: number;
  reference?: string;
  note?: string;
  updated_on: DateStr;
}
