import { describe, expect, test } from "vitest";
import { True, False, Unknown } from "../src/logic";
import {
  assess, estimatePayout, questions, rank, totalAtStake, PER_SETTLEMENT_CAP,
} from "../src/match";
import {
  Criterion, Profile, Settlement, defaultProfile, EMPTY_WINDOW,
} from "../src/model";

function win(from: string, until: string) {
  return { from, until };
}

function settlement(over: Partial<Settlement>): Settlement {
  return {
    id: "s", title: "S", stage: "claims-open", summary: "",
    claims_open_on: "", claim_deadline: "", opt_out_deadline: "",
    settlement_fund: 0, payout_low: 0, payout_high: 0, estimated_class_size: 0,
    proof_required: false, effort_minutes: 10, url: "https://x.ca",
    source_id: "t", extractor: "llm", confidence: 1,
    first_seen: "2026-01-01", last_seen: "2026-01-01",
    ...over,
  };
}

function crit(over: Partial<Criterion>): Criterion {
  return {
    kind: "purchase", entity: "x", label: "X", window: { ...EMPTY_WINDOW },
    text: "", prevalence: 0.5, ...over,
  };
}

/** The Loblaws bread class: nationwide, twenty-year period, banner names. */
function bread(): Settlement {
  return settlement({
    id: "bread", title: "Packaged bread price-fixing",
    claim_deadline: "2027-06-30", payout_low: 25, payout_high: 25,
    criteria: [
      crit({ kind: "residency", entity: "CA", label: "Canada", prevalence: 1 }),
      crit({ entity: "no-frills", label: "No Frills", group: "banner", window: win("2001-01-01", "2021-12-31") }),
      crit({ entity: "sobeys", label: "Sobeys", group: "banner", window: win("2001-01-01", "2021-12-31") }),
    ],
  });
}

function record(p: Profile, h: Partial<Profile["holdings"][number]>) {
  p.holdings.push({
    kind: "purchase", entity: "", label: "", window: { ...EMPTY_WINDOW },
    added_on: "2026-01-01", ...h,
  } as Profile["holdings"][number]);
}

describe("eligibility", () => {
  test("a banner alias satisfies a class written in another banner's name", () => {
    const p = defaultProfile();
    record(p, { entity: "Loblaws", label: "Loblaws" });
    const a = assess(bread(), p);
    expect(a.verdict).toBe(True);
    expect(a.questions).toHaveLength(0);
    expect(a.expectedValue).toBe(25);
  });

  test("an alternative group needs only one branch", () => {
    // Saying no to one banner must not rule the class out while another branch
    // in the same group is still open.
    const p = defaultProfile();
    record(p, { entity: "sobeys", label: "Sobeys", negative: true });
    const a = assess(bread(), p);
    expect(a.verdict).toBe(Unknown);
    expect(a.questions).toHaveLength(1);
    expect(a.questions[0]!.entity).toBe("loblaw");
  });

  test("a ruled-out settlement stops asking and is priced at zero", () => {
    const p = defaultProfile();
    record(p, { entity: "loblaw", negative: true });
    record(p, { entity: "sobeys", negative: true });
    const a = assess(bread(), p);
    expect(a.verdict).toBe(False);
    expect(a.questions).toHaveLength(0);
    expect(a.expectedValue).toBe(0);
  });

  test("an unanswered profile produces priced questions", () => {
    const a = assess(bread(), defaultProfile());
    expect(a.verdict).toBe(Unknown);
    expect(a.questions).toHaveLength(2);
    expect(a.probability).toBeCloseTo(0.75, 5);
    expect(a.questions[0]!.ask).toBe(
      "Did you buy No Frills between 2001-01-01 and 2021-12-31?");
  });

  test("province is a wildcard in one direction only", () => {
    const ontarioOnly = settlement({
      id: "on", payout_low: 100, payout_high: 100,
      criteria: [crit({ kind: "residency", entity: "ON", label: "Ontario" })],
    });
    expect(assess(ontarioOnly, defaultProfile()).verdict).toBe(Unknown);

    const alberta = defaultProfile();
    alberta.residences = [{ province: "AB", window: { ...EMPTY_WINDOW } }];
    expect(assess(ontarioOnly, alberta).verdict).toBe(False);

    const national = settlement({
      id: "ca", criteria: [crit({ kind: "residency", entity: "CA", label: "Canada" })],
    });
    expect(assess(national, alberta).verdict).toBe(True);
  });

  test("a class period excludes a non-overlapping holding but not an undated one", () => {
    const s = settlement({
      id: "bank", payout_low: 50, payout_high: 50,
      criteria: [crit({ kind: "account", entity: "cibc", label: "CIBC", window: win("2018-01-01", "2022-12-31") })],
    });
    const closed = defaultProfile();
    record(closed, { kind: "account", entity: "CIBC", label: "CIBC", window: win("2005-01-01", "2010-01-01") });
    expect(assess(s, closed).verdict).toBe(False);

    const undated = defaultProfile();
    record(undated, { kind: "account", entity: "CIBC", label: "CIBC" });
    expect(assess(s, undated).verdict).toBe(True);
  });

  test("no criteria is unknown, never eligible", () => {
    // The empty conjunction is True as a matter of logic. Reporting that as
    // "you are in the class" would be the most damaging thing this can say.
    const a = assess(settlement({ id: "bare", payout_low: 100, payout_high: 100 }), defaultProfile());
    expect(a.verdict).toBe(Unknown);
    expect(a.note).toBeTruthy();
    expect(a.expectedValue).toBeLessThan(a.payout);
  });
});

