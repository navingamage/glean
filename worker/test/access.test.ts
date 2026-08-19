import { describe, expect, test, vi, beforeEach } from "vitest";
import { accessEmail, identify, OWNER_IDENTITY } from "../src/access";

const TEAM = "antipode";
const AUD = "aud-tag-123";

function b64url(bytes: Uint8Array | string): string {
  const b = typeof bytes === "string" ? new TextEncoder().encode(bytes) : bytes;
  let bin = "";
  for (const x of b) bin += String.fromCharCode(x);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** A real RSA keypair, so the verification path is exercised rather than stubbed. */
async function keypair() {
  return crypto.subtle.generateKey(
    { name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
    true, ["sign", "verify"],
  ) as Promise<CryptoKeyPair>;
}

async function mint(priv: CryptoKey, claims: Record<string, unknown>, kid = "k1", alg = "RS256") {
  const header = b64url(JSON.stringify({ alg, kid, typ: "JWT" }));
  const payload = b64url(JSON.stringify(claims));
  const sig = await crypto.subtle.sign("RSASSA-PKCS1-v1_5", priv,
    new TextEncoder().encode(`${header}.${payload}`));
  return `${header}.${payload}.${b64url(new Uint8Array(sig))}`;
}

function req(headers: Record<string, string>): Request {
  return new Request("https://glean.antipodetech.com/", { headers });
}

function goodClaims(over: Record<string, unknown> = {}) {
  const now = Math.floor(Date.now() / 1000);
  return {
    iss: `https://${TEAM}.cloudflareaccess.com`,
    aud: [AUD], email: "Navin@Example.com", exp: now + 600, iat: now, ...over,
  };
}

describe("Access identity", () => {
  let pair: CryptoKeyPair;

  beforeEach(async () => {
    pair = await keypair();
    const jwk = await crypto.subtle.exportKey("jwk", pair.publicKey);
    vi.stubGlobal("fetch", vi.fn(async () =>
      new Response(JSON.stringify({ keys: [{ ...jwk, kid: "k1" }] }), { status: 200 })));
  });

  test("a valid assertion yields the email, lowercased", async () => {
    const token = await mint(pair.privateKey, goodClaims());
    const email = await accessEmail(req({ "Cf-Access-Jwt-Assertion": token }), TEAM, AUD);
    expect(email).toBe("navin@example.com");
  });

  test("the plain email header is never trusted", async () => {
    // Access sets this header, but so can anyone reaching the Worker directly.
    // Trusting it would let a stranger read any user's profile by naming them.
    const email = await accessEmail(
      req({ "Cf-Access-Authenticated-User-Email": "victim@example.com" }), TEAM, AUD);
    expect(email).toBe("");
    const who = await identify(
      req({ "Cf-Access-Authenticated-User-Email": "victim@example.com" }), TEAM, AUD);
    expect(who).toBe(OWNER_IDENTITY);
  });

  test("a token signed by the wrong key is rejected", async () => {
    const other = await keypair();
    const token = await mint(other.privateKey, goodClaims());
    expect(await accessEmail(req({ "Cf-Access-Jwt-Assertion": token }), TEAM, AUD)).toBe("");
  });

  test("expiry, issuer and audience are all checked", async () => {
    const now = Math.floor(Date.now() / 1000);
    const bad = [
      goodClaims({ exp: now - 10 }),
      goodClaims({ iss: "https://evil.cloudflareaccess.com" }),
      goodClaims({ aud: ["some-other-application"] }),
    ];
    for (const claims of bad) {
      const token = await mint(pair.privateKey, claims);
      expect(await accessEmail(req({ "Cf-Access-Jwt-Assertion": token }), TEAM, AUD)).toBe("");
    }
  });

  test("an unsigned token is rejected", async () => {
    // The classic JWT attack: swap the algorithm to none and drop the signature.
    const header = b64url(JSON.stringify({ alg: "none", kid: "k1", typ: "JWT" }));
    const payload = b64url(JSON.stringify(goodClaims()));
    expect(await accessEmail(req({ "Cf-Access-Jwt-Assertion": `${header}.${payload}.` }), TEAM, AUD)).toBe("");
  });

  test("without Access configured everything is the owner", async () => {
    const token = await mint(pair.privateKey, goodClaims());
    expect(await identify(req({ "Cf-Access-Jwt-Assertion": token }), undefined, undefined))
      .toBe(OWNER_IDENTITY);
    expect(await identify(req({}), TEAM, AUD)).toBe(OWNER_IDENTITY);
  });
});
