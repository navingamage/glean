import { describe, expect, test } from "vitest";
import { collapse } from "../src/dedupe";
import { Settlement } from "../src/model";

const AUTHORITY = new Map([["canooq", 1], ["cfm", 2], ["mnp", 4]]);

function s(id: string, source: string, title: string, deadline = "", claimURL = ""): Settlement {
  return {
    id, source_id: source, title, claim_deadline: deadline, claim_url: claimURL,
    stage: "claims-open", summary: "", claims_open_on: "", opt_out_deadline: "",
    settlement_fund: 0, payout_low: 0, payout_high: 0, estimated_class_size: 0,
    proof_required: false, effort_minutes: 10, url: "", extractor: "llm",
    confidence: 1, first_seen: "", last_seen: "",
  };
}

describe("collapse", () => {
  test("spacing differences still merge, and the administrator wins", () => {
    // The real pair: one site writes the name as three words, the other as one,
    // so they share no title words and nearly every trigram.
    const groups = collapse([
      s("a", "canooq", "Big Steel Box Flood Damage Settlement", "2026-10-30"),
      s("b", "mnp", "BigSteelBox Abbotsford flood damage settlement", "2026-10-30"),
    ], AUTHORITY);
    expect(groups).toHaveLength(1);
    expect(groups[0]!.primary.source_id).toBe("mnp");
  });

  test("similar titles with different deadlines stay separate", () => {
    const groups = collapse([
      s("a", "canooq", "Tobacco Claims Canada - Quebec Class Action Plan", "2026-08-31"),
      s("b", "canooq", "Tobacco Claims Canada - Pan-Canadian Claimants Plan", "2027-09-03"),
    ], AUTHORITY);
    expect(groups).toHaveLength(2);
  });

  test("different portals never merge", () => {
    const groups = collapse([
      s("a", "canooq", "Widget Defect Settlement", "", "https://widgetsettlement.ca/claim"),
      s("b", "cfm", "Widget Defect Settlement", "", "https://otherwidget.ca/claim"),
    ], AUTHORITY);
    expect(groups).toHaveLength(2);
  });

  test("a shared portal merges on a weaker title match", () => {
    const groups = collapse([
      s("a", "canooq", "Apple gift card scam settlement", "", "https://giftcardsettlement.ca/"),
      s("b", "mnp", "Apple Gift Card Class Action Settlement", "", "https://www.giftcardsettlement.ca/"),
    ], AUTHORITY);
    expect(groups).toHaveLength(1);
  });

  test("one unknown deadline is neutral and the fuller record wins", () => {
    const groups = collapse([
      s("a", "canooq", "Canadian Armed Forces Racial Discrimination / Harassment Settlement", "2026-10-15"),
      s("b", "canooq", "Canadian Armed Forces Racial Discrimination and Harassment Class Action"),
    ], AUTHORITY);
    expect(groups).toHaveLength(1);
    expect(groups[0]!.primary.id).toBe("a");
  });

  test("unrelated settlements are left alone and order is preserved", () => {
    const groups = collapse([
      s("first", "canooq", "Packaged bread price-fixing", "2027-06-30"),
      s("second", "canooq", "Rexulti side effects compulsive behaviour", "2026-08-27"),
      s("third", "cfm", "BC Birth Alerts"),
    ], AUTHORITY);
    expect(groups.map((g) => g.primary.id)).toEqual(["first", "second", "third"]);
  });
});
