/**
 * Where settlements come from, and turning a listing page into a work list.
 *
 * The registry is data, not code, and deliberately loose. Every site in it will
 * be redesigned, and a scraper pinned to CSS selectors breaks silently the day
 * that happens: it keeps returning zero results and nobody notices until a
 * deadline has passed. A source says only where to start and roughly what a case
 * URL looks like; everything after that is scoring on the page's own words. A
 * redesign costs recall, not correctness.
 */
import registry from "./sources.json";
import { canonicalURL, identityURL } from "./normalize";
import { Link } from "./html";

export type Kind = "aggregator" | "firm" | "administrator" | "registry";

export interface Source {
  id: string;
  name: string;
  kind: Kind;
  jurisdiction: string[];
  listing: string[];
  follow: string[];
  exclude?: string[];
  disabled?: boolean;
  notes?: string;
}

/**
 * How much weight a conflicting field from this source carries. An administrator
 * publishes the deadline it will actually enforce; an aggregator is repeating
 * what it read somewhere else.
 */
export function authority(k: Kind): number {
  switch (k) {
    case "administrator": return 4;
    case "registry": return 3;
    case "firm": return 2;
    default: return 1;
  }
}

export function loadSources(): Source[] {
  return (registry as Source[]).map((s) => ({ ...s, kind: s.kind ?? "aggregator" }));
}

export function enabledSources(all: Source[], only: string[] = []): Source[] {
  const want = new Set(only);
  return all.filter((s) => !s.disabled && (want.size === 0 || want.has(s.id)));
}

export function authorityMap(all: Source[]): Map<string, number> {
  return new Map(all.map((s) => [s.id, authority(s.kind)]));
}

/** Whether a discovered URL is inside a source's territory. */
export function wants(s: Source, rawURL: string): boolean {
  const url = identityURL(rawURL);
  for (const p of s.exclude ?? []) {
    if (new RegExp(p).test(url)) return false;
  }
  if (!s.follow || s.follow.length === 0) return false;
  return s.follow.some((p) => new RegExp(p).test(url));
}

export interface Candidate {
  url: string;
  title: string;
  sourceId: string;
  score: number;
}

/**
 * Words suggesting a case page rather than a partner bio. Weights only order the
 * queue; nothing is rejected on this basis alone, because a source whose follow
 * pattern already matched has earned the benefit of the doubt.
 */
const SIGNALS: Record<string, number> = {
  "class action": 6, "action collective": 6, settlement: 6, settlements: 6,
  claim: 5, claims: 5, reclamation: 4, deadline: 4, notice: 3, avis: 2,
  "opt out": 3, certification: 3, certified: 3, lawsuit: 3, compensation: 3,
  eligible: 3, eligibility: 3, proposed: 1, court: 1, case: 1, reglement: 4,
};

/** Links that are website structure rather than content. Subtracted, not excluded. */
const NOISE: Record<string, number> = {
  privacy: 5, terms: 5, cookie: 5, careers: 4, contact: 3,
  subscribe: 3, newsletter: 3, login: 4, "sign in": 4, author: 3,
  category: 2, tag: 2, search: 3, feed: 3, "about us": 3,
};

export function scoreLink(url: string, text: string): number {
  // URL path segments are hyphenated; normalizing them to spaces lets one
  // keyword table serve both the href and the visible text.
  const hay = (url + " " + text).toLowerCase().replace(/[-_/]+/g, " ");
  let score = 0;
  for (const [word, weight] of Object.entries(SIGNALS)) {
    if (hay.includes(word)) score += weight;
  }
  for (const [word, weight] of Object.entries(NOISE)) {
    if (hay.includes(word)) score -= weight;
  }
  if (text.trim().split(/\s+/).length >= 4) score += 2;
  return score;
}

/** Case pages a listing points at, best first. */
export function candidatesFrom(s: Source, links: Link[], limit: number): Candidate[] {
  const best = new Map<string, Candidate>();
  for (const link of links) {
    const url = canonicalURL(link.url);
    if (!wants(s, url)) continue;
    const cand: Candidate = {
      url, title: link.text, sourceId: s.id, score: scoreLink(url, link.text),
    };
    // The same case is often linked twice on one page, once from a thumbnail
    // with no text and once from a headline. Keep the version that says more.
    const key = identityURL(url);
    const prev = best.get(key);
    if (!prev || cand.score > prev.score) best.set(key, cand);
  }
  const out = [...best.values()].sort((a, b) =>
    b.score - a.score || (a.url < b.url ? -1 : 1));
  return limit > 0 ? out.slice(0, limit) : out;
}
