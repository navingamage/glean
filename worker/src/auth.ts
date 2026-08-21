/**
 * Gating the dashboard.
 *
 * The profile behind this page is a list of every bank, shop, insurer and
 * employer the user has answered a question about, so nothing is served to a
 * request that has not signed in. Signing in is a username and a password on a
 * form, because the thing it replaced was a 44-character key pasted into a
 * query string, and a key that has to be carried between devices ends up in a
 * note or a message where it is worse off than a password would have been.
 *
 * The credentials come from GLEAN_USERNAME and GLEAN_PASSWORD and default to
 * test / test, which is what makes the deployment openable by anyone who is
 * shown it. Set both to something real before the store holds a real profile.
 *
 * The cookie is not the password. It is an HMAC of the username under the
 * password, so a stolen cookie cannot be read back into the credential that
 * minted it, and changing the password invalidates every session at once. It is
 * HttpOnly, Secure and SameSite=Lax, so a link from elsewhere cannot act on it.
 */
const COOKIE = "glean_session";

export const DEFAULT_USERNAME = "test";
export const DEFAULT_PASSWORD = "test";

/** Constant-time string comparison. */
export function sameSecret(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

function cookieValue(header: string | null, name: string): string {
  if (!header) return "";
  for (const part of header.split(";")) {
    const [k, ...rest] = part.trim().split("=");
    if (k === name) return rest.join("=");
  }
  return "";
}

export interface Credentials {
  username: string;
  password: string;
}

export function credentials(env: {
  GLEAN_USERNAME?: string;
  GLEAN_PASSWORD?: string;
}): Credentials {
  return {
    username: env.GLEAN_USERNAME || DEFAULT_USERNAME,
    password: env.GLEAN_PASSWORD || DEFAULT_PASSWORD,
  };
}

/** True when the credentials are still the ones anybody can guess. */
export function isDefault(c: Credentials): boolean {
  return c.username === DEFAULT_USERNAME && c.password === DEFAULT_PASSWORD;
}

function hex(bytes: ArrayBuffer): string {
  return Array.from(new Uint8Array(bytes))
    .map((b) => b.toString(16).padStart(2, "0")).join("");
}

/** The session value for a set of credentials: HMAC(username) under the password. */
export async function sessionToken(c: Credentials): Promise<string> {
  const key = await crypto.subtle.importKey(
    "raw", new TextEncoder().encode(c.password),
    { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = await crypto.subtle.sign(
    "HMAC", key, new TextEncoder().encode(`glean:${c.username}`));
  return hex(sig);
}

/** Whether this request already carries a valid session. */
export async function signedIn(req: Request, c: Credentials): Promise<boolean> {
  const have = cookieValue(req.headers.get("Cookie"), COOKIE);
  if (!have) return false;
  return sameSecret(have, await sessionToken(c));
}

export async function sessionCookie(c: Credentials): Promise<string> {
  const token = await sessionToken(c);
  return `${COOKIE}=${token}; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=31536000`;
}

export function clearedCookie(): string {
  return `${COOKIE}=; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=0`;
}

/**
 * Reads a posted login form. Both fields are compared in constant time, and
 * both have to match before anything is said about either, so a wrong guess
 * cannot be narrowed down to which half was wrong.
 */
export async function checkLogin(req: Request, c: Credentials): Promise<boolean> {
  const form = await req.formData();
  const user = String(form.get("username") ?? "");
  const pass = String(form.get("password") ?? "");
  const okUser = sameSecret(user, c.username);
  const okPass = sameSecret(pass, c.password);
  return okUser && okPass;
}
