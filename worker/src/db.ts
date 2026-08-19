/**
 * State in D1.
 *
 * The event log is history: what was discovered, what moved, what was answered.
 * The single most useful thing this program can say is that a deadline changed,
 * and a store that overwrites cannot say it.
 *
 * Content hashes are deliberately not in that log. They are derived state, and
 * writing an event per page per scan to record that nothing happened would bloat
 * the table and drown the signal it exists to carry. They also cannot live on
 * the settlement row: an uneventful re-extraction writes no update, so the fresh
 * hash would be discarded in exactly the case it was needed, and a stable page
 * would be re-read at full price every night.
 */
import { Claim, Profile, Settlement, defaultProfile, today } from "./model";

export interface Change {
  field: string;
  from: string;
  to: string;
}

export class Store {
  constructor(private db: D1Database) {}

  async settlements(): Promise<Settlement[]> {
    const { results } = await this.db
      .prepare("SELECT data FROM settlements ORDER BY COALESCE(NULLIF(claim_deadline,''),'9999-12-31')")
      .all<{ data: string }>();
    return results.map((r) => JSON.parse(r.data) as Settlement);
  }

  async settlement(id: string): Promise<Settlement | undefined> {
    const row = await this.db.prepare("SELECT data FROM settlements WHERE id = ?")
      .bind(id).first<{ data: string }>();
    return row ? (JSON.parse(row.data) as Settlement) : undefined;
  }

  async seenHash(id: string): Promise<string> {
    const row = await this.db.prepare("SELECT hash FROM hashes WHERE id = ?")
      .bind(id).first<{ hash: string }>();
    return row?.hash ?? "";
  }

  /**
   * Note the text a settlement was extracted from. Called whenever a page is
   * read successfully, including when the extraction changed nothing: "we
   * looked and it was the same" is precisely the fact worth remembering.
   */
  async recordHash(id: string, hash: string): Promise<void> {
    if (!id || !hash) return;
    await this.db.prepare(
      "INSERT INTO hashes (id, hash) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET hash = excluded.hash",
    ).bind(id, hash).run();
  }

  /**
   * Record a settlement, returning whether it is new and what moved. FirstSeen
   * is carried forward from the existing record, because the incoming one was
   * built a moment ago and would otherwise report everything as found today.
   */
  async upsert(incoming: Settlement): Promise<{ isNew: boolean; changes: Change[] }> {
    const s = { ...incoming, last_seen: today() };
    const prev = await this.settlement(s.id);
    if (!prev) {
      if (!s.first_seen) s.first_seen = today();
      await this.write(s);
      await this.event("discovered", s.id, { settlement: s });
      return { isNew: true, changes: [] };
    }
    s.first_seen = prev.first_seen;
    const changes = diff(prev, s);
    await this.write(s);
    if (changes.length > 0) {
      await this.event("updated", s.id, { settlement: s, changes });
    }
    return { isNew: false, changes };
  }

  private async write(s: Settlement): Promise<void> {
    await this.db.prepare(
      `INSERT INTO settlements (id, data, stage, claim_deadline, source_id, first_seen, last_seen)
       VALUES (?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(id) DO UPDATE SET
         data = excluded.data, stage = excluded.stage,
         claim_deadline = excluded.claim_deadline, source_id = excluded.source_id,
         last_seen = excluded.last_seen`,
    ).bind(s.id, JSON.stringify(s), s.stage, s.claim_deadline, s.source_id,
      s.first_seen, s.last_seen).run();
  }

  async event(type: string, settlementId: string, data: unknown, user = ""): Promise<void> {
    await this.db
      .prepare("INSERT INTO events (at, type, settlement_id, user, data) VALUES (?, ?, ?, ?, ?)")
      .bind(new Date().toISOString(), type, settlementId, user, JSON.stringify(data)).run();
  }

  async claims(user: string): Promise<Claim[]> {
    const { results } = await this.db.prepare("SELECT data FROM claims WHERE user = ?")
      .bind(user).all<{ data: string }>();
    return results.map((r) => JSON.parse(r.data) as Claim);
  }

  async claim(user: string, id: string): Promise<Claim | undefined> {
    const row = await this.db
      .prepare("SELECT data FROM claims WHERE user = ? AND settlement_id = ?")
      .bind(user, id).first<{ data: string }>();
    return row ? (JSON.parse(row.data) as Claim) : undefined;
  }

