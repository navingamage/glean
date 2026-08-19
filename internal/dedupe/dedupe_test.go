package dedupe

import (
	"testing"

	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/source"
)

func d(s string) model.Date {
	v, err := model.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func set(id, sourceID, title, deadline, claimURL string) *model.Settlement {
	return &model.Settlement{
		ID: id, SourceID: sourceID, Title: title,
		ClaimDeadline: d(deadline), ClaimURL: claimURL,
	}
}

var registry = []*source.Source{
	{ID: "canooq", Kind: source.KindAggregator},
	{ID: "mnp", Kind: source.KindAdministrator},
	{ID: "cfm", Kind: source.KindFirm},
}

func TestSpacingDifferencesStillMerge(t *testing.T) {
	// These are the real pair that motivated the package: one site writes the
	// company name as three words, the other as one, so they share no title
	// words at all and nearly every character trigram.
	a := set("a", "canooq", "Big Steel Box Flood Damage Settlement", "2026-10-30", "")
	b := set("b", "mnp", "BigSteelBox Abbotsford flood damage settlement", "2026-10-30", "")

	groups := Collapse([]*model.Settlement{a, b}, registry)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	// The administrator's record wins, because it is the one whose deadline
	// will actually be enforced.
	if groups[0].Primary.SourceID != "mnp" {
		t.Errorf("primary came from %q, want the administrator", groups[0].Primary.SourceID)
	}
	if got := groups[0].Sources(); len(got) != 2 {
		t.Errorf("sources = %v, want both", got)
	}
}

func TestSimilarTitlesWithDifferentDeadlinesStaySeparate(t *testing.T) {
	// Two genuinely different tobacco proceedings that share most of their
	// words. A disagreement on a stated deadline is proof of difference and is
	// checked before any similarity score.
	a := set("a", "canooq", "Tobacco Claims Canada - Quebec Class Action Plan", "2026-08-31", "")
	b := set("b", "canooq", "Tobacco Claims Canada - Pan-Canadian Claimants Plan", "2027-09-03", "")

	if groups := Collapse([]*model.Settlement{a, b}, registry); len(groups) != 2 {
		t.Fatalf("merged two different proceedings into %d group(s)", len(groups))
	}
}

func TestDifferentPortalsNeverMerge(t *testing.T) {
	a := set("a", "canooq", "Widget Defect Settlement", "", "https://widgetsettlement.ca/claim")
	b := set("b", "cfm", "Widget Defect Settlement", "", "https://otherwidget.ca/claim")
	if groups := Collapse([]*model.Settlement{a, b}, registry); len(groups) != 2 {
		t.Fatal("records pointing at different claim portals must not merge")
	}
}

func TestSharedPortalMergesOnAWeakerTitleMatch(t *testing.T) {
	// The portal is close to proof on its own, so the titles only have to be
	// plausibly related.
	a := set("a", "canooq", "Apple gift card scam settlement", "", "https://giftcardsettlement.ca/")
	b := set("b", "mnp", "Apple Gift Card Class Action Settlement", "", "https://www.giftcardsettlement.ca/")
	groups := Collapse([]*model.Settlement{a, b}, registry)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
}

func TestUnrelatedSettlementsAreLeftAlone(t *testing.T) {
	in := []*model.Settlement{
		set("a", "canooq", "Packaged bread price-fixing", "2027-06-30", ""),
		set("b", "canooq", "Rexulti side effects compulsive behaviour", "2026-08-27", ""),
		set("c", "cfm", "BC Birth Alerts", "", ""),
	}
	if groups := Collapse(in, registry); len(groups) != 3 {
		t.Fatalf("collapsed unrelated settlements into %d group(s)", len(groups))
	}
}

func TestOneUnknownDeadlineIsNeutral(t *testing.T) {
	// Nearly identical titles, one record missing a deadline. That is a record
	// that knows less, not a different case.
	a := set("a", "canooq", "Canadian Armed Forces Racial Discrimination / Harassment Settlement", "2026-10-15", "")
	b := set("b", "canooq", "Canadian Armed Forces Racial Discrimination and Harassment Class Action", "", "")
	groups := Collapse([]*model.Settlement{a, b}, registry)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	// Same authority, so the tie goes to the record that knows more.
	if groups[0].Primary.ID != "a" {
		t.Errorf("primary = %q, want the record with the deadline", groups[0].Primary.ID)
	}
}

func TestOrderOfPrimariesIsPreserved(t *testing.T) {
	in := []*model.Settlement{
		set("first", "canooq", "Alpha matter", "", ""),
		set("second", "canooq", "Beta matter", "", ""),
		set("third", "canooq", "Gamma matter", "", ""),
	}
	groups := Collapse(in, registry)
	for i, want := range []string{"first", "second", "third"} {
		if groups[i].Primary.ID != want {
			t.Fatalf("group %d = %q, want %q: a ranked list must stay ranked",
				i, groups[i].Primary.ID, want)
		}
	}
}
