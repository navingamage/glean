/**
 * Whether a person belongs to a settlement class, and how much it is worth
 * finding out.
 *
 * The interesting output is not the yes/no. It is the list of questions that
 * would turn a maybe into a yes, ordered by how many dollars each answer
 * unlocks.
 */
import { Tri, True, False, Unknown, and, or, andAll, orAll, triString } from "./logic";
import {
  Criterion, CriterionKind, Profile, Settlement, Window,
  groups, daysLeft, expired, stageActionable, windowPhrase, windowString,
  windowsOverlap, windowZero,
} from "./model";
import { normalizeEntity, normalizeProvince, PROVINCES } from "./normalize";

/** Used when the extractor gave no estimate: middling on purpose. */
const DEFAULT_PREVALENCE = 0.3;

/**
 * Share of a settlement fund reaching claimants after fees and administration.
 * Used only when a notice states a fund but no per-claimant figure.
 */
const FEE_FRACTION = 0.6;

/**
 * P(in the class) for a settlement whose class definition could not be read at
 * all. A guess, and a low one: most classes are narrower than "anyone".
 */
const UNREAD_CLASS_PRIOR = 0.2;

/** At most this many of one settlement's questions reach the interview at once. */
export const PER_SETTLEMENT_CAP = 3;

export interface CriterionResult {
  criterion: Criterion;
  value: Tri;
  valueStr: string;
  reason: string;
}

export interface GroupResult {
  criteria: CriterionResult[];
  value: Tri;
  valueStr: string;
}

export interface Question {
  id: string;
  kind: CriterionKind;
  entity: string;
  label: string;
  window: Window;
  ask: string;
  unlocks: string[];
  worth: number;
}

export interface Assessment {
  settlementId: string;
  verdict: Tri;
  verdictStr: string;
  groups: GroupResult[];
  questions: Question[];
  note?: string;
  probability: number;
  payout: number;
  expectedValue: number;
  dollarsPerMinute: number;
  daysLeft: number;
  actionable: boolean;
}

function prevalenceOf(c: Criterion): number {
  return c.prevalence > 0 && c.prevalence <= 1 ? c.prevalence : DEFAULT_PREVALENCE;
}

function display(c: Criterion): string {
  return c.label || c.entity || c.text;
}

function findHolding(p: Profile, c: Criterion) {
  const keys = new Set([normalizeEntity(c.entity)]);
  for (const a of c.aliases ?? []) keys.add(normalizeEntity(a));
  for (const h of p.holdings) {
    if (h.kind !== c.kind && h.kind !== "other" && c.kind !== "other") continue;
    if (keys.has(normalizeEntity(h.entity))) return h;
  }
  return undefined;
}

/**
 * Residency is separate because "Canada" is a wildcard in one direction only. A
 * nationwide class covers a resident of any province, but a profile that says no
 * more than "Canada" cannot answer a class restricted to Saskatchewan.
 */
function residency(c: Criterion, p: Profile): [Tri, string] {
  let want = normalizeProvince(c.entity) || normalizeProvince(c.label);
  if (!want) return [Unknown, "could not read a province out of " + display(c)];

  let vague = false;
  for (const r of p.residences) {
    const code = r.province.toUpperCase();
    if (!windowsOverlap(r.window, c.window)) continue;
    if (code === want) return [True, `you lived in ${PROVINCES[want]}`];
    if (want === "CA") return [True, "you are resident in Canada"];
    if (code === "CA") vague = true;
  }
  if (vague) {
    return [Unknown, `your profile says Canada but this class is ${PROVINCES[want]} only`];
  }
  return [False, `you have not lived in ${PROVINCES[want]}`];
}

function evaluate(c: Criterion, p: Profile): [Tri, string] {
  if (c.kind === "residency") return residency(c, p);

  const h = findHolding(p, c);
  if (!h) return [Unknown, "no answer on file for " + display(c)];
  if (h.negative) return [False, `you said no to ${h.label}`];
  // A holding with no stated window is an assertion about the person, not about
  // a period, so it cannot contradict a class period.
  if (windowZero(h.window) || windowZero(c.window)) {
    return [True, `${h.label} is on your profile`];
  }
  if (windowsOverlap(h.window, c.window)) {
    return [True, `${h.label}, ${windowString(h.window)}, overlaps the class period ${windowString(c.window)}`];
  }
  return [False, `${h.label}, ${windowString(h.window)}, falls outside the class period ${windowString(c.window)}`];
}

