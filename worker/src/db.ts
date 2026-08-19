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

  async event(type: string, settlementId: string, data: unknown): Promise<void> {
    await this.db.prepare("INSERT INTO events (at, type, settlement_id, data) VALUES (?, ?, ?, ?)")
      .bind(new Date().toISOString(), type, settlementId, JSON.stringify(data)).run();
  }

  async claims(): Promise<Claim[]> {
    const { results } = await this.db.prepare("SELECT data FROM claims").all<{ data: string }>();
    return results.map((r) => JSON.parse(r.data) as Claim);
  }

  async claim(id: string): Promise<Claim | undefined> {
    const row = await this.db.prepare("SELECT data FROM claims WHERE settlement_id = ?")
      .bind(id).first<{ data: string }>();
    return row ? (JSON.parse(row.data) as Claim) : undefined;
  }

  async setClaim(c: Claim): Promise<void> {
    const next = { ...c, updated_on: today() };
    await this.db.prepare(
      `INSERT INTO claims (settlement_id, data) VALUES (?, ?)
       ON CONFLICT(settlement_id) DO UPDATE SET data = excluded.data`,
    ).bind(next.settlement_id, JSON.stringify(next)).run();
    await this.event("claim", next.settlement_id, next);
  }

  async profile(): Promise<Profile> {
    const row = await this.db.prepare("SELECT data FROM profile WHERE id = 1")
      .first<{ data: string }>();
    return row ? (JSON.parse(row.data) as Profile) : defaultProfile();
  }

  async saveProfile(p: Profile): Promise<void> {
    const next = { ...p, updated_on: today() };
    await this.db.prepare(
      `INSERT INTO profile (id, data) VALUES (1, ?)
       ON CONFLICT(id) DO UPDATE SET data = excluded.data`,
    ).bind(JSON.stringify(next)).run();
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
