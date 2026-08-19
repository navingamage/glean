/**
 * One pass over every source: discover, fetch, extract, store.
 *
 * The ordering of the cheap checks matters more than anything else here. A scan
 * looks at a few hundred pages, and almost none have changed since yesterday.
 * Conditional GET removes most of the bytes, the text hash removes most of the
 * parsing, and a relevance score removes most of what would otherwise be a model
 * call on a partner biography. What is left is the handful that said something.
 */
import { Change, Store } from "./db";
import { Extractor, NotClassAction, Tier, stakes, underspecified } from "./extract/openrouter";
import { backfill, heuristic, relevance } from "./extract/heuristic";
import { findPortal } from "./extract/portal";
import { Disallowed, Fetcher, isHTML } from "./fetcher";
import { cleanTitle, parse } from "./html";
import { Settlement } from "./model";
import { newID, sha256Hex } from "./normalize";
import { Candidate, Source, authority, candidatesFrom } from "./sources";

export interface ScanOptions {
  perSource: number;
  minRelevance: number;
  refetch: boolean;
  /**
   * Wall-clock budget. A Worker invocation is not unbounded, and a scan that is
   * killed halfway has still done real work: everything extracted before the
   * cutoff is committed, and the next run picks up the pages it did not reach
   * because their hashes are still missing.
   */
  deadlineMs: number;
}

export function defaultOptions(): ScanOptions {
  return { perSource: 40, minRelevance: 8, refetch: false, deadlineMs: 4 * 60 * 1000 };
}

export interface ScanResult {
  candidates: number;
  fetched: number;
  unchanged: number;
  irrelevant: number;
  extracted: number;
  prime: number;
  escalated: number;
  newIds: string[];
  updated: Record<string, Change[]>;
  errors: string[];
  ranOut: boolean;
}

export function summarize(r: ScanResult): string {
  return `${r.candidates} candidates, ${r.fetched} fetched, ${r.unchanged} unchanged, ` +
    `${r.irrelevant} not relevant, ${r.extracted} extracted (${r.prime} prime, ` +
    `${r.escalated} escalated), ${r.newIds.length} new, ` +
    `${Object.keys(r.updated).length} updated, ${r.errors.length} errors` +
    (r.ranOut ? ", stopped on the time budget" : "");
}

export async function runScan(
  store: Store,
  sources: Source[],
  extractor: Extractor | undefined,
  opts: ScanOptions,
): Promise<ScanResult> {
  const started = Date.now();
  const fetcher = new Fetcher(store, opts.refetch);
  const res: ScanResult = {
    candidates: 0, fetched: 0, unchanged: 0, irrelevant: 0, extracted: 0,
    prime: 0, escalated: 0, newIds: [], updated: {}, errors: [], ranOut: false,
  };
  const outOfTime = () => Date.now() - started > opts.deadlineMs;

  for (const src of sources) {
    if (outOfTime()) { res.ranOut = true; break; }

    const candidates: Candidate[] = [];
    for (const listing of src.listing) {
      try {
        const page = await fetcher.get(listing);
        if (!isHTML(page) || !page.body) continue;
        const parsed = await parse(page.body, listing);
        candidates.push(...candidatesFrom(src, parsed.links, opts.perSource));
      } catch (e) {
        if (!(e instanceof Disallowed)) res.errors.push(`${src.id}: ${e}`);
      }
    }
    res.candidates += candidates.length;

    for (const cand of candidates.slice(0, opts.perSource)) {
      if (outOfTime()) { res.ranOut = true; break; }
      try {
        await visit(store, fetcher, extractor, src, cand, opts, res);
      } catch (e) {
        if (!(e instanceof Disallowed)) res.errors.push(`${src.id}: ${e}`);
      }
    }
  }
  return res;
}