function questionFor(c: Criterion): Question {
  const label = display(c);
  const phrase = windowPhrase(c.window);
  const period = phrase ? " " + phrase : "";
  const asks: Record<CriterionKind, string> = {
    residency: `Did you live in ${label}${period}?`,
    purchase: `Did you buy ${label}${period}?`,
    account: `Did you hold an account with ${label}${period}?`,
    service: `Did you use ${label}${period}?`,
    employment: `Did you work for ${label}${period}?`,
    vehicle: `Did you own or lease a ${label}${period}?`,
    device: `Did you own a ${label}${period}?`,
    medical: `Were you prescribed or treated with ${label}${period}?`,
    other: `Does this apply to you: ${label}${period}?`,
  };
  return {
    id: `${c.kind}:${normalizeEntity(c.entity)}`,
    kind: c.kind,
    entity: normalizeEntity(c.entity),
    label,
    window: c.window,
    ask: asks[c.kind],
    unlocks: [],
    worth: 0,
  };
}

/**
 * What one claimant can expect, in Canadian dollars. The order of preference is
 * the order of trustworthiness. Zero means unknown, which the ranking treats as
 * unranked rather than worthless.
 */
export function estimatePayout(s: Settlement): number {
  if (s.payout_low > 0 && s.payout_high > 0) return (s.payout_low + s.payout_high) / 2;
  if (s.payout_high > 0) return s.payout_high / 2;
  if (s.payout_low > 0) return s.payout_low;
  if (s.settlement_fund > 0 && s.estimated_class_size > 0) {
    return (s.settlement_fund * FEE_FRACTION) / s.estimated_class_size;
  }
  return 0;
}

export function assess(s: Settlement, p: Profile): Assessment {
  const base = {
    settlementId: s.id,
    daysLeft: daysLeft(s),
    actionable: stageActionable(s.stage) && !expired(s),
  };

  // A settlement with no criteria is one whose class definition was never
  // extracted. The empty conjunction is True as a matter of logic, and reporting
  // that as "you are in the class" would be a lie: the honest reading of "we
  // could not find out who is in this class" is that we do not know.
  if (!s.criteria || s.criteria.length === 0) {
    const payout = estimatePayout(s);
    const effort = s.effort_minutes > 0 ? s.effort_minutes : 15;
    return {
      ...base,
      verdict: Unknown,
      verdictStr: triString(Unknown),
      groups: [],
      questions: [],
      note: "the class definition on this page could not be read, so eligibility is unknown",
      probability: UNREAD_CLASS_PRIOR,
      payout,
      expectedValue: payout * UNREAD_CLASS_PRIOR,
      dollarsPerMinute: (payout * UNREAD_CLASS_PRIOR) / effort,
    };
  }

  const groupValues: Tri[] = [];
  const questions: Question[] = [];
  const seen = new Set<string>();
  const results: GroupResult[] = [];
  let prob = 1;

  for (const group of groups(s)) {
    const criteria: CriterionResult[] = [];
    const vals: Tri[] = [];
    let missChance = 1;
    const pending: Question[] = [];

    for (const c of group) {
      const [v, reason] = evaluate(c, p);
      criteria.push({ criterion: c, value: v, valueStr: triString(v), reason });
      vals.push(v);
      if (v === Unknown) {
        missChance *= 1 - prevalenceOf(c);
        pending.push(questionFor(c));
      }
    }

    const value = orAll(vals);
    results.push({ criteria, value, valueStr: triString(value) });
    groupValues.push(value);

    // Questions are held back until the group's value is known, because one
    // satisfied alternative settles the whole group. Asking "did you also shop
    // at Sobeys?" of someone who already said Loblaws cannot change anything.
    if (value === Unknown) {
      prob *= 1 - missChance;
      for (const q of pending) {
        if (!seen.has(q.id)) {
          seen.add(q.id);
          questions.push(q);
        }
      }
    }
    if (value === False) prob = 0;
  }

  const verdict = andAll(groupValues);
  if (verdict === False) {
    prob = 0;
    questions.length = 0; // no point asking about a settlement already ruled out
  }
  const probability = Math.min(1, Math.max(0, prob));
  const payout = estimatePayout(s);
  const expectedValue = payout * probability;
  const effort = s.effort_minutes > 0 ? s.effort_minutes : 15;

  for (const q of questions) {
    q.unlocks = [s.id];
    // The probability-weighted value, not the headline payout. Using the payout
    // would price a question about a rare drug class at its full $17,500 and put
    // it above one about a grocery chain half the country shops at, and would
    // let a summary add ninety-six questions into half a million dollars nobody
    // is owed.
    q.worth = expectedValue;
  }

  return {
    ...base,
    verdict,
    verdictStr: triString(verdict),
    groups: results,
    questions,
    probability,
    payout,
    expectedValue,
    dollarsPerMinute: expectedValue / effort,
  };
}

