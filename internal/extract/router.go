package extract

import (
	"fmt"
	"strings"

	"github.com/navingamage/glean/internal/model"
)

// Tier is how much accuracy a page is worth paying for.
//
// The two tiers exist because the pages in a scan are not equally consequential
// and the failure modes are not equally expensive. Reading a deadline off a
// notice is transcription: a small model does it as well as a large one, and
// when it gets it wrong the date is visibly absent rather than quietly wrong.
// Decomposing a class definition is not transcription. A model that reads
// "purchased bread at Loblaws, No Frills or Zehrs" as three requirements rather
// than three alternatives produces a settlement the reader is confidently told
// they do not qualify for, and nobody ever finds out.
//
// So the cheap tier handles the pages where being wrong is cheap, and the good
// model is spent on the ones where it is not.
type Tier int

const (
	Cheap Tier = iota
	Prime
)

func (t Tier) String() string {
	if t == Prime {
		return "prime"
	}
	return "cheap"
}

// DefaultCheapModel handles the bulk: pages that describe a proceeding nobody
// can claim from yet, and the second-hop claim portals that exist to state one
// deadline.
const DefaultCheapModel = "google/gemini-2.5-flash-lite"

// DefaultPrimeModel handles pages where a claim can actually be filed, which is
// where a misread class definition costs money.
const DefaultPrimeModel = "google/gemini-3.7-flash"

// cheapMaxChars truncates harder than the prime tier. Input tokens dominate the
// bill, and the fields the cheap tier is asked for, stage and deadline and
// amounts, are stated near the top of a notice or not at all. Sending a firm's
// entire case archive to find them is paying for scrollback.
const cheapMaxChars = 18_000

// Router holds one client per tier.
type Router struct {
	cheap *Client
	prime *Client
}

// NewRouter builds both tiers. Either model id may be empty for its default,
// and passing the same id for both collapses to one client, which is what
// `-model` does.
func NewRouter(apiKey, cheapModel, primeModel string) (*Router, error) {
	if cheapModel == "" {
		cheapModel = DefaultCheapModel
	}
	if primeModel == "" {
		primeModel = DefaultPrimeModel
	}

	cheap, err := NewClient(apiKey, cheapModel)
	if err != nil {
		return nil, err
	}
	cheap.MaxChars = cheapMaxChars

	if cheapModel == primeModel {
		return &Router{cheap: cheap, prime: cheap}, nil
	}
	prime, err := NewClient(apiKey, primeModel)
	if err != nil {
		return nil, err
	}
	// Both tiers share one Usage so a scan reports one bill.
	prime.Usage = cheap.Usage
	return &Router{cheap: cheap, prime: prime}, nil
}

func (r *Router) For(t Tier) *Client {
	if r == nil {
		return nil
	}
	if t == Prime {
		return r.prime
	}
	return r.cheap
}

// Usage is the combined spend across both tiers.
func (r *Router) Usage() *Usage {
	if r == nil {
		return nil
	}
	return r.cheap.Usage
}

func (r *Router) String() string {
	if r == nil {
		return "no model"
	}
	if r.cheap.Model == r.prime.Model {
		return r.prime.Model
	}
	return fmt.Sprintf("%s / %s", r.cheap.Model, r.prime.Model)
}

// Underspecified reports whether a cheap extraction came back too thin to act
// on, and should be redone by the capable model.
//
// The test is narrow on purpose. A settlement you cannot claim from yet is
// allowed to be vague. A settlement that is open for claims right now, with no
// class definition extracted at all, is useless: the matcher can only report
// that eligibility is unknown, which is the answer glean exists to improve on.
// Measured against a single-model run, this is what the cheap tier actually got
// wrong, so it is what the escalation checks for.
func Underspecified(s *model.Settlement) bool {
	return s != nil && s.Stage.Actionable() && len(s.Criteria) == 0
}

// claimable are phrases that only appear once a settlement has a live claims
// process. A page carrying them is one where the reader could act today, which
// is precisely when it matters whether the class definition was read correctly.
var claimable = []string{
	"file a claim", "submit a claim", "make a claim", "claim form",
	"claims administrator", "claims deadline", "claim deadline",
	"deadline to file", "claims process is open", "how to claim",
	"deposer une reclamation", "formulaire de reclamation",
	"opt out deadline", "opt-out deadline", "settlement approved",
	"court approved the settlement", "you may be entitled",
}

// Stakes picks a tier for a page.
//
// The rule is deliberately blunt: spend on pages where a claim can be filed, be
// cheap everywhere else. A proceeding that has only been commenced cannot be
// claimed from for years, and by the time it can be, the page will have changed
// and been re-read. Getting its class definition slightly wrong today costs
// nothing, so it is not worth paying to get right.
//
// authoritative marks the administrators and court registries, whose pages are
// the ones a deadline will actually be enforced from.
func Stakes(text string, authoritative bool) Tier {
	if authoritative {
		return Prime
	}
	low := strings.ToLower(text)
	hits := 0
	for _, phrase := range claimable {
		if strings.Contains(low, phrase) {
			hits++
			if hits >= 2 {
				return Prime
			}
		}
	}
	return Cheap
}
