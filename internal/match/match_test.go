package match

import (
	"testing"

	"github.com/navingamage/glean/internal/logic"
	"github.com/navingamage/glean/internal/model"
)

func d(s string) model.Date {
	v, err := model.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func win(a, b string) model.Window { return model.Window{From: d(a), Until: d(b)} }

// bread is the Loblaws packaged-bread class, which is the case that motivated
// most of this package: nationwide, a twenty-year class period, and a definition
// written in banner names rather than in the name of the parent company.
func bread() *model.Settlement {
	return &model.Settlement{
		ID:            "bread",
		Title:         "Packaged bread price-fixing",
		Stage:         model.StageClaimsOpen,
		ClaimDeadline: d("2027-06-30"),
		PayoutLow:     25, PayoutHigh: 25,
		EffortMinutes: 10,
		Criteria: []model.Criterion{
			{Kind: model.KindResidency, Entity: "CA", Label: "Canada", Prevalence: 1},
			{Kind: model.KindPurchase, Entity: "no-frills", Label: "No Frills", Group: "banner",
				Window: win("2001-01-01", "2021-12-31"), Prevalence: 0.5},
			{Kind: model.KindPurchase, Entity: "sobeys", Label: "Sobeys", Group: "banner",
				Window: win("2001-01-01", "2021-12-31"), Prevalence: 0.5},
		},
	}
}

func TestBannerAliasSatisfiesClass(t *testing.T) {
	p := model.DefaultProfile()
	p.Record(model.Holding{Kind: model.KindPurchase, Entity: "Loblaws", Label: "Loblaws"})

	a := Assess(bread(), p)
	if a.Verdict != logic.True {
		t.Fatalf("verdict = %v, want yes (reasons: %v)", a.Verdict, reasons(a))
	}
	if len(a.Questions) != 0 {
		t.Errorf("an eligible settlement should ask nothing, got %d questions", len(a.Questions))
	}
	if a.ExpectedValue != 25 {
		t.Errorf("expected value = %v, want 25", a.ExpectedValue)
	}
}

func TestAlternativeGroupNeedsOnlyOne(t *testing.T) {
	// Saying no to one banner must not rule the class out while the other banner
	// in the same group is still open. This is the bug that a plain conjunction
	// over criteria would produce.
	p := model.DefaultProfile()
	p.Record(model.Holding{Kind: model.KindPurchase, Entity: "sobeys", Label: "Sobeys", Negative: true})

	a := Assess(bread(), p)
	if a.Verdict != logic.Unknown {
		t.Fatalf("verdict = %v, want unknown (reasons: %v)", a.Verdict, reasons(a))
	}
	if len(a.Questions) != 1 || a.Questions[0].Entity != "loblaw" {
		t.Fatalf("want one open question about the other banner, got %+v", a.Questions)
	}
}

func TestNegativeAnswerRulesOutAndStopsAsking(t *testing.T) {
	p := model.DefaultProfile()
	p.Record(model.Holding{Kind: model.KindPurchase, Entity: "loblaw", Negative: true})
	p.Record(model.Holding{Kind: model.KindPurchase, Entity: "sobeys", Negative: true})

	a := Assess(bread(), p)
	if a.Verdict != logic.False {
		t.Fatalf("verdict = %v, want no", a.Verdict)
	}
	if len(a.Questions) != 0 {
		t.Errorf("a ruled-out settlement must not generate questions, got %v", a.Questions)
	}
	if a.Probability != 0 || a.ExpectedValue != 0 {
		t.Errorf("ruled out but priced at p=%v ev=%v", a.Probability, a.ExpectedValue)
	}
}

func TestUnknownProfileGeneratesPricedQuestion(t *testing.T) {
	a := Assess(bread(), model.DefaultProfile())
	if a.Verdict != logic.Unknown {
		t.Fatalf("verdict = %v, want unknown", a.Verdict)
	}
	if len(a.Questions) != 2 {
		t.Fatalf("want a question per unanswered banner, got %d", len(a.Questions))
	}
	// Two independent 0.5 chances of having shopped at one of the banners.
	if got := a.Probability; got < 0.74 || got > 0.76 {
		t.Errorf("probability = %v, want ~0.75", got)
	}
	if got := a.Questions[0].Ask; got != "Did you buy No Frills between 2001-01-01 and 2021-12-31?" {
		t.Errorf("question reads %q", got)
	}
}

func TestProvinceIsAWildcardInOneDirectionOnly(t *testing.T) {
	ontarioOnly := &model.Settlement{
		ID: "on", Stage: model.StageClaimsOpen, PayoutLow: 100, PayoutHigh: 100,
		Criteria: []model.Criterion{{Kind: model.KindResidency, Entity: "ON", Label: "Ontario"}},
	}

	// A profile that says only "Canada" cannot answer a province-restricted class.
	if a := Assess(ontarioOnly, model.DefaultProfile()); a.Verdict != logic.Unknown {
		t.Errorf("Canada-only profile against an Ontario class = %v, want unknown", a.Verdict)
	}

	alberta := model.DefaultProfile()
	alberta.Residences = []model.Residence{{Province: "AB"}}
	if a := Assess(ontarioOnly, alberta); a.Verdict != logic.False {
		t.Errorf("Alberta profile against an Ontario class = %v, want no", a.Verdict)
	}

	// The other direction: a provincial resident is inside a nationwide class.
	national := &model.Settlement{
		ID: "ca", Stage: model.StageClaimsOpen,
		Criteria: []model.Criterion{{Kind: model.KindResidency, Entity: "CA", Label: "Canada"}},
	}
	if a := Assess(national, alberta); a.Verdict != logic.True {
		t.Errorf("Alberta profile against a nationwide class = %v, want yes", a.Verdict)
	}
}

func TestClassPeriodExcludesNonOverlappingHolding(t *testing.T) {
	s := &model.Settlement{
		ID: "bank", Stage: model.StageClaimsOpen, PayoutLow: 50, PayoutHigh: 50,
		Criteria: []model.Criterion{{
			Kind: model.KindAccount, Entity: "cibc", Label: "CIBC",
			Window: win("2018-01-01", "2022-12-31"),
		}},
	}
	p := model.DefaultProfile()
	p.Record(model.Holding{Kind: model.KindAccount, Entity: "CIBC", Label: "CIBC",
		Window: win("2005-01-01", "2010-01-01")})
	if a := Assess(s, p); a.Verdict != logic.False {
		t.Errorf("account closed before the class period = %v, want no", a.Verdict)
	}

	// A holding with no stated window is a fact about the person, not a claim
	// about a period, so it must not be read as contradicting the class period.
	q := model.DefaultProfile()
	q.Record(model.Holding{Kind: model.KindAccount, Entity: "CIBC", Label: "CIBC"})
	if a := Assess(s, q); a.Verdict != logic.True {
		t.Errorf("undated holding = %v, want yes", a.Verdict)
	}
}

func TestPayoutEstimateFallsBackToFund(t *testing.T) {
	s := &model.Settlement{SettlementFund: 10_000_000, EstimatedClassSize: 100_000}
	if got := EstimatePayout(s); got != 60 {
		t.Errorf("fund-derived payout = %v, want 60", got)
	}
	if got := EstimatePayout(&model.Settlement{SettlementFund: 10_000_000}); got != 0 {
		t.Errorf("a fund with no class size should price at 0 (unknown), got %v", got)
	}
}

func TestRankPrefersTheClaimThatClosesFirst(t *testing.T) {
	soon := &model.Settlement{ID: "soon", Stage: model.StageClaimsOpen,
		ClaimDeadline: model.DateOf(model.Today().Time().AddDate(0, 0, 5)),
		PayoutLow:     40, PayoutHigh: 40}
	later := &model.Settlement{ID: "later", Stage: model.StageClaimsOpen,
		ClaimDeadline: model.DateOf(model.Today().Time().AddDate(1, 0, 0)),
		PayoutLow:     70, PayoutHigh: 70}
	expired := &model.Settlement{ID: "expired", Stage: model.StageClaimsOpen,
		ClaimDeadline: d("2020-01-01"), PayoutLow: 500, PayoutHigh: 500}

	byID := map[string]*model.Settlement{"soon": soon, "later": later, "expired": expired}
	p := model.DefaultProfile()
	as := []Assessment{Assess(later, p), Assess(expired, p), Assess(soon, p)}
	Rank(as, byID)

	got := []string{as[0].SettlementID, as[1].SettlementID, as[2].SettlementID}
	want := []string{"soon", "later", "expired"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rank = %v, want %v", got, want)
		}
	}
}