function score(a: Assessment, s: Settlement | undefined): number {
  if (!s || expired(s) || a.verdict === False) return -1;
  let v = a.expectedValue;
  if (v === 0) {
    // Most notices state no per-claimant figure. One constant for all of them
    // would sort a class you are almost certainly in level with one you are
    // almost certainly not, discarding the only thing known about them. The
    // proxy never reaches payout or expectedValue.
    const UNPRICED_PROXY = 20;
    v = 0.01 + a.probability * UNPRICED_PROXY;
  }
  if (!a.actionable) v *= 0.25;
  const d = a.daysLeft;
  if (d <= 7) v *= 2.5;
  else if (d <= 30) v *= 1.6;
  else if (d <= 90) v *= 1.15;
  return v;
}

/**
 * Order for a person deciding what to do this evening. Urgency is a multiplier
 * on value rather than a separate sort key, because a forty dollar claim closing
 * on Friday is the one at risk, not the two hundred dollar one closing in spring.
 */
export function rank(as: Assessment[], byId: Map<string, Settlement>): void {
  as.sort((x, y) => {
    const sx = score(x, byId.get(x.settlementId));
    const sy = score(y, byId.get(y.settlementId));
    if (sx !== sy) return sy - sx;
    return x.daysLeft - y.daysLeft;
  });
}

/**
 * Open questions across many assessments, merged by id. The result is the
 * shortest interview that turns the largest number of maybes into claims.
 */
export function questions(as: Assessment[]): Question[] {
  const merged = new Map<string, Question>();
  for (const a of as) {
    if (a.verdict === False) continue;
    for (const q of a.questions) {
      const m = merged.get(q.id);
      if (!m) {
        merged.set(q.id, { ...q, unlocks: [...q.unlocks] });
        continue;
      }
      m.unlocks.push(...q.unlocks);
      m.worth += q.worth;
      if (!m.window.from || (q.window.from && q.window.from < m.window.from)) {
        m.window = { ...m.window, from: q.window.from };
      }
      if (!m.window.until || (q.window.until && q.window.until > m.window.until)) {
        m.window = { ...m.window, until: q.window.until };
      }
    }
  }
  const out = [...merged.values()].sort((a, b) => {
    if (a.worth !== b.worth) return b.worth - a.worth;
    // Plenty of notices state no payout, leaving a whole tier worth nothing on
    // paper. Among those, the one blocking the most settlements comes first.
    if (a.unlocks.length !== b.unlocks.length) return b.unlocks.length - a.unlocks.length;
    return a.id < b.id ? -1 : 1;
  });
  return capPerSettlement(out);
}

/**
 * An alternative group with nine branches produces nine questions, all worth the
 * same, all about one case. The major junior hockey class does exactly this, one
 * per league per province. Left alone they fill the entire queue and bury every
 * other settlement behind a case the reader has probably never been near.
 */
function capPerSettlement(qs: Question[]): Question[] {
  const seen = new Map<string, number>();
  const out: Question[] = [];
  for (const q of qs) {
    if (q.unlocks.some((id) => (seen.get(id) ?? 0) >= PER_SETTLEMENT_CAP)) continue;
    for (const id of q.unlocks) seen.set(id, (seen.get(id) ?? 0) + 1);
    out.push(q);
  }
  return out;
}

/**
 * Expected value behind unanswered questions, counted once per settlement.
 * Summing question worth would double count, because one settlement with three
 * open criteria contributes three questions each carrying its value.
 */
export function totalAtStake(qs: Question[], as: Assessment[]): number {
  const value = new Map<string, number>();
  for (const a of as) value.set(a.settlementId, a.expectedValue);
  const counted = new Set<string>();
  let sum = 0;
  for (const q of qs) {
    for (const id of q.unlocks) {
      if (!counted.has(id)) {
        counted.add(id);
        sum += value.get(id) ?? 0;
      }
    }
  }
  return sum;
}
