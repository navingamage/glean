package extract

import (
	"testing"

	"github.com/navingamage/glean/internal/model"
)

func TestDeadlineAcrossBlockBoundaries(t *testing.T) {
	// Notices lay deadlines out as label and value in separate cells at least as
	// often as they write them in a sentence, and the text extractor turns those
	// into separate lines. A cue that stops at the first newline reads the label
	// and misses the date sitting directly beneath it.
	text := "Settlement summary\nOpt-out deadline\nAugust 31, 2026\nComments or objections deadline\nSeptember 15, 2026"
	var s model.Settlement
	Backfill(&s, text)
	if got := s.OptOutDeadline.String(); got != "2026-08-31" {
		t.Errorf("opt out deadline = %q, want 2026-08-31", got)
	}
}

func TestDeadlineFromProse(t *testing.T) {
	cases := map[string]string{
		"Claims must be postmarked on or before October 15, 2026.": "2026-10-15",
		"The claims deadline is 15 October 2026.":                  "2026-10-15",
		"Deadline to file a claim: 2026-10-15":                     "2026-10-15",
		"You must submit a claim by December 1, 2027 to be paid.":  "2027-12-01",
		"La date limite est le 15 octobre 2026.":                   "",
	}
	for text, want := range cases {
		var s model.Settlement
		Backfill(&s, text)
		if got := s.ClaimDeadline.String(); got != want {
			t.Errorf("%q gave deadline %q, want %q", text, got, want)
		}
	}
}

func TestCueDoesNotReachIntoTheNextParagraph(t *testing.T) {
	// "deadline" here belongs to a hearing, and the date four lines down belongs
	// to something else entirely. Reaching across to it would produce a
	// confident, wrong date, which is the worst thing this extractor can do.
	text := "The approval hearing deadline for objections has passed.\n\n\n\n\nUnrelated notice dated March 3, 2030."
	var s model.Settlement
	Backfill(&s, text)
	if got := s.ClaimDeadline.String(); got == "2030-03-03" {
		t.Errorf("cue reached across a paragraph break to %q", got)
	}
}

func TestMoneyParsing(t *testing.T) {
	cases := map[string]float64{
		"$7.5 million settlement": 7_500_000,
		"$26-million":             26_000_000,
		"$1,250,000":              1_250_000,
		"up to $5,000":            5000,
		"$25":                     25,
		"no amount here":          0,
	}
	for in, want := range cases {
		if got := ParseMoney(in); got != want {
			t.Errorf("ParseMoney(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRelevanceGate(t *testing.T) {
	notice := "Notice of certified class action settlement. Class members may file a claim " +
		"before the claim deadline. The claims administrator will process each claim form. " +
		"The class period runs from 2001 to 2021. Eligible class members may opt out. " +
		"This settlement was court approved and the claim form is available online now. " +
		"Please read this notice of settlement carefully to decide whether to submit a claim."
	if Relevance(notice) < 8 {
		t.Errorf("a real notice scored %d, below the extraction gate", Relevance(notice))
	}
	careers := "Join our team. See our practice areas and our lawyers. Privacy policy and terms of use apply."
	if Relevance(careers) >= 8 {
		t.Errorf("a careers page scored %d, above the extraction gate", Relevance(careers))
	}
}

func TestFindPortalPrefersTheSettlementsOwnDomain(t *testing.T) {
	doc := `<body>
	<a href="https://x.com/CFMLawyers">Follow us</a>
	<a href="https://www.cbc.ca/news/business/bread-price-fixing-1.4878243">Read the coverage</a>
	<a href="http://www.CanadianBreadSettlement.ca">Official settlement website</a>
	<a href="https://cfmlawyers.ca/people">Our team</a>
	</body>`
	got := FindPortal(doc, "https://cfmlawyers.ca/class-action/bread")
	if got != "http://www.CanadianBreadSettlement.ca" {
		t.Errorf("FindPortal = %q", got)
	}
}

func TestFindPortalDeclinesWhenNothingLooksOfficial(t *testing.T) {
	doc := `<body><a href="https://x.com/a">X</a><a href="https://cbc.ca/news/story">Coverage</a></body>`
	if got := FindPortal(doc, "https://firm.ca/cases/one"); got != "" {
		t.Errorf("FindPortal invented a portal: %q", got)
	}
}

func TestFindPortalIgnoresSameSiteLinks(t *testing.T) {
	doc := `<body><a href="/settlement-claim-form">File a claim</a></body>`
	if got := FindPortal(doc, "https://firm.ca/cases/one"); got != "" {
		t.Errorf("second hop should be off-site only, got %q", got)
	}
}
