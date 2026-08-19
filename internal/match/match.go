// Package match decides whether a person belongs to a settlement class, and how
// much it is worth finding out.
//
// The interesting output is not the yes/no. It is the list of questions that
// would turn a maybe into a yes, ordered by how many dollars each answer
// unlocks. A person who has answered nothing is Unknown against almost every
// class in the country; the job here is to work out which six questions to ask
// so that stops being true.
package match

import (
	"fmt"
	"sort"
	"strings"

	"github.com/navingamage/glean/internal/logic"
	"github.com/navingamage/glean/internal/model"
)

// defaultPrevalence is used when the extractor gave no estimate. It is
// deliberately middling: it keeps an unpriced unknown from either dominating the
// ranking or vanishing from it.
const defaultPrevalence = 0.3

// feeFraction is the share of a settlement fund that reaches claimants after
// counsel fees, administration and honoraria. Canadian approvals commonly land
// near a third for fees on contested files and rather less on straightforward
// ones; 0.6 is a conservative round number used only when a notice states a
// fund but no per-claimant figure.
const feeFraction = 0.6

// CriterionResult is one clause of a class definition, evaluated.
type CriterionResult struct {
	Criterion model.Criterion `json:"criterion"`
	Value     logic.Tri       `json:"-"`
	ValueStr  string          `json:"value"`
	Reason    string          `json:"reason"`
}

// GroupResult is a set of alternative criteria, OR'd.
type GroupResult struct {
	Criteria []CriterionResult `json:"criteria"`
	Value    logic.Tri         `json:"-"`
	ValueStr string            `json:"value"`
}

// Question is one thing worth asking the user. ID is stable across settlements
// and across runs, so answering it once retires it everywhere.
type Question struct {
	ID     string              `json:"id"`
	Kind   model.CriterionKind `json:"kind"`
	Entity string              `json:"entity"`
	Label  string              `json:"label"`
	Window model.Window        `json:"window"`
	Ask    string              `json:"ask"`

	// Unlocks lists the settlements this question is blocking, and Worth is the
	// expected dollars across them that a yes would move from "maybe" to "claim".
	Unlocks []string `json:"unlocks"`
	Worth   float64  `json:"worth"`
}

// Assessment is a settlement judged against one profile.
type Assessment struct {
	SettlementID string        `json:"settlement_id"`
	Verdict      logic.Tri     `json:"-"`
	VerdictStr   string        `json:"verdict"`
	Groups       []GroupResult `json:"groups"`
	Questions    []Question    `json:"questions"`
	// Note explains a verdict that the criteria list does not account for.
	Note string `json:"note,omitempty"`

	// Probability is P(the user is in the class), given what is known and a
	// prevalence prior over what is not.
	Probability float64 `json:"probability"`
	// Payout is the per-claimant estimate in Canadian dollars.
	Payout           float64 `json:"payout"`
	ExpectedValue    float64 `json:"expected_value"`
	DollarsPerMinute float64 `json:"dollars_per_minute"`
	DaysLeft         int     `json:"days_left"`
	Actionable       bool    `json:"actionable"`
}

// unreadClassPrior is P(you are in the class) for a settlement whose class
// definition could not be read at all. It is a guess, and a low one: most
// classes are narrower than "anyone", so a page glean could not parse is more
// likely than not to describe a class the reader is outside.
const unreadClassPrior = 0.2

