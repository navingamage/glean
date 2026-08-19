/**
 * Identity, from Cloudflare Access when it is in front and from the shared key
 * when it is not.
 *
 * The important rule here is that the `Cf-Access-Authenticated-User-Email`
 * header is never trusted. Access sets it, but so can anyone who reaches the
 * Worker without going through Access, and a Worker on a workers.dev subdomain
 * is reachable directly by default. Trusting that header would mean any
 * stranger could name themselves as any user and read that user's profile.
 *
 * What is trusted is `Cf-Access-Jwt-Assertion`, a JWT signed by the team's
 * private key, verified here against the team's published public keys with the
 * audience and issuer checked. That is unforgeable without the key.
 *
 * When no valid assertion is present the request falls back to the single owner
 * identity, which the shared access key already gated. So the Worker is correct
 * whether or not Access is configured: with it, several people can each have
 * their own profile; without it, there is one profile behind one secret.
 */

const OWNER = "owner";

interface Jwks {
  keys: JsonWebKey[];
}

/** Cached per isolate. The certs rotate slowly and a fetch per request is waste. */
let jwksCache: { at: number; keys: Map<string, CryptoKey> } | undefined;
const JWKS_TTL_MS = 60 * 60 * 1000;

function b64urlToBytes(s: string): Uint8Array {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function decodeJson(part: string): any {
  return JSON.parse(new TextDecoder().decode(b64urlToBytes(part)));
}

async function loadKeys(team: string): Promise<Map<string, CryptoKey>> {
  const now = Date.now();
  if (jwksCache && now - jwksCache.at < JWKS_TTL_MS) return jwksCache.keys;

  const resp = await fetch(`https://${team}.cloudflareaccess.com/cdn-cgi/access/certs`);
  if (!resp.ok) throw new Error(`access: certs ${resp.status}`);
  const jwks = (await resp.json()) as Jwks;

  const keys = new Map<string, CryptoKey>();
  for (const jwk of jwks.keys ?? []) {
    const kid = (jwk as any).kid as string | undefined;
    if (!kid) continue;
    try {
      keys.set(kid, await crypto.subtle.importKey(
        "jwk", jwk,
        { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" },
        false, ["verify"],
      ));
    } catch { /* a key we cannot import is a key we do not accept */ }
  }
  jwksCache = { at: now, keys };
  return keys;
}

/**
 * The verified email from an Access assertion, or "" when there is none, it does
 * not verify, or Access is not configured. Never throws into the request path:
 * a certs endpoint having a bad minute should fall back to the owner identity,
 * not take the dashboard down.
 */
export async function accessEmail(
  req: Request,
  team: string | undefined,
  aud: string | undefined,
): Promise<string> {
  const token = req.headers.get("Cf-Access-Jwt-Assertion");
  if (!token || !team || !aud) return "";

  try {
    const parts = token.split(".");
    if (parts.length !== 3) return "";
    const [rawHeader, rawPayload, rawSig] = parts as [string, string, string];

    const header = decodeJson(rawHeader);
    if (header.alg !== "RS256") return ""; // never accept "none", never accept HS256
    const keys = await loadKeys(team);
    const key = keys.get(header.kid);
    if (!key) return "";

    const signed = new TextEncoder().encode(`${rawHeader}.${rawPayload}`);
    const ok = await crypto.subtle.verify(
      "RSASSA-PKCS1-v1_5", key, b64urlToBytes(rawSig), signed);
    if (!ok) return "";

    const claims = decodeJson(rawPayload);
    const now = Math.floor(Date.now() / 1000);
    if (typeof claims.exp === "number" && claims.exp < now) return "";
    if (typeof claims.nbf === "number" && claims.nbf > now + 60) return "";
    if (claims.iss !== `https://${team}.cloudflareaccess.com`) return "";

    // aud is the application tag, and checking it is what stops a token minted
    // for one Access application being replayed against another.
    const audience: string[] = Array.isArray(claims.aud) ? claims.aud : [claims.aud];
    if (!audience.includes(aud)) return "";

    const email = String(claims.email ?? "").toLowerCase().trim();
    return email;
  } catch {
    return "";
  }
}

/**
 * Who this request is, for scoping the profile and the claims.
 *
 * Settlements are deliberately not scoped. They are public facts about
 * proceedings, they cost money to extract, and scanning once for everybody is
 * the entire efficiency argument. Only the answers about a person, and what
 * that person has claimed, belong to that person.
 */
export async function identify(
  req: Request,
  team: string | undefined,
  aud: string | undefined,
): Promise<string> {
  return (await accessEmail(req, team, aud)) || OWNER;
}

export const OWNER_IDENTITY = OWNER;
