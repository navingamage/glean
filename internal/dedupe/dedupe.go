// Package dedupe collapses the same settlement found in more than one place.
//
// A settlement is identified by the URL it was read from. That is right for
// tracking one page over time and wrong the moment two sites describe one case:
// the BigSteelBox flood settlement appears on the aggregator that indexed it and
// on the administrator that runs it, and the Canadian Armed Forces file appears
// on three. Showing those separately inflates the list and invites someone to
// file the same claim twice.
//
// Merging is deliberately conservative, because the two errors are not
// symmetrical. Two settlements wrongly merged hide one of them completely, which
// is the failure that costs money. Two left separate are only untidy.
package dedupe

import (
	"net/url"
	"sort"
	"strings"

	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/source"
)

// Thresholds on title similarity, chosen by how much corroboration comes with
// it. A shared claim portal is close to proof on its own, so the titles only
// have to be plausibly related; a shared deadline is good evidence; with neither,
// the titles have to be nearly the same thing said twice.
const (
	withPortal   = 0.35
	withDeadline = 0.45
	titleAlone   = 0.70
)

// Group is one settlement and the other records describing the same case.
type Group struct {
	Primary *model.Settlement
	Also    []*model.Settlement
}

// Sources lists every source that reported this case, primary first.
func (g Group) Sources() []string {
	seen := map[string]bool{g.Primary.SourceID: true}
	out := []string{g.Primary.SourceID}
	for _, s := range g.Also {
		if !seen[s.SourceID] {
			seen[s.SourceID] = true
			out = append(out, s.SourceID)
		}
	}
	return out
}

type record struct {
	s      *model.Settlement
	words  map[string]bool
	grams  map[string]bool
	portal string
}

// Collapse groups a list of settlements, preserving the order of the primaries
// so a caller that has already ranked its list keeps that ranking.
func Collapse(settlements []*model.Settlement, sources []*source.Source) []Group {
	authority := map[string]int{}
	for _, s := range sources {
		authority[s.ID] = s.Kind.Authority()
	}

	recs := make([]record, 0, len(settlements))
	for _, s := range settlements {
		recs = append(recs, record{
			s:      s,
			words:  words(s.Title),
			grams:  trigrams(s.Title),
			portal: portalHost(s),
		})
	}

	taken := make([]bool, len(recs))
	var groups []Group
	for i := range recs {
		if taken[i] {
			continue
		}
		taken[i] = true
		g := Group{Primary: recs[i].s}
		for j := i + 1; j < len(recs); j++ {
			if !taken[j] && same(recs[i], recs[j]) {
				taken[j] = true
				g.Also = append(g.Also, recs[j].s)
			}
		}
		groups = append(groups, g)
	}

	// The record to show is the one from the most authoritative source, because
	// an administrator publishes the deadline that will actually be enforced.
	// Ties go to whichever record knows more.
	for i := range groups {
		if len(groups[i].Also) == 0 {
			continue
		}
		all := append([]*model.Settlement{groups[i].Primary}, groups[i].Also...)
		sort.SliceStable(all, func(a, b int) bool {
			if x, y := authority[all[a].SourceID], authority[all[b].SourceID]; x != y {
				return x > y
			}
			if x, y := filled(all[a]), filled(all[b]); x != y {
				return x > y
			}
			return all[a].Confidence > all[b].Confidence
		})
		groups[i].Primary, groups[i].Also = all[0], all[1:]
	}
	return groups
}

// same decides whether two records describe one proceeding.
func same(a, b record) bool {
	// A stated disagreement is proof of difference, and is checked before any
	// similarity score. "Tobacco Claims Canada - Quebec Class Action Plan" and
	// "Tobacco Claims Canada - Pan-Canadian Claimants Plan" share most of their
	// words and are two different proceedings with two different deadlines.
	samePortal := a.portal != "" && a.portal == b.portal
	if a.portal != "" && b.portal != "" && !samePortal {
		return false
	}
	da, db := a.s.ClaimDeadline, b.s.ClaimDeadline
	sameDeadline := !da.Zero() && da == db
	if !da.Zero() && !db.Zero() && !sameDeadline {
		return false
	}

	need := titleAlone
	switch {
	case samePortal:
		need = withPortal
	case sameDeadline:
		need = withDeadline
	}

	// Titles are compared two ways because sites disagree about spacing:
	// "Big Steel Box" and "BigSteelBox" share no words at all and nearly all
	// their character trigrams.
	score := jaccard(a.words, b.words)
	if g := jaccard(a.grams, b.grams); g > score {
		score = g
	}
	return score >= need
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for k := range a {
		if b[k] {
			shared++
		}
	}
	union := len(a) + len(b) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// stop words carry no distinguishing information here: every record in the
// store is a Canadian class action settlement, so those words are noise.
var stop = map[string]bool{
	"the": true, "and": true, "of": true, "in": true, "for": true, "to": true,
	"class": true, "action": true, "actions": true, "settlement": true,
	"settlements": true, "lawsuit": true, "canada": true, "canadian": true,
	"claim": true, "claims": true, "proposed": true, "national": true,
	"proceeding": true, "recours": true, "collectif": true,
}

func words(title string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(title), notAlnum) {
		if len(w) >= 3 && !stop[w] {
			out[w] = true
		}
	}
	return out
}

func trigrams(title string) map[string]bool {
	var b strings.Builder
	for _, w := range strings.FieldsFunc(strings.ToLower(title), notAlnum) {
		if !stop[w] {
			b.WriteString(w)
		}
	}
	s := b.String()
	out := map[string]bool{}
	for i := 0; i+3 <= len(s); i++ {
		out[s[i:i+3]] = true
	}
	return out
}

func notAlnum(r rune) bool {
	return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9')
}

func portalHost(s *model.Settlement) string {
	if s.ClaimURL == "" {
		return ""
	}
	u, err := url.Parse(s.ClaimURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(u.Host, "www."))
}

// filled counts how much a record knows, used to break authority ties.
func filled(s *model.Settlement) int {
	n := len(s.Criteria)
	for _, known := range []bool{
		!s.ClaimDeadline.Zero(), s.ClaimURL != "", s.PayoutHigh > 0,
		s.SettlementFund > 0, s.ClassText != "", s.Administrator != "",
	} {
		if known {
			n++
		}
	}
	return n
}