// Assess evaluates one settlement against one profile.
func Assess(s *model.Settlement, p *model.Profile) Assessment {
	a := Assessment{
		SettlementID: s.ID,
		DaysLeft:     s.DaysLeft(),
		Actionable:   s.Stage.Actionable() && !s.Expired(),
	}

	// A settlement with no criteria is one whose class definition was never
	// extracted, which happens on every page when there is no API key and on
	// some pages even when there is. The empty conjunction is True as a matter
	// of logic, and reporting that as "you are in the class" would be a lie: the
	// honest reading of "we could not find out who is in this class" is that we
	// do not know.
	if len(s.Criteria) == 0 {
		a.Verdict = logic.Unknown
		a.VerdictStr = a.Verdict.String()
		a.Note = "the class definition on this page could not be read, so eligibility is unknown"
		a.Probability = unreadClassPrior
		a.Payout = EstimatePayout(s)
		a.ExpectedValue = a.Payout * a.Probability
		effort := s.EffortMinutes
		if effort <= 0 {
			effort = 15
		}
		a.DollarsPerMinute = a.ExpectedValue / float64(effort)
		return a
	}

	groupValues := make([]logic.Tri, 0, len(s.Criteria))
	prob := 1.0
	seen := map[string]bool{}

	for _, group := range s.Groups() {
		gr := GroupResult{}
		vals := make([]logic.Tri, 0, len(group))
		// missChance is P(no member of this group turns out to be satisfied),
		// accumulated over the members that are still open.
		missChance := 1.0
		var pending []Question

		for _, c := range group {
			v, reason := evaluate(c, p)
			gr.Criteria = append(gr.Criteria, CriterionResult{
				Criterion: c, Value: v, ValueStr: v.String(), Reason: reason,
			})
			vals = append(vals, v)
			if v == logic.Unknown {
				missChance *= 1 - prevalence(c)
				pending = append(pending, questionFor(c))
			}
		}

		gr.Value = logic.OrAll(vals)
		gr.ValueStr = gr.Value.String()
		a.Groups = append(a.Groups, gr)
		groupValues = append(groupValues, gr.Value)

		// Questions are held back until the group's value is known, because one
		// satisfied alternative settles the whole group. Asking "did you also
		// shop at Sobeys?" of someone who has already said Loblaws is a question
		// whose answer cannot change anything.
		if gr.Value == logic.Unknown {
			prob *= 1 - missChance
			for _, q := range pending {
				if !seen[q.ID] {
					seen[q.ID] = true
					a.Questions = append(a.Questions, q)
				}
			}
		}
		if gr.Value == logic.False {
			prob = 0
		}
	}

	a.Verdict = logic.AndAll(groupValues)
	a.VerdictStr = a.Verdict.String()
	if a.Verdict == logic.False {
		prob = 0
		a.Questions = nil // no point asking about a settlement already ruled out
	}
	a.Probability = clamp(prob)

	a.Payout = EstimatePayout(s)
	a.ExpectedValue = a.Payout * a.Probability
	effort := s.EffortMinutes
	if effort <= 0 {
		effort = 15
	}
	a.DollarsPerMinute = a.ExpectedValue / float64(effort)

	for i := range a.Questions {
		a.Questions[i].Unlocks = []string{s.ID}
		a.Questions[i].Worth = a.Payout
	}
	return a
}

// evaluate resolves a single criterion against the profile.
func evaluate(c model.Criterion, p *model.Profile) (logic.Tri, string) {
	if c.Kind == model.KindResidency {
		return residency(c, p)
	}

	h := p.Find(c)
	if h == nil {
		return logic.Unknown, "no answer on file for " + display(c)
	}
	if h.Negative {
		return logic.False, fmt.Sprintf("you said no to %s", h.Label)
	}
	// A holding with no stated window is an assertion about the person, not about
	// a period, so it cannot contradict a class period.
	if h.Window.Zero() || c.Window.Zero() {
		return logic.True, fmt.Sprintf("%s is on your profile", h.Label)
	}
	if h.Window.Overlaps(c.Window) {
		return logic.True, fmt.Sprintf("%s, %s, overlaps the class period %s",
			h.Label, h.Window, c.Window)
	}
	return logic.False, fmt.Sprintf("%s, %s, falls outside the class period %s",
		h.Label, h.Window, c.Window)
}

// residency is separate because "Canada" is a wildcard in one direction only.
// A nationwide class covers a resident of any province, but a profile that says
// no more than "Canada" cannot answer a class restricted to Saskatchewan.
func residency(c model.Criterion, p *model.Profile) (logic.Tri, string) {
	want := model.NormalizeProvince(c.Entity)
	if want == "" {
		want = model.NormalizeProvince(c.Label)
	}
	if want == "" {
		return logic.Unknown, "could not read a province out of " + display(c)
	}

	vague := false
	for _, r := range p.Residences {
		code := strings.ToUpper(r.Province)
		if !r.Window.Overlaps(c.Window) {
			continue
		}
		if code == want {
			return logic.True, fmt.Sprintf("you lived in %s", model.Provinces[want])
		}
		if want == "CA" {
			return logic.True, "you are resident in Canada"
		}
		if code == "CA" {
			vague = true
		}
	}
	if vague {
		return logic.Unknown, fmt.Sprintf("your profile says Canada but this class is %s only",
			model.Provinces[want])
	}
	return logic.False, fmt.Sprintf("you have not lived in %s", model.Provinces[want])
}

