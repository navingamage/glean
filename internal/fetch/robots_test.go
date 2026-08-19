package fetch

import (
	"testing"
	"time"
)

const file = `
# comment
User-agent: *
Disallow: /private/
Disallow: /*.pdf$
Allow: /private/public-notice
Crawl-delay: 2

User-agent: gleanbot
User-agent: somebot
Disallow: /admin
Crawl-delay: 5
`

func TestSpecificAgentGroupWinsOutright(t *testing.T) {
	r := parseRobots(file, "gleanbot/0.1 (+https://example)")
	if r.delay != 5*time.Second {
		t.Errorf("crawl delay = %v, want 5s", r.delay)
	}
	// The wildcard group's rules must not leak into the specific group.
	if !r.allowed("/private/anything") {
		t.Error("wildcard Disallow leaked into the gleanbot group")
	}
	if r.allowed("/admin/panel") {
		t.Error("/admin should be disallowed for gleanbot")
	}
}

func TestWildcardGroupUsedWhenUnnamed(t *testing.T) {
	r := parseRobots(file, "otherbot/1.0")
	if r.delay != 2*time.Second {
		t.Errorf("crawl delay = %v, want 2s", r.delay)
	}
	cases := map[string]bool{
		"/cases/bread":                 true,
		"/private/files":               false,
		"/private/public-notice":       true, // longest match wins, tie goes to Allow
		"/private/public-notice-2.pdf": true,
		"/notice.pdf":                  false, // '$' anchored wildcard
		"/notice.pdf?x=1":              true,  // anchor means the path must end there
	}
	for path, want := range cases {
		if got := r.allowed(path); got != want {
			t.Errorf("allowed(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestMissingFileAllowsEverything(t *testing.T) {
	r := parseRobots("", "gleanbot")
	if !r.allowed("/anything/at/all") {
		t.Error("an empty robots.txt must not restrict anything")
	}
	var nilRules *robots
	if !nilRules.allowed("/x") {
		t.Error("a nil ruleset must allow")
	}
}
