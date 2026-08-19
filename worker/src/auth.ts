/**
 * Gating the dashboard.
 *
 * The profile behind this page is a list of every bank, shop, insurer and
 * employer the user has answered a question about. Publishing that on a URL
 * anyone can guess would be a far worse outcome than the program failing to
 * deploy, so the Worker refuses to serve anything without a configured key.
 *
 * A single shared key rather than accounts, because there is exactly one user.
 * The comparison is constant-time so a wrong guess leaks nothing about how
 * wrong it was, and the cookie is HttpOnly, Secure and SameSite=Lax so a link
 * from elsewhere cannot act on the session.
 */
const COOKIE = "glean_session";

/** Constant-time string comparison. */
function sameSecret(a: string, b: string): boolean {
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

export type AuthResult =
  | { ok: true }
  | { ok: false; response: Response };

export function authorize(req: Request, key: string | undefined): AuthResult {
  if (!key) {
    // Refusing to serve is the only safe behaviour. A deployment that forgot the
    // secret should be visibly broken, never quietly public.
    return {
      ok: false,
      response: new Response(
        "glean: GLEAN_ACCESS_KEY is not set, so the dashboard is closed.\n" +
        "Set it with: wrangler secret put GLEAN_ACCESS_KEY\n",
        { status: 503, headers: { "Content-Type": "text/plain; charset=utf-8" } },
      ),
    };
  }

  const url = new URL(req.url);
  const fromQuery = url.searchParams.get("k") ?? "";
  if (fromQuery && sameSecret(fromQuery, key)) {
    // Set the cookie and bounce to the same path without the key, so it does not
    // sit in history, in a bookmark, or in a Referer header on the way out.
    url.searchParams.delete("k");
    return {
      ok: false,
      response: new Response(null, {
        status: 303,
        headers: {
          Location: url.pathname + (url.search || ""),
          "Set-Cookie": `${COOKIE}=${key}; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=31536000`,
        },
      }),
    };
  }

  if (sameSecret(cookieValue(req.headers.get("Cookie"), COOKIE), key)) {
    return { ok: true };
  }

  return {
    ok: false,
    response: new Response(
      "glean: not authorized.\n\nOpen https://<host>/?k=YOUR_ACCESS_KEY once to sign in.\n",
      {
        status: 401,
        headers: {
          "Content-Type": "text/plain; charset=utf-8",
          // Nothing here should ever be indexed or cached by anything.
          "Cache-Control": "no-store",
          "X-Robots-Tag": "noindex, nofollow",
        },
      },
    ),
  };
}