func questionFor(c model.Criterion) Question {
	q := Question{
		ID:     string(c.Kind) + ":" + model.NormalizeEntity(c.Entity),
		Kind:   c.Kind,
		Entity: model.NormalizeEntity(c.Entity),
		Label:  display(c),
		Window: c.Window,
	}
	period := ""
	if !c.Window.Zero() {
		period = " between " + c.Window.String()
	}
	switch c.Kind {
	case model.KindResidency:
		q.Ask = fmt.Sprintf("Did you live in %s%s?", q.Label, period)
	case model.KindPurchase:
		q.Ask = fmt.Sprintf("Did you buy %s%s?", q.Label, period)
	case model.KindAccount:
		q.Ask = fmt.Sprintf("Did you hold an account with %s%s?", q.Label, period)
	case model.KindService:
		q.Ask = fmt.Sprintf("Did you use %s%s?", q.Label, period)
	case model.KindEmployment:
		q.Ask = fmt.Sprintf("Did you work for %s%s?", q.Label, period)
	case model.KindVehicle:
		q.Ask = fmt.Sprintf("Did you own or lease a %s%s?", q.Label, period)
	case model.KindDevice:
		q.Ask = fmt.Sprintf("Did you own a %s%s?", q.Label, period)
	case model.KindMedical:
		q.Ask = fmt.Sprintf("Were you prescribed or treated with %s%s?", q.Label, period)
	default:
		q.Ask = fmt.Sprintf("Does this apply to you: %s%s?", q.Label, period)
	}
	return q
}

func display(c model.Criterion) string {
	if c.Label != "" {
		return c.Label
	}
	if c.Entity != "" {
		return c.Entity
	}
	return c.Text
}

func prevalence(c model.Criterion) float64 {
	if c.Prevalence <= 0 || c.Prevalence > 1 {
		return defaultPrevalence
	}
	return c.Prevalence
}

func clamp(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// EstimatePayout returns what one claimant can expect, in Canadian dollars.
//
// The order of preference is the order of trustworthiness: a stated per-claimant
// range beats a stated minimum, which beats dividing the fund by a guess at the
// class size. When a notice gives none of the three the answer is zero, and zero
// means "unknown", which the ranking treats as unranked rather than worthless.
func EstimatePayout(s *model.Settlement) float64 {
	switch {
	case s.PayoutLow > 0 && s.PayoutHigh > 0:
		return (s.PayoutLow + s.PayoutHigh) / 2
	case s.PayoutHigh > 0:
		return s.PayoutHigh / 2
	case s.PayoutLow > 0:
		return s.PayoutLow
	case s.SettlementFund > 0 && s.EstimatedClassSize > 0:
		return s.SettlementFund * feeFraction / float64(s.EstimatedClassSize)
	}
	return 0
}

// Rank orders assessments for a person deciding what to do this evening.
//
// Sorting purely by expected value buries a forty dollar claim that closes on
// Friday underneath a two hundred dollar claim that closes next spring, and the
// forty dollars is the one at risk. So urgency is a multiplier on value rather
// than a separate sort key, and an already-ruled-out or expired settlement is
// pushed to the bottom instead of being hidden, because seeing why something was
// excluded is how you catch a wrong answer in your own profile.
func Rank(as []Assessment, byID map[string]*model.Settlement) {
	sort.SliceStable(as, func(i, j int) bool {
		si, sj := score(as[i], byID[as[i].SettlementID]), score(as[j], byID[as[j].SettlementID])
		if si != sj {
			return si > sj
		}
		return as[i].DaysLeft < as[j].DaysLeft
	})
}

func score(a Assessment, s *model.Settlement) float64 {
	if s == nil || s.Expired() || a.Verdict == logic.False {
		return -1
	}
	v := a.ExpectedValue
	if v == 0 {
		// An unpriced settlement still deserves a place in the list, below
		// anything with a real number on it but above the ruled-out.
		v = 0.01
	}
	if !a.Actionable {
		v *= 0.25 // not yet claimable: worth watching, not worth an evening
	}
	switch d := a.DaysLeft; {
	case d <= 7:
		v *= 2.5
	case d <= 30:
		v *= 1.6
	case d <= 90:
		v *= 1.15
	}
	return v
}

// Questions aggregates the open questions across many assessments, merging by
// ID and summing what each one unlocks. The result is the shortest interview
// that turns the largest number of maybes into claims.
func Questions(as []Assessment) []Question {
	merged := map[string]*Question{}
	for _, a := range as {
		if a.Verdict == logic.False {
			continue
		}
		for _, q := range a.Questions {
			m, ok := merged[q.ID]
			if !ok {
				c := q
				merged[q.ID] = &c
				continue
			}
			m.Unlocks = append(m.Unlocks, q.Unlocks...)
			m.Worth += q.Worth
			if m.Window.From.Zero() || (!q.Window.From.Zero() && q.Window.From.Before(m.Window.From)) {
				m.Window.From = q.Window.From
			}
			if m.Window.Until.Zero() || q.Window.Until.After(m.Window.Until) {
				m.Window.Until = q.Window.Until
			}
		}
	}
	out := make([]Question, 0, len(merged))
	for _, q := range merged {
		out = append(out, *q)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Worth != out[j].Worth {
			return out[i].Worth > out[j].Worth
		}
		return out[i].ID < out[j].ID
	})
	return out
}
