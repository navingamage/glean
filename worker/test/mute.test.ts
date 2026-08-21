import { describe, expect, test } from "vitest";
import {
  addMute, haystack, matchesTerm, mutedBy, normalizeTerm, removeMute, suggestTerm,
} from "../src/mute";
import { Profile, Settlement, defaultProfile } from "../src/model";

function settlement(over: Partial<Settlement> = {}): Settlement {
  return {
    id: "abc", title: "Major Junior Hockey Class Action", stage: "claims-open",
    summary: "Players in the CHL seeking unpaid wages.",
    claims_open_on: "", claim_deadline: "", opt_out_deadline: "",
    settlement_fund: 0, payout_low: 0, payout_high: 0, estimated_class_size: 0,
    proof_required: false, effort_minutes: 15, url: "https://example.ca",
    source_id: "src", extractor: "test", confidence: 1,
    first_seen: "2026-01-01", last_seen: "2026-01-01", ...over,
  };
}

function profileWith(...terms: string[]): Profile {
  const p = defaultProfile();
  for (const t of terms) addMute(p, t);
  return p;
}

describe("muting a subject", () => {
  test("a term hides the case it names", () => {
    expect(mutedBy(settlement(), profileWith("hockey"))).toBe("hockey");
    expect(mutedBy(settlement(), profileWith("bread"))).toBe("");
  });

  test("the criteria are searched, not only the title", () => {
    // The administrative title of a case often says nothing a reader would
    // recognise it by. The class definition does.
    const s = settlement({
      title: "Smith v. Canadian Hockey League et al",
      summary: "A proceeding.",
      criteria: [{
        kind: "employment", entity: "ohl", label: "Ontario Hockey League",
        window: { from: "", until: "" }, text: "played in the OHL", prevalence: 0.01,
      }],
    });
    expect(mutedBy(s, profileWith("ontario hockey league"))).toBe("ontario hockey league");
  });

  test("whole words only, so a short term cannot hide half the registry", () => {
    const s = settlement({ title: "GM Vehicle Emissions", summary: "Management of engines." });
    expect(matchesTerm(s, "gm")).toBe(true);
    expect(matchesTerm(settlement({ title: "Management fees", summary: "" }), "gm")).toBe(false);
  });

  test("case and spacing do not matter", () => {
    expect(normalizeTerm("  Major   Junior ")).toBe("major junior");
    expect(mutedBy(settlement(), profileWith("MAJOR   junior"))).toBe("major junior");
  });

  test("a phrase matches across a line break in the notice", () => {
    const s = settlement({ class_text: "anyone who played major\njunior hockey in Canada" });
    expect(matchesTerm(s, "major junior")).toBe(true);
  });

  test("regex metacharacters are literal, not a pattern", () => {
    const s = settlement({ title: "Rogers (Cable) Inc." });
    expect(matchesTerm(s, "(cable)")).toBe(true);
    expect(matchesTerm(s, ".*")).toBe(false);
  });

  test("terms are unique and removable by what was typed", () => {
    const p = profileWith("hockey");
    expect(addMute(p, "Hockey")).toBe(false);
    expect(p.mutes).toHaveLength(1);
    expect(removeMute(p, "HOCKEY")).toBe(true);
    expect(p.mutes).toHaveLength(0);
    expect(removeMute(p, "hockey")).toBe(false);
  });

  test("an empty term is not a rule that hides everything", () => {
    const p = defaultProfile();
    expect(addMute(p, "   ")).toBe(false);
    expect(p.mutes).toHaveLength(0);
    expect(matchesTerm(settlement(), "")).toBe(false);
  });

  test("a profile written before mutes existed has none", () => {
    const old = { ...defaultProfile(), mutes: undefined };
    expect(mutedBy(settlement(), old)).toBe("");
  });

  test("the suggested term skips the vocabulary every notice shares", () => {
    expect(suggestTerm(settlement({ title: "Canadian Bread Price-Fixing Class Action" })))
      .toBe("bread");
    expect(suggestTerm(settlement({ title: "Class Action Settlement" }))).toBe("");
  });

  test("the haystack is lowercased once, including the defendants", () => {
    const s = settlement({ defendants: ["Loblaw Companies Limited"] });
    expect(haystack(s)).toContain("loblaw companies limited");
  });
});
