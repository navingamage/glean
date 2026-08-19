import { describe, expect, test } from "vitest";
import { backfill, parseMoney, relevance } from "../src/extract/heuristic";
import { Settlement } from "../src/model";

function blank(): Settlement {
  return {
    id: "x", title: "", stage: "unknown", summary: "",
    claims_open_on: "", claim_deadline: "", opt_out_deadline: "",
    settlement_fund: 0, payout_low: 0, payout_high: 0, estimated_class_size: 0,
    proof_required: false, effort_minutes: 0, url: "", source_id: "",
    extractor: "", confidence: 0, first_seen: "", last_seen: "",
  };
}

describe("deadlines", () => {
  test("found across block boundaries", () => {
    // Notices lay deadlines out as label and value in separate cells at least as
    // often as they write them in a sentence, and those become separate lines.
    const s = blank();
    backfill(s, "Settlement summary\nOpt-out deadline\nAugust 31, 2026\nComments deadline\nSeptember 15, 2026");
    expect(s.opt_out_deadline).toBe("2026-08-31");
  });

  test("found in prose, in several shapes", () => {
    const cases: [string, string][] = [
      ["Claims must be postmarked on or before October 15, 2026.", "2026-10-15"],
      ["The claims deadline is 15 October 2026.", "2026-10-15"],
      ["Deadline to file a claim: 2026-10-15", "2026-10-15"],
      ["You must submit a claim by December 1, 2027 to be paid.", "2027-12-01"],
    ];
    for (const [text, want] of cases) {
      const s = blank();
      backfill(s, text);
      expect(s.claim_deadline, text).toBe(want);
    }
  });

  test("a cue does not reach into the next paragraph", () => {
    // "deadline" here belongs to a hearing and the date belongs to something
    // else. Reaching across would produce a confident, wrong date.
    const s = blank();
    backfill(s, "The approval hearing deadline for objections has passed.\n\n\n\n\nUnrelated notice dated March 3, 2030.");
    expect(s.claim_deadline).not.toBe("2030-03-03");
  });
});

describe("money", () => {
  test("handles the shapes notices actually use", () => {
    expect(parseMoney("$7.5 million settlement")).toBe(7_500_000);
    expect(parseMoney("$26-million")).toBe(26_000_000);
    expect(parseMoney("$1,250,000")).toBe(1_250_000);
    expect(parseMoney("up to $5,000")).toBe(5000);
    expect(parseMoney("$25")).toBe(25);
    expect(parseMoney("no amount here")).toBe(0);
  });
});

describe("relevance gate", () => {
  test("passes a notice and rejects a careers page", () => {
    const notice = "Notice of certified class action settlement. Class members may file a claim " +
      "before the claim deadline. The claims administrator will process each claim form. " +
      "The class period runs from 2001 to 2021. Eligible class members may opt out. " +
      "This settlement was court approved and the claim form is available online now. " +
      "Please read this notice of settlement carefully to decide whether to submit a claim.";
    expect(relevance(notice)).toBeGreaterThanOrEqual(8);

    const careers = "Join our team. See our practice areas and our lawyers. Privacy policy and terms of use apply.";
    expect(relevance(careers)).toBeLessThan(8);
  });
});
