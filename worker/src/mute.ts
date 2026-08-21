/**
 * Subjects the reader has asked not to be shown.
 *
 * The queue is only useful if it is short, and a registry scanned for everybody
 * carries whole categories one person will never claim from. The major junior
 * hockey class is the standing example: it decomposes into a criterion per
 * league per province, so it produces more questions than any other proceeding
 * in the registry, and somebody who has never played hockey has to answer all
 * of them to make them go away.
 *
 * So a term hides a settlement outright. It is matched against everything the
 * reader could recognise the case by, which includes the class criteria: the
 * word "hockey" appears in the criteria of that case more reliably than in its
 * administrative title.
 *
 * Matching is on whole words, because a substring test makes "gm" hide anything
 * mentioning management and there is no way for the reader to see why. A term
 * of several words matches that phrase, with any run of whitespace between.
 */
import { Profile, Settlement } from "./model";

/** The text a term is matched against: everything the reader would recognise. */
export function haystack(s: Settlement): string {
  const parts: string[] = [
    s.title, s.summary ?? "", s.case_name ?? "", s.class_text ?? "",
    ...(s.defendants ?? []),
    ...(s.criteria ?? []).flatMap((c) => [c.label, c.entity, c.text, ...(c.aliases ?? [])]),
  ];
  return parts.filter(Boolean).join(" \n ").toLowerCase();
}

/** Trimmed and collapsed, which is the form both stored and compared. */
export function normalizeTerm(term: string): string {
  return term.trim().replace(/\s+/g, " ").toLowerCase();
}

function pattern(term: string): RegExp | undefined {
  const t = normalizeTerm(term);
  if (!t) return undefined;
  const escaped = t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/ /g, "\\s+");
  // \b only does what it looks like it does next to a word character. A term
  // ending in punctuation gets a bare match rather than a boundary that can
  // never hold.
  const left = /^\w/.test(t) ? "\\b" : "";
  const right = /\w$/.test(t) ? "\\b" : "";
  try {
    return new RegExp(left + escaped + right, "i");
  } catch {
    return undefined;
  }
}

export function matchesTerm(s: Settlement, term: string): boolean {
  const re = pattern(term);
  return re ? re.test(haystack(s)) : false;
}

/**
 * The first term hiding this settlement, or "". Returning the term rather than
 * a boolean is what lets the page say which word did it, so a rule that hides
 * more than it was meant to can be found and removed.
 */
export function mutedBy(s: Settlement, p: Profile): string {
  const text = haystack(s);
  for (const m of p.mutes ?? []) {
    const re = pattern(m.term);
    if (re && re.test(text)) return m.term;
  }
  return "";
}

/** Adds a term, keeping the list unique and in the order it was built. */
export function addMute(p: Profile, term: string): boolean {
  const t = normalizeTerm(term);
  if (!t) return false;
  const mutes = p.mutes ?? (p.mutes = []);
  if (mutes.some((m) => normalizeTerm(m.term) === t)) return false;
  mutes.push({ term: t, added_on: new Date().toISOString().slice(0, 10) });
  return true;
}

export function removeMute(p: Profile, term: string): boolean {
  const t = normalizeTerm(term);
  const mutes = p.mutes ?? [];
  const at = mutes.findIndex((m) => normalizeTerm(m.term) === t);
  if (at < 0) return false;
  mutes.splice(at, 1);
  p.mutes = mutes;
  return true;
}

/**
 * A term worth offering when somebody hides one case, taken from its title.
 * Stop words and the vocabulary every notice shares are dropped, because
 * "settlement" would hide the entire registry on one click.
 */
const NOISE = new Set([
  "class", "action", "actions", "settlement", "settlements", "claim", "claims",
  "canada", "canadian", "national", "notice", "proposed", "the", "and", "for",
  "inc", "ltd", "llc", "corp", "corporation", "company", "co", "of", "in", "on",
  "de", "du", "la", "le", "des", "case", "lawsuit", "litigation", "fund",
  "price", "fixing", "price-fixing", "consumer", "product", "products",
]);

export function suggestTerm(s: Settlement): string {
  for (const word of s.title.toLowerCase().split(/[^a-z0-9'-]+/)) {
    if (word.length > 3 && !NOISE.has(word)) return word;
  }
  return "";
}