async function visit(
  store: Store,
  fetcher: Fetcher,
  extractor: Extractor | undefined,
  src: Source,
  cand: Candidate,
  opts: ScanOptions,
  res: ScanResult,
): Promise<void> {
  const page = await fetcher.get(cand.url);
  res.fetched++;
  if (!isHTML(page) || !page.body) return;

  const parsed = await parse(page.body, cand.url);

  // Change detection runs on the readable text, not the bytes. Sites emit a
  // rotating CSRF token, session id or ad slot on every response, so a byte hash
  // reports that a notice changed overnight when not one word of it did.
  // Stripping the markup is free; the model call is all of the money, so the
  // gate belongs after the text is in hand and before the model is asked.
  const textHash = await sha256Hex(parsed.text);
  const id = await newID(cand.url);
  const known = !!(await store.settlement(id));
  if (known && !opts.refetch && (await store.seenHash(id)) === textHash) {
    res.unchanged++;
    return;
  }

  if (relevance(parsed.text) < opts.minRelevance) {
    res.irrelevant++;
    return;
  }

  const title = cleanTitle(parsed.title || cand.title, cand.url);
  let s: Settlement | undefined;
  let tier: Tier = "cheap";

  if (extractor) {
    tier = stakes(parsed.text, authority(src.kind) >= 3);
    try {
      s = await extractor.extract(tier, cand.url, title, parsed.text);
      // Escalate when the cheap answer is visibly inadequate: a settlement open
      // for claims with no class definition is one the matcher can say nothing
      // useful about, which is the whole job.
      if (tier === "cheap" && underspecified(s)) {
        try {
          s = await extractor.extract("prime", cand.url, title, parsed.text);
          tier = "prime";
          res.escalated++;
        } catch { /* the cheap record stands */ }
      }
    } catch (e) {
      if (e instanceof NotClassAction) {
        res.irrelevant++;
        return;
      }
      // Fall through to the heuristic rather than losing the page: a model
      // outage should degrade the record, not drop the settlement.
      res.errors.push(`${src.id}: ${e}`);
    }
  }

  if (!s) {
    s = blankSettlement(id, title, cand.url);
    heuristic(s, parsed.text);
  } else {
    backfill(s, parsed.text);
  }
  if (!s.title) s.title = title;

  // Follow the claim portal when the case page did not answer the question the
  // user is actually asking. One hop, only off-site, only when something is
  // still missing.
  const portal = portalFor(s, parsed.links, cand.url);
  if (portal && await followPortal(fetcher, extractor, s, portal)) {
    s.claim_url = portal;
  }

  s.source_id = src.id;
  s.content_hash = textHash;
  if (!s.jurisdiction || s.jurisdiction.length === 0) s.jurisdiction = src.jurisdiction;

  res.extracted++;
  if (tier === "prime") res.prime++;

  const { isNew, changes } = await store.upsert(s);
  // Recorded whether or not the extraction changed anything: "we read this page
  // and it said the same as last time" is the fact that makes the next scan free.
  await store.recordHash(s.id, textHash);
  if (isNew) res.newIds.push(s.id);
  if (changes.length > 0) res.updated[s.id] = changes;
}

function portalFor(s: Settlement, links: { url: string; text: string }[], pageURL: string): string {
  if (s.claim_deadline && s.payout_high > 0) return ""; // nothing left worth a request
  if (s.claim_url) return s.claim_url;
  return findPortal(links, pageURL);
}

async function followPortal(
  fetcher: Fetcher,
  extractor: Extractor | undefined,
  s: Settlement,
  portal: string,
): Promise<boolean> {
  let page;
  try {
    page = await fetcher.get(portal);
  } catch {
    return false;
  }
  if (!isHTML(page) || !page.body) return false;
  const parsed = await parse(page.body, portal);
  if (relevance(parsed.text) < 6) return false;

  // A claim portal exists to state one deadline and one set of amounts. That is
  // transcription, so it goes to the cheap tier: the class definition has
  // already been read from the case page, and re-reading it here would be paying
  // twice for the same paragraph.
  if (extractor) {
    try {
      const p = await extractor.extract("cheap", portal, s.title, parsed.text);
      mergePortal(s, p);
    } catch { /* the case page's record stands */ }
  }
  backfill(s, parsed.text);
  return true;
}

/**
 * Take from an administrator's page only the facts it is the authority on. Its
 * title, summary and class definition are not imported: a portal page often
 * covers several settlements, and letting it overwrite the case description
 * would replace something specific with something generic.
 */
function mergePortal(s: Settlement, p: Settlement): void {
  if (!s.claim_deadline && p.claim_deadline) s.claim_deadline = p.claim_deadline;
  if (!s.opt_out_deadline && p.opt_out_deadline) s.opt_out_deadline = p.opt_out_deadline;
  if (!s.claims_open_on && p.claims_open_on) s.claims_open_on = p.claims_open_on;
  if (!s.payout_low && !s.payout_high) {
    s.payout_low = p.payout_low;
    s.payout_high = p.payout_high;
    s.payout_note = p.payout_note;
  }
  if (!s.settlement_fund) s.settlement_fund = p.settlement_fund;
  if (!s.administrator) s.administrator = p.administrator;
  if (!s.proof_required && p.proof_required) {
    s.proof_required = true;
    s.proof_note = p.proof_note;
  }
  // The administrator is the authority on whether claims are open.
  if (p.stage === "claims-open" || p.stage === "settled" || p.stage === "claims-closed") {
    s.stage = p.stage;
  }
}

function blankSettlement(id: string, title: string, url: string): Settlement {
  return {
    id, title, stage: "unknown", summary: "",
    claims_open_on: "", claim_deadline: "", opt_out_deadline: "",
    settlement_fund: 0, payout_low: 0, payout_high: 0, estimated_class_size: 0,
    proof_required: false, effort_minutes: 0, url,
    source_id: "", extractor: "", confidence: 0, first_seen: "", last_seen: "",
  };
}
