/**
 * Collapse the same settlement found in more than one place.
 *
 * A settlement is identified by the URL it was read from, which is right for
 * tracking one page over time and wrong the moment two sites describe one case.
 * Merging is deliberately conservative: two settlements wrongly merged hide one
 * of them completely, which is the failure that costs money, whereas two left
 * separate are only untidy.
 */
import { Settlement } from "./model";

/** Thresholds on title similarity, by how much corroboration comes with it. */
const WITH_PORTAL = 0.35;
const WITH_DEADLINE = 0.45;
const TITLE_ALONE = 0.70;

export interface Group {
  primary: Settlement;
  also: Settlement[];
}

const STOP = new Set([
  "the", "and", "of", "in", "for", "to", "class", "action", "actions",
  "settlement", "settlements", "lawsuit", "canada", "canadian", "claim",
  "claims", "proposed", "national", "proceeding", "recours", "collectif",
]);

function words(title: string): Set<string> {
  const out = new Set<string>();
  for (const w of title.toLowerCase().split(/[^a-z0-9]+/)) {
    if (w.length >= 3 && !STOP.has(w)) out.add(w);
  }
  return out;
}

function trigrams(title: string): Set<string> {
  const joined = title.toLowerCase().split(/[^a-z0-9]+/)
    .filter((w) => w && !STOP.has(w)).join("");
  const out = new Set<string>();
  for (let i = 0; i + 3 <= joined.length; i++) out.add(joined.slice(i, i + 3));
  return out;
}

function jaccard(a: Set<string>, b: Set<string>): number {
  if (a.size === 0 || b.size === 0) return 0;
  let shared = 0;
  for (const k of a) if (b.has(k)) shared++;
  const union = a.size + b.size - shared;
  return union === 0 ? 0 : shared / union;
}

function portalHost(s: Settlement): string {
  if (!s.claim_url) return "";
  try {
    return new URL(s.claim_url).hostname.toLowerCase().replace(/^www\./, "");
  } catch {
    return "";
  }
}

interface Rec {
  s: Settlement;
  words: Set<string>;
  grams: Set<string>;
  portal: string;
}

function same(a: Rec, b: Rec): boolean {
  // A stated disagreement is proof of difference, checked before any similarity
  // score. The Quebec and Pan-Canadian tobacco plans share most of their words
  // and are two different proceedings with two different deadlines.
  const samePortal = !!a.portal && a.portal === b.portal;
  if (a.portal && b.portal && !samePortal) return false;

  const da = a.s.claim_deadline, db = b.s.claim_deadline;
  const sameDeadline = !!da && da === db;
  if (da && db && !sameDeadline) return false;

  const need = samePortal ? WITH_PORTAL : sameDeadline ? WITH_DEADLINE : TITLE_ALONE;

  // Titles are compared two ways because sites disagree about spacing: "Big
  // Steel Box" and "BigSteelBox" share no words and nearly all their trigrams.
  const score = Math.max(jaccard(a.words, b.words), jaccard(a.grams, b.grams));
  return score >= need;
}

/** How much a record knows, used to break authority ties. */
function filled(s: Settlement): number {
  let n = s.criteria?.length ?? 0;
  for (const known of [
    !!s.claim_deadline, !!s.claim_url, s.payout_high > 0,
    s.settlement_fund > 0, !!s.class_text, !!s.administrator,
  ]) {
    if (known) n++;
  }
  return n;
}

/**
 * Group a list, preserving the order of the primaries so a ranked list stays
 * ranked. `authority` maps a source id to how much its version of a conflicting
 * field is worth; an administrator publishes the deadline that will actually be
 * enforced.
 */
export function collapse(settlements: Settlement[], authority: Map<string, number>): Group[] {
  const recs: Rec[] = settlements.map((s) => ({
    s, words: words(s.title), grams: trigrams(s.title), portal: portalHost(s),
  }));

  const taken = new Array(recs.length).fill(false);
  const groups: Group[] = [];
  for (let i = 0; i < recs.length; i++) {
    if (taken[i]) continue;
    taken[i] = true;
    const g: Group = { primary: recs[i]!.s, also: [] };
    for (let j = i + 1; j < recs.length; j++) {
      if (!taken[j] && same(recs[i]!, recs[j]!)) {
        taken[j] = true;
        g.also.push(recs[j]!.s);
      }
    }
    groups.push(g);
  }

  for (const g of groups) {
    if (g.also.length === 0) continue;
    const all = [g.primary, ...g.also].sort((a, b) => {
      const aa = authority.get(a.source_id) ?? 1, ab = authority.get(b.source_id) ?? 1;
      if (aa !== ab) return ab - aa;
      const fa = filled(a), fb = filled(b);
      if (fa !== fb) return fb - fa;
      return b.confidence - a.confidence;
    });
    g.primary = all[0]!;
    g.also = all.slice(1);
  }
  return groups;
}
