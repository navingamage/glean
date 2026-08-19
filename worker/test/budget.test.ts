import { describe, expect, test } from "vitest";
import { BudgetExhausted, Fetcher } from "../src/fetcher";
import { Store } from "../src/db";

/** A store stub: the budget logic touches only the HTTP cache. */
function fakeStore(): Store {
  return {
    cacheGet: async () => undefined,
    cachePut: async () => {},
  } as unknown as Store;
}

describe("subrequest budget", () => {
  test("stops before the platform cap rather than being killed at it", async () => {
    // A Worker gets fifty outbound requests per invocation on the free plan, and
    // the registry needs several hundred. Being cut off mid-write is the failure
    // this prevents: stopping deliberately keeps everything already committed.
    const original = globalThis.fetch;
    let calls = 0;
    globalThis.fetch = (async () => {
      calls++;
      return new Response("<html><body><p>x</p></body></html>",
        { headers: { "Content-Type": "text/html" } });
    }) as typeof fetch;

    try {
      const f = new Fetcher(fakeStore(), true, 5);
      let thrown: unknown;
      try {
        // Distinct hosts, so each costs a robots.txt fetch plus the page.
        for (let i = 0; i < 10; i++) await f.get(`https://host${i}.example/page`);
      } catch (e) {
        thrown = e;
      }
      expect(thrown).toBeInstanceOf(BudgetExhausted);
      expect(f.used).toBeLessThanOrEqual(5);
      expect(calls).toBeLessThanOrEqual(5);
      expect(f.remaining()).toBe(0);
    } finally {
      globalThis.fetch = original;
    }
  });

  test("robots.txt is charged, because the platform charges it too", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = (async () =>
      new Response("", { headers: { "Content-Type": "text/html" } })) as typeof fetch;
    try {
      const f = new Fetcher(fakeStore(), true, 40);
      await f.get("https://one.example/a");
      // One robots.txt for the host, one for the page.
      expect(f.used).toBe(2);
      await f.get("https://one.example/b");
      // robots is cached per host, so the second page costs one.
      expect(f.used).toBe(3);
    } finally {
      globalThis.fetch = original;
    }
  });
});