func TestQuestionsReadAsEnglish(t *testing.T) {
	// A half-open class period is the common case, and "between up to
	// 2012-03-12" is what naive concatenation produces.
	mk := func(w model.Window) string {
		s := &model.Settlement{ID: "x", Stage: model.StageClaimsOpen,
			Criteria: []model.Criterion{{Kind: model.KindMedical, Entity: "drug", Label: "Drug", Window: w}}}
		return Assess(s, model.DefaultProfile()).Questions[0].Ask
	}
	cases := map[string]string{
		mk(model.Window{}):                       "Were you prescribed or treated with Drug?",
		mk(model.Window{Until: d("2012-03-12")}): "Were you prescribed or treated with Drug on or before 2012-03-12?",
		mk(model.Window{From: d("2012-03-12")}):  "Were you prescribed or treated with Drug at any time since 2012-03-12?",
		mk(win("2001-01-01", "2021-12-31")):      "Were you prescribed or treated with Drug between 2001-01-01 and 2021-12-31?",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("question reads %q, want %q", got, want)
		}
	}
}

func TestQuestionsMergeAcrossSettlements(t *testing.T) {
	// One question about CIBC that unblocks two settlements should be asked once
	// and priced at the sum of what it unlocks.
	mk := func(id string, payout float64) *model.Settlement {
		return &model.Settlement{ID: id, Stage: model.StageClaimsOpen,
			PayoutLow: payout, PayoutHigh: payout,
			Criteria: []model.Criterion{{Kind: model.KindAccount, Entity: "cibc", Label: "CIBC"}}}
	}
	p := model.DefaultProfile()
	as := []Assessment{Assess(mk("a", 30), p), Assess(mk("b", 70), p)}

	qs := Questions(as)
	if len(qs) != 1 {
		t.Fatalf("want one merged question, got %d", len(qs))
	}
	// Worth is probability-weighted, not the headline payout. One unanswered
	// criterion at the default prevalence of 0.3 puts these two settlements at
	// $9 and $21. Pricing the question at the full $100 would rank a question
	// about a rare drug class above one about a supermarket half the country
	// shops at, and would let a summary line total ninety-six questions into
	// half a million dollars nobody is owed.
	if got := qs[0].Worth; got < 29.9 || got > 30.1 {
		t.Errorf("merged worth = %v, want about 30", got)
	}
	if len(qs[0].Unlocks) != 2 {
		t.Errorf("merged question unlocks %v", qs[0].Unlocks)
	}

	// The headline figure counts each settlement once, however many of its
	// criteria are still open.
	byID := map[string]*model.Settlement{"a": mk("a", 30), "b": mk("b", 70)}
	if got := TotalAtStake(qs, byID, as); got < 29.9 || got > 30.1 {
		t.Errorf("total at stake = %v, want about 30", got)
	}
}

