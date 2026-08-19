package fetch

import (
	"strconv"
	"strings"
	"time"
)

// robots is a parsed robots.txt, reduced to the rules that apply to us.
//
// Politeness is not a nicety here. glean reads the same few dozen law firm and
// settlement administrator sites every day forever, from a residential address,
// and the cost of being wrong about that is a block that silently stops finding
// money. Respecting the file and the crawl delay is the cheapest possible
// insurance.
type robots struct {
	allow    []string
	disallow []string
	delay    time.Duration
}

// parseRobots reads a robots.txt and keeps the group that applies to agent,
// falling back to the wildcard group. Where both exist the specific group wins
// outright, which is what the standard says and what site owners expect.
func parseRobots(body, agent string) *robots {
	agent = strings.ToLower(agent)

	type group struct {
		names []string
		r     robots
	}
	var groups []*group
	var cur *group
	inAgents := false

	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "user-agent":
			// Consecutive User-agent lines share one group of rules.
			if cur == nil || !inAgents {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.names = append(cur.names, strings.ToLower(value))
			inAgents = true
			continue
		case "disallow":
			if cur != nil && value != "" {
				cur.r.disallow = append(cur.r.disallow, value)
			}
		case "allow":
			if cur != nil && value != "" {
				cur.r.allow = append(cur.r.allow, value)
			}
		case "crawl-delay":
			if cur != nil {
				if f, err := strconv.ParseFloat(value, 64); err == nil && f > 0 {
					cur.r.delay = time.Duration(f * float64(time.Second))
				}
			}
		}
		inAgents = false
	}

	var wildcard *robots
	for _, g := range groups {
		for _, name := range g.names {
			if name == "*" && wildcard == nil {
				r := g.r
				wildcard = &r
			}
			if name != "*" && strings.Contains(agent, name) {
				r := g.r
				return &r
			}
		}
	}
	if wildcard != nil {
		return wildcard
	}
	return &robots{}
}

// allowed applies the longest-match rule: the most specific pattern wins, and a
// tie goes to Allow.
func (r *robots) allowed(path string) bool {
	if r == nil {
		return true
	}
	best, allow := -1, true
	for _, p := range r.disallow {
		if n := matchLen(p, path); n > best {
			best, allow = n, false
		}
	}
	for _, p := range r.allow {
		if n := matchLen(p, path); n >= best {
			best, allow = n, true
		}
	}
	return allow
}

// matchLen returns the length of pattern p if it matches path, else -1.
// Patterns may contain '*' wildcards and a trailing '$' anchor.
func matchLen(p, path string) int {
	anchored := strings.HasSuffix(p, "$")
	pat := strings.TrimSuffix(p, "$")

	parts := strings.Split(pat, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		var idx int
		if i == 0 {
			if !strings.HasPrefix(path[pos:], part) {
				return -1
			}
			idx = 0
		} else {
			idx = strings.Index(path[pos:], part)
			if idx < 0 {
				return -1
			}
		}
		pos += idx + len(part)
	}
	if anchored && pos != len(path) {
		return -1
	}
	return len(pat)
}
