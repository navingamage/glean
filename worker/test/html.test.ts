import { describe, expect, test } from "vitest";
import { cleanTitle } from "../src/html";

describe("cleanTitle", () => {
  test("strips a masthead but never part of the name", () => {
    const cases: [string, string, string][] = [
      ["Bread - CFM Lawyers", "https://cfmlawyers.ca/class-action/bread", "Bread"],
      ["Starbucks Class Action | Canooq.ca", "https://canooq.ca/x", "Starbucks Class Action"],
      ["GM Ignition Switch | Merchant Law Group LLP", "https://merchantlaw.com/x", "GM Ignition Switch"],
      ["CHL Class Action | Home", "https://charneylawyers.com/x", "CHL Class Action"],
      ["Settlement - Yaz | Consumer Law Group Class Actions Canada", "https://clg.org/x", "Settlement - Yaz"],
      // The tail is part of the name and must survive.
      ["Smith v. Loblaw - Ontario Class", "https://example.ca/x", "Smith v. Loblaw - Ontario Class"],
      ["Abilify Settlement", "https://mnp.ca/x", "Abilify Settlement"],
      ["Notice - claims must be filed by October 15 2026", "https://x.ca/y",
        "Notice - claims must be filed by October 15 2026"],
    ];
    for (const [title, url, want] of cases) {
      expect(cleanTitle(title, url), title).toBe(want);
    }
  });
});
