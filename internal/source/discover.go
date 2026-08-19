package source

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/navingamage/glean/internal/fetch"
	"github.com/navingamage/glean/internal/htmlx"
	"github.com/navingamage/glean/internal/model"
)

// Candidate is a page that might describe a settlement.
type Candidate struct {
	URL      string
	Title    string
	SourceID string
	Score    int
}

// signals are words whose presence in a link or a URL suggests a case page
// rather than a partner bio or a privacy policy. Weights are rough and only
// order the queue; nothing is rejected on this basis alone, because a source
// whose follow pattern already matched has earned the benefit of the doubt.
var signals = map[string]int{
	"class action": 6, "class-action": 6, "action collective": 6,
	"settlement": 6, "reglement": 4, "settlements": 6,
	"claim": 5, "claims": 5, "reclamation": 4,
	"deadline": 4, "notice": 3, "avis": 2,
	"opt out": 3, "opt-out": 3, "certification": 3, "certified": 3,
	"lawsuit": 3, "compensation": 3, "eligible": 3, "eligibility": 3,
	"proposed": 1, "court": 1, "case": 1,
}

// noise marks links that are structurally part of a website rather than part of
// its content. These are subtracted, not excluded, so that a genuinely relevant
// page filed under an unfortunate URL still gets through.
var noise = map[string]int{
	"privacy": 5, "terms": 5, "cookie": 5, "careers": 4, "contact": 3,
	"subscribe": 3, "newsletter": 3, "login": 4, "sign-in": 4, "author": 3,
	"category": 2, "tag": 2, "search": 3, "feed": 3, "about-us": 3,
}

// RawLinks returns every link on a source's listing pages with its relevance
// score, ignoring the follow patterns. It exists for `glean sources check
// -links`, which is how you work out what a redesigned site's URLs now look like
// without opening a browser and reading HTML by hand.
func RawLinks(ctx context.Context, c *fetch.Client, listing string, limit int) ([]Candidate, error) {
	resp, err := c.Get(ctx, listing)
	if err != nil {
		return nil, err
	}
	if !resp.IsHTML() {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []Candidate
	for _, link := range htmlx.ExtractLinks(resp.Body, listing) {
		url := model.CanonicalURL(link.URL)
		if seen[model.IdentityURL(url)] {
			continue
		}
		seen[model.IdentityURL(url)] = true
		out = append(out, Candidate{URL: url, Title: link.Text, Score: scoreLink(url, link.Text)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].URL < out[j].URL
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Discover reads a source's listing pages and returns the case pages they point
// at, best first.
func Discover(ctx context.Context, c *fetch.Client, s *Source, limit int) ([]Candidate, []error) {
	var errs []error
	best := map[string]Candidate{}

	for _, listing := range s.Listing {
		resp, err := c.Get(ctx, listing)
		if err != nil {
			if !errors.Is(err, fetch.ErrDisallowed) {
				errs = append(errs, err)
			}
			continue
		}
		if !resp.IsHTML() {
			continue
		}
		for _, link := range htmlx.ExtractLinks(resp.Body, listing) {
			url := model.CanonicalURL(link.URL)
			if !s.Wants(url) {
				continue
			}
			cand := Candidate{
				URL:      url,
				Title:    link.Text,
				SourceID: s.ID,
				Score:    scoreLink(url, link.Text),
			}
			// The same case is often linked twice on one page, once from a
			// thumbnail with no text and once from a headline. Keep the version
			// that tells us more, and key on identity so the two spellings of
			// one URL do not both survive.
			key := model.IdentityURL(url)
			if prev, ok := best[key]; !ok || cand.Score > prev.Score {
				best[key] = cand
			}
		}
	}

	out := make([]Candidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].URL < out[j].URL
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, errs
}

func scoreLink(url, text string) int {
	hay := strings.ToLower(url + " " + text)
	// URL path segments are written with hyphens; normalizing them to spaces
	// lets one keyword table serve both the href and the visible text.
	hay = strings.NewReplacer("-", " ", "_", " ", "/", " ").Replace(hay)

	score := 0
	for word, weight := range signals {
		if strings.Contains(hay, word) {
			score += weight
		}
	}
	for word, weight := range noise {
		if strings.Contains(hay, strings.ReplaceAll(word, "-", " ")) {
			score -= weight
		}
	}
	// A link with real words attached is more likely to be a headline than a
	// bare navigation chrome link.
	if n := len(strings.Fields(text)); n >= 4 {
		score += 2
	}
	return score
}
