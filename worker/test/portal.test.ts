import { describe, expect, test } from "vitest";
import { findPortal } from "../src/extract/portal";

describe("findPortal", () => {
  test("prefers the settlement's own domain", () => {
    const links = [
      { url: "https://x.com/CFMLawyers", text: "Follow us" },
      { url: "https://www.cbc.ca/news/business/bread-price-fixing-1.4878243", text: "Read the coverage" },
      { url: "http://www.CanadianBreadSettlement.ca", text: "Official settlement website" },
      { url: "https://cfmlawyers.ca/people", text: "Our team" },
    ];
    expect(findPortal(links, "https://cfmlawyers.ca/class-action/bread"))
      .toBe("http://www.CanadianBreadSettlement.ca");
  });

  test("declines when nothing looks official", () => {
    const links = [
      { url: "https://x.com/a", text: "X" },
      { url: "https://cbc.ca/news/story", text: "Coverage" },
    ];
    expect(findPortal(links, "https://firm.ca/cases/one")).toBe("");
  });

  test("ignores same-site links", () => {
    const links = [{ url: "https://firm.ca/settlement-claim-form", text: "File a claim" }];
    expect(findPortal(links, "https://firm.ca/cases/one")).toBe("");
  });
});
