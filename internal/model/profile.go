package model

import (
	"sort"
	"strings"
)

// Residence is a place the user lived, and when. Residency is the criterion that
// appears in nearly every Canadian class definition, so it gets its own field
// rather than living among the general holdings.
type Residence struct {
	Province string `json:"province"` // two-letter code, or "CA"
	Window   Window `json:"window"`
}

// Holding is one fact about the user that a class definition might turn on:
// an account they held, a store they shopped at, a car they drove.
//
// Negative holdings are as valuable as positive ones and are the reason the
// question flow terminates. "No, I have never banked with BMO" is recorded once
// and rules out every future BMO action without asking again.
type Holding struct {
	Kind     CriterionKind `json:"kind"`
	Entity   string        `json:"entity"`
	Label    string        `json:"label"`
	Window   Window        `json:"window"`
	Negative bool          `json:"negative,omitempty"`
	Note     string        `json:"note,omitempty"`
	AddedOn  Date          `json:"added_on"`
}

// Profile is everything glean knows about the person it is working for. It never
// leaves the machine: the matcher runs locally, and the only thing sent to an
// extraction model is the public settlement page, never the profile.
type Profile struct {
	Name       string      `json:"name,omitempty"`
	Email      string      `json:"email,omitempty"`
	Residences []Residence `json:"residences"`
	Holdings   []Holding   `json:"holdings"`

	// MinPayout and MaxEffort are the user's own filter. A claim worth eleven
	// dollars that wants proof of purchase from 2009 is a real settlement and a
	// bad use of an evening, and the ranking should be allowed to say so.
	MinPayout float64 `json:"min_payout"`
	MaxEffort int     `json:"max_effort_minutes"`

	UpdatedOn Date `json:"updated_on"`
}

// DefaultProfile is the starting point for someone who has answered nothing: a
// Canadian resident, with no other facts asserted. Every criterion beyond
// residency comes back Unknown, which is correct, and the question list is how
// the profile fills in.
func DefaultProfile() *Profile {
	return &Profile{
		Residences: []Residence{{Province: "CA"}},
		MinPayout:  5,
		MaxEffort:  60,
		UpdatedOn:  Today(),
	}
}

// LivedIn reports whether the user was resident in the given province at any
// point inside the window. "CA" on either side matches any province, since a
// nationwide class covers a resident of anywhere and a user who has only said
// "Canada" cannot be ruled out of a provincial class.
func (p *Profile) LivedIn(province string, w Window) bool {
	province = strings.ToUpper(province)
	for _, r := range p.Residences {
		code := strings.ToUpper(r.Province)
		if code != province && code != "CA" && province != "CA" {
			continue
		}
		if r.Window.Overlaps(w) {
			return true
		}
	}
	return false
}

// Find returns the holding matching a criterion, or nil. Matching is by
// normalized entity key against the criterion's own key and its aliases, so a
// profile that says "Loblaws" answers a criterion phrased as "No Frills".
func (p *Profile) Find(c Criterion) *Holding {
	keys := map[string]bool{NormalizeEntity(c.Entity): true}
	for _, a := range c.Aliases {
		keys[NormalizeEntity(a)] = true
	}
	for i := range p.Holdings {
		h := &p.Holdings[i]
		if h.Kind != c.Kind && h.Kind != KindOther && c.Kind != KindOther {
			continue
		}
		if keys[NormalizeEntity(h.Entity)] {
			return h
		}
	}
	return nil
}

// Record adds or updates a holding. A later answer supersedes an earlier one on
// the same entity and kind, because the usual reason to answer twice is that the
// first answer was wrong.
func (p *Profile) Record(h Holding) {
	h.Entity = NormalizeEntity(h.Entity)
	if h.Label == "" {
		h.Label = h.Entity
	}
	if h.AddedOn.Zero() {
		h.AddedOn = Today()
	}
	for i := range p.Holdings {
		if p.Holdings[i].Kind == h.Kind && NormalizeEntity(p.Holdings[i].Entity) == h.Entity {
			p.Holdings[i] = h
			p.UpdatedOn = Today()
			return
		}
	}
	p.Holdings = append(p.Holdings, h)
	p.UpdatedOn = Today()
}

// Sort orders residences and holdings so a written profile file has a stable
// diff between runs.
func (p *Profile) Sort() {
	sort.Slice(p.Residences, func(i, j int) bool {
		return p.Residences[i].Province < p.Residences[j].Province
	})
	sort.Slice(p.Holdings, func(i, j int) bool {
		if p.Holdings[i].Kind != p.Holdings[j].Kind {
			return p.Holdings[i].Kind < p.Holdings[j].Kind
		}
		return p.Holdings[i].Entity < p.Holdings[j].Entity
	})
}
