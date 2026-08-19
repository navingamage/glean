package extract

import (
	"testing"

	"github.com/navingamage/glean/internal/model"
)

const claimablePage = `Notice of settlement. The court approved the settlement on June 1.
Class members may file a claim using the claim form below. The claims deadline is
October 15, 2026. A claims administrator has been appointed.`

const earlyPage = `A proposed class action was commenced against the defendant in
the Ontario Superior Court of Justice. The action has not been certified and no
settlement has been reached. Register your interest to be kept informed.`

func TestStakesSpendsOnClaimablePages(t *testing.T) {
	if got := Stakes(claimablePage, false); got != Prime {
		t.Errorf("a page with a live claim form got tier %v, want prime", got)
	}
	if got := Stakes(earlyPage, false); got != Cheap {
		t.Errorf("an uncertified action got tier %v, want cheap", got)
	}
	// An administrator or court registry is where a deadline is authoritative,
	// so those pages are always worth the good model.
	if got := Stakes(earlyPage, true); got != Prime {
		t.Errorf("an authoritative source got tier %v, want prime", got)
	}
}

func TestStakesNeedsMoreThanOnePassingMention(t *testing.T) {
	// A firm's page that mentions claim forms once while describing what a class
	// action is should not be charged at the prime rate.
	page := `Our class action practice helps people file a claim when a settlement
	is reached. Contact us to discuss your matter.`
	if got := Stakes(page, false); got != Cheap {
		t.Errorf("a single passing mention got tier %v, want cheap", got)
	}
}

func TestRouterTiers(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test")
	r, err := NewRouter("", "cheap/model", "prime/model")
	if err != nil {
		t.Fatal(err)
	}
	if r.For(Cheap).Model != "cheap/model" || r.For(Prime).Model != "prime/model" {
		t.Errorf("router wired wrong: %s / %s", r.For(Cheap).Model, r.For(Prime).Model)
	}
	// The cheap tier truncates harder, because input tokens dominate the bill.
	if r.For(Cheap).MaxChars >= r.For(Prime).MaxChars {
		t.Errorf("cheap tier sends %d chars, prime sends %d; cheap should send less",
			r.For(Cheap).MaxChars, r.For(Prime).MaxChars)
	}
	// One bill, however many tiers ran.
	r.For(Cheap).Usage.add(10, 2, 0.01)
	r.For(Prime).Usage.add(10, 2, 0.02)
	if got := r.Usage().Cost; got < 0.0299 || got > 0.0301 {
		t.Errorf("combined cost = %v, want 0.03", got)
	}
	if got := r.String(); got != "cheap/model / prime/model" {
		t.Errorf("String = %q", got)
	}
}

func TestPinningBothTiersCollapsesToOneClient(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test")
	r, err := NewRouter("", "one/model", "one/model")
	if err != nil {
		t.Fatal(err)
	}
	if r.For(Cheap) != r.For(Prime) {
		t.Error("-model should pin both tiers to a single client")
	}
	if got := r.String(); got != "one/model" {
		t.Errorf("String = %q, want the bare model id", got)
	}
}

func TestUnderspecifiedOnlyFlagsClaimablePages(t *testing.T) {
	cases := []struct {
		name string
		s    *model.Settlement
		want bool
	}{
		{"claimable with no class definition",
			&model.Settlement{Stage: model.StageClaimsOpen}, true},
		{"approved but claims not yet open",
			&model.Settlement{Stage: model.StageSettled}, true},
		{"claimable and decomposed",
			&model.Settlement{Stage: model.StageClaimsOpen,
				Criteria: []model.Criterion{{Kind: model.KindResidency, Entity: "CA"}}}, false},
		{"only just filed, allowed to be vague",
			&model.Settlement{Stage: model.StageFiled}, false},
		{"already closed",
			&model.Settlement{Stage: model.StageClaimsClose}, false},
		{"nothing at all", nil, false},
	}
	for _, c := range cases {
		if got := Underspecified(c.s); got != c.want {
			t.Errorf("%s: Underspecified = %v, want %v", c.name, got, c.want)
		}
	}
}