describe("questions", () => {
  test("read as English for every window shape", () => {
    const ask = (w: { from: string; until: string }) =>
      assess(settlement({
        criteria: [crit({ kind: "medical", entity: "drug", label: "Drug", window: w })],
      }), defaultProfile()).questions[0]!.ask;

    expect(ask(EMPTY_WINDOW)).toBe("Were you prescribed or treated with Drug?");
    expect(ask({ from: "", until: "2012-03-12" }))
      .toBe("Were you prescribed or treated with Drug on or before 2012-03-12?");
    expect(ask({ from: "2012-03-12", until: "" }))
      .toBe("Were you prescribed or treated with Drug at any time since 2012-03-12?");
    expect(ask(win("2001-01-01", "2021-12-31")))
      .toBe("Were you prescribed or treated with Drug between 2001-01-01 and 2021-12-31?");
  });

  test("merge across settlements and are priced by expected value", () => {
    const mk = (id: string, payout: number) => settlement({
      id, payout_low: payout, payout_high: payout,
      // prevalence 0 falls through to the 0.3 default, which is the case being
      // priced here: a criterion the extractor gave no estimate for.
      criteria: [crit({ kind: "account", entity: "cibc", label: "CIBC", prevalence: 0 })],
    });
    const p = defaultProfile();
    const as = [assess(mk("a", 30), p), assess(mk("b", 70), p)];
    const qs = questions(as);
    expect(qs).toHaveLength(1);
    // Probability-weighted at the default prevalence of 0.3, not the full $100.
    expect(qs[0]!.worth).toBeCloseTo(30, 5);
    expect(qs[0]!.unlocks).toHaveLength(2);
    expect(totalAtStake(qs, as)).toBeCloseTo(30, 5);
  });

  test("one settlement cannot monopolise the queue", () => {
    // The hockey class is one alternative per league per province: nine
    // questions of identical value about a single case.
    const leagues = ["ohl-on", "qmjhl-nb", "qmjhl-ns", "qmjhl-pe", "qmjhl-qc",
      "whl-ab", "whl-bc", "whl-sk", "whl-mb"];
    const hockey = settlement({
      id: "hockey", payout_low: 9000, payout_high: 9000,
      criteria: leagues.map((l) =>
        crit({ kind: "employment", entity: l, label: l, group: "league", prevalence: 0.001 })),
    });
    const loaf = settlement({
      id: "bread", payout_low: 25, payout_high: 25,
      criteria: [crit({ entity: "loblaw", label: "Loblaws", prevalence: 0.8 })],
    });
    const p = defaultProfile();
    const qs = questions([assess(hockey, p), assess(loaf, p)]);

    const counts = new Map<string, number>();
    for (const q of qs) for (const id of q.unlocks) counts.set(id, (counts.get(id) ?? 0) + 1);
    expect(counts.get("hockey") ?? 0).toBeLessThanOrEqual(PER_SETTLEMENT_CAP);
    expect(counts.get("bread") ?? 0).toBeGreaterThan(0);
  });
});

describe("ranking", () => {
  test("prefers the claim that closes first", () => {
    const inDays = (n: number) =>
      new Date(Date.now() + n * 86_400_000).toISOString().slice(0, 10);
    const soon = settlement({ id: "soon", claim_deadline: inDays(5), payout_low: 40, payout_high: 40 });
    const later = settlement({ id: "later", claim_deadline: inDays(365), payout_low: 70, payout_high: 70 });
    const gone = settlement({ id: "gone", claim_deadline: "2020-01-01", payout_low: 500, payout_high: 500 });

    const byId = new Map([["soon", soon], ["later", later], ["gone", gone]]);
    const p = defaultProfile();
    const as = [assess(later, p), assess(gone, p), assess(soon, p)];
    rank(as, byId);
    expect(as.map((a) => a.settlementId)).toEqual(["soon", "later", "gone"]);
  });

  test("unpriced settlements rank by probability", () => {
    const mk = (id: string, prevalence: number) => settlement({
      id, criteria: [crit({ kind: "service", entity: id, label: id, prevalence })],
    });
    const likely = mk("likely", 0.9), unlikely = mk("unlikely", 0.02);
    const byId = new Map([["likely", likely], ["unlikely", unlikely]]);
    const as = [assess(unlikely, defaultProfile()), assess(likely, defaultProfile())];
    rank(as, byId);
    expect(as[0]!.settlementId).toBe("likely");
    // The proxy must not leak into anything shown as a dollar figure.
    for (const a of as) expect(a.payout).toBe(0);
  });
});

describe("payout", () => {
  test("falls back to the fund only with a class size", () => {
    expect(estimatePayout(settlement({ settlement_fund: 10_000_000, estimated_class_size: 100_000 }))).toBe(60);
    expect(estimatePayout(settlement({ settlement_fund: 10_000_000 }))).toBe(0);
  });
});
