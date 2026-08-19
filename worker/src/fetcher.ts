/**
 * The HTTP front door: identifies itself, obeys robots.txt, and remembers what
 * it saw last time so a re-scan costs conditional requests and almost no bytes.
 */
import { Store } from "./db";
import { sha256Hex } from "./normalize";

/** An unattributed scraper is one an administrator can only block. */
const USER_AGENT =
  "gleanbot/0.1 (+https://github.com/navingamage/glean; personal class action tracker)";

/**
 * The one-shot retry for a site that answers 403 to an identifying agent.
 *
 * robots.txt is the machine-readable statement of what a site owner wants
 * crawled, and it is honoured as a hard gate this retry does not touch. A 403 on
 * a public page that robots.txt explicitly permits is not that statement; it is
 * a firewall rule matching on the shape of a User-Agent string.
 */
const BROWSER_AGENT =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36";

const MAX_BODY = 4 << 20;

export class Disallowed extends Error {
  constructor(url: string) {
    super(`disallowed by robots.txt: ${url}`);
  }
}

export interface Fetched {
  url: string;
  body: string;
  contentType: string;
  hash: string;
  changed: boolean;
}

interface Robots {
  allow: string[];
  disallow: string[];
}

/** Longest-match wins, and a tie goes to Allow. */
function robotsAllows(r: Robots | undefined, path: string): boolean {
  if (!r) return true;
  let best = -1, allow = true;
  for (const p of r.disallow) {
    const n = matchLen(p, path);
    if (n > best) { best = n; allow = false; }
  }
  for (const p of r.allow) {
    const n = matchLen(p, path);
    if (n >= best) { best = n; allow = true; }
  }
  return allow;
}

/** Length of pattern p if it matches path, else -1. Supports '*' and a trailing '$'. */
function matchLen(p: string, path: string): number {
  const anchored = p.endsWith("$");
  const pat = anchored ? p.slice(0, -1) : p;
  let pos = 0;
  const parts = pat.split("*");
  for (let i = 0; i < parts.length; i++) {
    const part = parts[i]!;
    if (!part) continue;
    if (i === 0) {
      if (!path.startsWith(part, pos)) return -1;
      pos += part.length;
    } else {
      const idx = path.indexOf(part, pos);
      if (idx < 0) return -1;
      pos = idx + part.length;
    }
  }
  if (anchored && pos !== path.length) return -1;
  return pat.length;
}

/**
 * Keep the group applying to us, falling back to the wildcard. Where both exist
 * the specific group wins outright, which is what the standard says.
 */
function parseRobots(body: string, agent: string): Robots {
  const groups: { names: string[]; rules: Robots }[] = [];
  let cur: { names: string[]; rules: Robots } | undefined;
  let inAgents = false;

  for (let line of body.split("\n")) {
    const hash = line.indexOf("#");
    if (hash >= 0) line = line.slice(0, hash);
    const at = line.indexOf(":");
    if (at < 0) continue;
    const key = line.slice(0, at).trim().toLowerCase();
    const value = line.slice(at + 1).trim();

    if (key === "user-agent") {
      if (!cur || !inAgents) {
        cur = { names: [], rules: { allow: [], disallow: [] } };
        groups.push(cur);
      }
      cur.names.push(value.toLowerCase());
      inAgents = true;
      continue;
    }
    if (cur && value) {
      if (key === "disallow") cur.rules.disallow.push(value);
      else if (key === "allow") cur.rules.allow.push(value);
    }
    inAgents = false;
  }

  const low = agent.toLowerCase();
  let wildcard: Robots | undefined;
  for (const g of groups) {
    for (const name of g.names) {
      if (name === "*" && !wildcard) wildcard = g.rules;
      if (name !== "*" && low.includes(name)) return g.rules;
    }
  }
  return wildcard ?? { allow: [], disallow: [] };
}

export class Fetcher {
  private robots = new Map<string, Robots>();

  constructor(private store: Store, private ignoreCache = false) {}

  async get(rawURL: string): Promise<Fetched> {
    const u = new URL(rawURL);
    if (u.protocol !== "http:" && u.protocol !== "https:") {
      throw new Error(`unsupported scheme ${u.protocol}`);
    }
    const rules = await this.robotsFor(u);
    if (!robotsAllows(rules, u.pathname)) throw new Disallowed(rawURL);

    const cached = this.ignoreCache ? undefined : await this.store.cacheGet(rawURL);
    const headers: Record<string, string> = {
      "User-Agent": USER_AGENT,
      Accept: "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1",
      "Accept-Language": "en-CA,en;q=0.9,fr-CA;q=0.5",
    };
    if (cached?.etag) headers["If-None-Match"] = cached.etag;
    if (cached?.modified) headers["If-Modified-Since"] = cached.modified;

    let resp = await fetch(rawURL, { headers, redirect: "follow" });
    if ((resp.status === 403 || resp.status === 406)) {
      resp = await fetch(rawURL, {
        headers: { ...headers, "User-Agent": BROWSER_AGENT }, redirect: "follow",
      });
    }

    if (resp.status === 304 && cached) {
      return {
        url: rawURL, body: "", contentType: "",
        hash: cached.hash, changed: false,
      };
    }
    if (!resp.ok) throw new Error(`fetch ${rawURL}: ${resp.status}`);

    const body = (await resp.text()).slice(0, MAX_BODY);
    const hash = await sha256Hex(body);
    await this.store.cachePut(rawURL, resp.headers.get("ETag") ?? "",
      resp.headers.get("Last-Modified") ?? "", hash);

    return {
      url: rawURL,
      body,
      contentType: resp.headers.get("Content-Type") ?? "",
      hash,
      changed: !cached || cached.hash !== hash,
    };
  }

  private async robotsFor(u: URL): Promise<Robots | undefined> {
    const key = `${u.protocol}//${u.host}`;
    const hit = this.robots.get(key);
    if (hit) return hit;
    // A missing or unreadable robots.txt means no restrictions, which is the
    // standard's reading and the only one that does not turn a flaky server into
    // a permanent refusal to crawl.
    let rules: Robots = { allow: [], disallow: [] };
    try {
      const resp = await fetch(`${key}/robots.txt`, { headers: { "User-Agent": USER_AGENT } });
      if (resp.ok) rules = parseRobots((await resp.text()).slice(0, 512 << 10), USER_AGENT);
    } catch { /* treated as absent */ }
    this.robots.set(key, rules);
    return rules;
  }
}

/** Exposed for tests. Not part of the runtime surface. */
export const __testing = { parseRobots, robotsAllows, matchLen };

export function isHTML(f: Fetched): boolean {
  const ct = f.contentType.toLowerCase();
  return !ct || ct.includes("html") || ct.includes("xml") || ct.includes("text/plain");
}
