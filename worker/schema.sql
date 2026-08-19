-- glean's state in D1.
--
-- Settlements are stored as a JSON document with a few columns lifted out for
-- ordering and filtering. The document is the same shape the Go implementation
-- writes, so a store can be moved between the two without a translation step,
-- and adding a field to the record does not require a migration.

CREATE TABLE IF NOT EXISTS settlements (
  id             TEXT PRIMARY KEY,
  data           TEXT NOT NULL,
  stage          TEXT,
  claim_deadline TEXT,
  source_id      TEXT,
  first_seen     TEXT,
  last_seen      TEXT
);
CREATE INDEX IF NOT EXISTS settlements_deadline ON settlements(claim_deadline);

-- What each page said last time. Kept apart from the event log because it is
-- derived state, not history: writing an event per page per scan to record that
-- nothing happened would drown the signal the log exists to carry.
CREATE TABLE IF NOT EXISTS hashes (
  id   TEXT PRIMARY KEY,
  hash TEXT NOT NULL
);

-- Claims and profiles are per person; settlements are not.
--
-- A settlement is a public fact about a proceeding, it costs money to extract,
-- and scanning once for everybody is the whole efficiency argument. What
-- somebody answered about their own bank accounts, and what they have claimed,
-- belongs to them alone. `user` is a verified Cloudflare Access email, or
-- 'owner' when Access is not in front and the shared key is the only gate.
CREATE TABLE IF NOT EXISTS claims (
  user          TEXT NOT NULL,
  settlement_id TEXT NOT NULL,
  data          TEXT NOT NULL,
  PRIMARY KEY (user, settlement_id)
);

CREATE TABLE IF NOT EXISTS profile (
  user TEXT PRIMARY KEY,
  data TEXT NOT NULL
);

-- The audit trail: what was discovered, what moved, what was answered. The
-- single most useful thing this program can say is that a deadline changed, and
-- a store that overwrites cannot say it.
CREATE TABLE IF NOT EXISTS events (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  at            TEXT NOT NULL,
  type          TEXT NOT NULL,
  settlement_id TEXT,
  user          TEXT,
  data          TEXT
);
CREATE INDEX IF NOT EXISTS events_at ON events(at);

-- When each source was last walked.
--
-- One invocation cannot cover the whole registry, so sources are taken
-- least-recently-scanned first. Without this the same few sources at the top of
-- the list would be rescanned every run and the ones at the bottom would never
-- be reached at all.
CREATE TABLE IF NOT EXISTS source_state (
  id           TEXT PRIMARY KEY,
  last_scanned TEXT NOT NULL
);

-- Conditional-GET state, so a re-scan of forty pages costs forty conditional
-- requests and almost no bytes.
CREATE TABLE IF NOT EXISTS http_cache (
  url        TEXT PRIMARY KEY,
  etag       TEXT,
  modified   TEXT,
  hash       TEXT,
  fetched_at TEXT
);