  async setClaim(user: string, c: Claim): Promise<void> {
    const next = { ...c, updated_on: today() };
    await this.db.prepare(
      `INSERT INTO claims (user, settlement_id, data) VALUES (?, ?, ?)
       ON CONFLICT(user, settlement_id) DO UPDATE SET data = excluded.data`,
    ).bind(user, next.settlement_id, JSON.stringify(next)).run();
    await this.event("claim", next.settlement_id, next, user);
  }

  /** Every identity starts from the default profile rather than inheriting one. */
  async profile(user: string): Promise<Profile> {
    const row = await this.db.prepare("SELECT data FROM profile WHERE user = ?")
      .bind(user).first<{ data: string }>();
    return row ? (JSON.parse(row.data) as Profile) : defaultProfile();
  }

  async saveProfile(user: string, p: Profile): Promise<void> {
    const next = { ...p, updated_on: today() };
    await this.db.prepare(
      `INSERT INTO profile (user, data) VALUES (?, ?)
       ON CONFLICT(user) DO UPDATE SET data = excluded.data`,
    ).bind(user, JSON.stringify(next)).run();
  }

  /** Identities with a profile, for the scheduled digest. */
  async users(): Promise<string[]> {
    const { results } = await this.db.prepare("SELECT user FROM profile").all<{ user: string }>();
    return results.map((r) => r.user);
  }

  /**
   * Sources least-recently-scanned first. One invocation cannot cover the whole
   * registry, so without rotation the sources at the top of the list would be
   * rescanned every run and the ones at the bottom never reached.
   */
  async sourceOrder(ids: string[]): Promise<string[]> {
    const { results } = await this.db
      .prepare("SELECT id, last_scanned FROM source_state").all<{ id: string; last_scanned: string }>();
    const seen = new Map(results.map((r) => [r.id, r.last_scanned]));
    // Never-scanned sources sort first, which is what clears a cold backlog.
    return [...ids].sort((a, b) => (seen.get(a) ?? "").localeCompare(seen.get(b) ?? ""));
  }

  async markScanned(id: string): Promise<void> {
    await this.db.prepare(
      `INSERT INTO source_state (id, last_scanned) VALUES (?, ?)
       ON CONFLICT(id) DO UPDATE SET last_scanned = excluded.last_scanned`,
    ).bind(id, new Date().toISOString()).run();
  }

  async cacheGet(url: string): Promise<{ etag: string; modified: string; hash: string } | undefined> {
    const row = await this.db.prepare("SELECT etag, modified, hash FROM http_cache WHERE url = ?")
      .bind(url).first<{ etag: string; modified: string; hash: string }>();
    return row ?? undefined;
  }

  async cachePut(url: string, etag: string, modified: string, hash: string): Promise<void> {
    await this.db.prepare(
      `INSERT INTO http_cache (url, etag, modified, hash, fetched_at) VALUES (?, ?, ?, ?, ?)
       ON CONFLICT(url) DO UPDATE SET etag = excluded.etag, modified = excluded.modified,
         hash = excluded.hash, fetched_at = excluded.fetched_at`,
    ).bind(url, etag, modified, hash, new Date().toISOString()).run();
  }
}

/**
 * Fields whose movement is worth a line in the log and a line in the digest.
 * Summary wording and confidence drift on every re-extraction and are
 * deliberately not among them.
 */
export function diff(a: Settlement, b: Settlement): Change[] {
  const out: Change[] = [];
  const add = (field: string, from: unknown, to: unknown) => {
    const f = from === 0 || from === undefined || from === null ? "" : String(from);
    const t = to === 0 || to === undefined || to === null ? "" : String(to);
    if (f !== t) out.push({ field, from: f, to: t });
  };
  add("stage", a.stage, b.stage);
  add("claim_deadline", a.claim_deadline, b.claim_deadline);
  add("opt_out_deadline", a.opt_out_deadline, b.opt_out_deadline);
  add("claims_open_on", a.claims_open_on, b.claims_open_on);
  add("claim_url", a.claim_url, b.claim_url);
  add("payout_low", a.payout_low, b.payout_low);
  add("payout_high", a.payout_high, b.payout_high);
  add("settlement_fund", a.settlement_fund, b.settlement_fund);
  add("criteria", a.criteria?.length ?? 0, b.criteria?.length ?? 0);
  return out;
}
