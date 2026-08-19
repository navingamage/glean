// Package source describes where settlements come from and turns a listing page
// into a work list of candidate notice pages.
//
// The registry is data, not code, and it is deliberately loose. Every site in it
// will be redesigned at some point, and a scraper pinned to CSS selectors breaks
// silently the day that happens: it keeps returning zero results and nobody
// notices until a deadline has passed. So a source says only where to start and
// roughly what a case URL looks like, and everything after that is scoring on the
// page's own words. A redesign costs recall, not correctness.
package source

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/navingamage/glean/internal/model"
)

//go:embed sources.json
var builtin []byte

// Kind records what sort of site this is, which is mostly a statement about how
// far to trust a date found on it. An administrator publishes the deadline it
// will actually enforce; an aggregator is repeating what it read somewhere else.
type Kind string

const (
	KindAggregator    Kind = "aggregator"
	KindFirm          Kind = "firm"
	KindAdministrator Kind = "administrator"
	KindRegistry      Kind = "registry"
)

// Authority is how much weight a conflicting field from this source carries.
func (k Kind) Authority() int {
	switch k {
	case KindAdministrator:
		return 4
	case KindRegistry:
		return 3
	case KindFirm:
		return 2
	default:
		return 1
	}
}

type Source struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         Kind     `json:"kind"`
	Jurisdiction []string `json:"jurisdiction"`
	Listing      []string `json:"listing"`
	Follow       []string `json:"follow"`
	Exclude      []string `json:"exclude,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
	Notes        string   `json:"notes,omitempty"`

	follow  []*regexp.Regexp
	exclude []*regexp.Regexp
}

// Compile prepares the follow and exclude patterns. Load calls it; anything that
// builds a Source by hand has to call it too, or Wants silently matches nothing.
func (s *Source) Compile() error {
	s.follow, s.exclude = nil, nil
	for _, p := range s.Follow {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("source %s: follow %q: %w", s.ID, p, err)
		}
		s.follow = append(s.follow, re)
	}
	for _, p := range s.Exclude {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("source %s: exclude %q: %w", s.ID, p, err)
		}
		s.exclude = append(s.exclude, re)
	}
	return nil
}

// Wants reports whether a discovered URL is inside this source's territory.
// Patterns are matched against the identity form, so a registry entry can be
// written once as https without www and still catch a site that links to itself
// the other way round.
func (s *Source) Wants(rawURL string) bool {
	url := model.IdentityURL(rawURL)
	for _, re := range s.exclude {
		if re.MatchString(url) {
			return false
		}
	}
	if len(s.follow) == 0 {
		return false
	}
	for _, re := range s.follow {
		if re.MatchString(url) {
			return true
		}
	}
	return false
}

// Load reads the registry. With an empty path it returns the built-in list;
// otherwise the file replaces it, so a user can maintain their own without
// rebuilding.
func Load(path string) ([]*Source, error) {
	data := builtin
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("source: %w", err)
		}
		data = b
	}
	var out []*Source
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("source: parsing registry: %w", err)
	}
	seen := map[string]bool{}
	for _, s := range out {
		if s.ID == "" {
			return nil, fmt.Errorf("source: entry %q has no id", s.Name)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("source: duplicate id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Kind == "" {
			s.Kind = KindAggregator
		}
		if err := s.Compile(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Enabled filters out disabled sources and, when ids is non-empty, everything
// not named.
func Enabled(all []*Source, ids []string) []*Source {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []*Source
	for _, s := range all {
		if s.Disabled {
			continue
		}
		if len(want) > 0 && !want[s.ID] {
			continue
		}
		out = append(out, s)
	}
	return out
}
