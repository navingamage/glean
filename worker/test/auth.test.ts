import { describe, expect, test } from "vitest";
import {
  checkLogin, clearedCookie, credentials, isDefault, sameSecret,
  sessionCookie, sessionToken, signedIn,
} from "../src/auth";

const DEFAULTS = credentials({});

function login(username: string, password: string, next = "/"): Request {
  const body = new URLSearchParams({ username, password, next });
  return new Request("https://glean.antipodetech.com/login", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });
}

function withCookie(value: string): Request {
  return new Request("https://glean.antipodetech.com/", {
    headers: { Cookie: `glean_session=${value}` },
  });
}

describe("sign-in", () => {
  test("test / test opens a deployment that set nothing", async () => {
    expect(DEFAULTS).toEqual({ username: "test", password: "test" });
    expect(isDefault(DEFAULTS)).toBe(true);
    expect(await checkLogin(login("test", "test"), DEFAULTS)).toBe(true);
  });

  test("the environment overrides the defaults", async () => {
    const c = credentials({ GLEAN_USERNAME: "navin", GLEAN_PASSWORD: "hunter2" });
    expect(isDefault(c)).toBe(false);
    expect(await checkLogin(login("navin", "hunter2"), c)).toBe(true);
    expect(await checkLogin(login("test", "test"), c)).toBe(false);
  });

  test("either half being wrong is a failure", async () => {
    expect(await checkLogin(login("test", "nope"), DEFAULTS)).toBe(false);
    expect(await checkLogin(login("nope", "test"), DEFAULTS)).toBe(false);
    expect(await checkLogin(login("", ""), DEFAULTS)).toBe(false);
  });

  test("no cookie is not signed in", async () => {
    const bare = new Request("https://glean.antipodetech.com/");
    expect(await signedIn(bare, DEFAULTS)).toBe(false);
    expect(await signedIn(withCookie(""), DEFAULTS)).toBe(false);
  });

  test("the cookie the login sets is accepted afterwards", async () => {
    const header = await sessionCookie(DEFAULTS);
    const value = header.slice("glean_session=".length).split(";")[0]!;
    expect(await signedIn(withCookie(value), DEFAULTS)).toBe(true);
    expect(header).toContain("HttpOnly");
    expect(header).toContain("Secure");
    expect(header).toContain("SameSite=Lax");
  });

  test("the cookie is never the password", async () => {
    const token = await sessionToken(DEFAULTS);
    expect(token).not.toContain("test");
    expect(token).toMatch(/^[0-9a-f]{64}$/);
    // Guessing the username is not enough to mint a session for it.
    expect(await signedIn(withCookie("test"), DEFAULTS)).toBe(false);
  });

  test("changing the password invalidates the sessions it minted", async () => {
    const before = await sessionToken(DEFAULTS);
    const after = await sessionToken(credentials({ GLEAN_PASSWORD: "something-else" }));
    expect(after).not.toBe(before);
    expect(await signedIn(withCookie(before), credentials({ GLEAN_PASSWORD: "something-else" })))
      .toBe(false);
  });

  test("signing out expires the cookie", () => {
    expect(clearedCookie()).toContain("Max-Age=0");
  });

  test("the comparison does not stop at the first differing byte", () => {
    expect(sameSecret("abcd", "abcd")).toBe(true);
    expect(sameSecret("abcd", "abce")).toBe(false);
    expect(sameSecret("abcd", "abc")).toBe(false);
  });
});