func reasons(a Assessment) []string {
	var out []string
	for _, g := range a.Groups {
		for _, c := range g.Criteria {
			out = append(out, c.ValueStr+": "+c.Reason)
		}
	}
	return out
}

func TestNoCriteriaIsUnknownNotEligible(t *testing.T) {
	// Without an extraction model there are no criteria on any settlement. The
	// empty conjunction is True, and reporting that to a user as "you are in the
	// class" would be the single most damaging thing this program could say.
	s := &model.Settlement{
		ID: "bare", Stage: model.StageClaimsOpen,
		PayoutLow: 100, PayoutHigh: 100,
	}
	a := Assess(s, model.DefaultProfile())
	if a.Verdict != logic.Unknown {
		t.Fatalf("verdict with no criteria = %v, want unknown", a.Verdict)
	}
	if a.Note == "" {
		t.Error("an unread class definition should say so")
	}
	if a.ExpectedValue >= a.Payout {
		t.Errorf("expected value %v should be discounted below the payout %v",
			a.ExpectedValue, a.Payout)
	}
}

func TestUnpricedSettlementsRankByProbability(t *testing.T) {
	// Most notices state no per-claimant figure. Ranking every one of them
	// equally would put a class you are almost certainly in level with one you
	// are almost certainly not, which discards the only thing known about them.
	mk := func(id string, prevalence float64) *model.Settlement {
		return &model.Settlement{ID: id, Stage: model.StageClaimsOpen,
			Criteria: []model.Criterion{{
				Kind: model.KindService, Entity: id, Label: id, Prevalence: prevalence,
			}}}
	}
	likely, unlikely := mk("likely", 0.9), mk("unlikely", 0.02)
	byID := map[string]*model.Settlement{"likely": likely, "unlikely": unlikely}
	p := model.DefaultProfile()

	as := []Assessment{Assess(unlikely, p), Assess(likely, p)}
	Rank(as, byID)
	if as[0].SettlementID != "likely" {
		t.Errorf("ranked %q first, want the one you probably qualify for", as[0].SettlementID)
	}

	// The proxy must not leak into anything the user reads as a dollar figure.
	for _, a := range as {
		if a.Payout != 0 || a.ExpectedValue != 0 {
			t.Errorf("%s: unpriced settlement reported payout %v / value %v",
				a.SettlementID, a.Payout, a.ExpectedValue)
		}
	}
}
