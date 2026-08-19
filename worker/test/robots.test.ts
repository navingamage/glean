import { describe, expect, test } from "vitest";
// The parser is module-private; re-exported for testing via a thin shim keeps
// the production surface honest.
import { __testing } from "../src/fetcher";

const { parseRobots, robotsAllows } = __testing;

const FILE = `
# comment
User-agent: *
Disallow: /private/
Disallow: /*.pdf$
Allow: /private/public-notice
Crawl-delay: 2

User-agent: gleanbot
User-agent: somebot
Disallow: /admin
`;

describe("robots.txt", () => {
  test("a specific agent group wins outright", () => {
    const r = parseRobots(FILE, "gleanbot/0.1 (+https://example)");
    // The wildcard group's rules must not leak into the specific group.
    expect(robotsAllows(r, "/private/anything")).toBe(true);
    expect(robotsAllows(r, "/admin/panel")).toBe(false);
  });

  test("the wildcard group is used when unnamed", () => {
    const r = parseRobots(FILE, "otherbot/1.0");
    const cases: [string, boolean][] = [
      ["/cases/bread", true],
      ["/private/files", false],
      ["/private/public-notice", true],       // longest match wins, tie to Allow
      ["/private/public-notice-2.pdf", true],
      ["/notice.pdf", false],                  // '$' anchored wildcard
      ["/notice.pdf?x=1", true],               // anchor means the path ends there
    ];
    for (const [path, want] of cases) {
      expect(robotsAllows(r, path), path).toBe(want);
    }
  });

  test("an empty file restricts nothing", () => {
    expect(robotsAllows(parseRobots("", "gleanbot"), "/anything")).toBe(true);
    expect(robotsAllows(undefined, "/x")).toBe(true);
  });
});
