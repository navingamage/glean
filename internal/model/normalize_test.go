package model

import "testing"

func TestNormalizeEntityFoldsBanners(t *testing.T) {
	// A bread price-fixing class is written in banner names; a profile is written
	// in whatever name the person remembers. Both have to land on one key.
	for _, in := range []string{"No Frills", "no-frills", "NoFrills", "Zehrs", "Real Canadian Superstore", "Loblaw Companies Limited", "Loblaws Inc."} {
		if got := NormalizeEntity(in); got != "loblaw" {
			t.Errorf("NormalizeEntity(%q) = %q, want loblaw", in, got)
		}
	}
	for _, in := range []string{"CIBC", "Canadian Imperial Bank of Commerce", "Simplii Financial"} {
		if got := NormalizeEntity(in); got != "cibc" {
			t.Errorf("NormalizeEntity(%q) = %q, want cibc", in, got)
		}
	}
	// Something with no entry falls through to a plain slug rather than vanishing.
	if got := NormalizeEntity("Bright Widget Co."); got != "bright-widget" {
		t.Errorf("unknown entity normalized to %q", got)
	}
	// Stripping must not eat a name that ends in a suffix-shaped word.
	for in, want := range map[string]string{
		"Air Canada Inc.":  "air-canada",
		"Air Canada Rouge": "air-canada",
		"Bank of Montreal": "bmo",
	} {
		if got := NormalizeEntity(in); got != want {
			t.Errorf("NormalizeEntity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeProvince(t *testing.T) {
	cases := map[string]string{
		"Ontario": "ON", "ont": "ON", "ON": "ON",
		"British Columbia": "BC", "Colombie-Britannique": "BC",
		"Canada": "CA", "nationwide": "CA",
		"Ohio": "",
	}
	for in, want := range cases {
		if got := NormalizeProvince(in); got != want {
			t.Errorf("NormalizeProvince(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalURLRepairsDoubledScheme(t *testing.T) {
	// mnp.ca emits these; without the repair they parse to a host of "https:"
	// and the settlement behind them is never fetched.
	got := CanonicalURL("https://https://www.mnp.ca/en/class-action/abilify")
	if got != "https://www.mnp.ca/en/class-action/abilify" {
		t.Errorf("CanonicalURL = %q", got)
	}
}

func TestCanonicalKeepsTheAddressIdentityNormalizesIt(t *testing.T) {
	// CanonicalURL is what gets fetched, so it must not invent a scheme or a
	// host the site does not answer on. IdentityURL is what gets hashed, so it
	// must collapse the spellings.
	raw := "http://www.example.ca/cases/bread/?utm_source=x#top"
	if got, want := CanonicalURL(raw), "http://www.example.ca/cases/bread"; got != want {
		t.Errorf("CanonicalURL = %q, want %q", got, want)
	}
	if got, want := IdentityURL(raw), "https://example.ca/cases/bread"; got != want {
		t.Errorf("IdentityURL = %q, want %q", got, want)
	}
}

func TestCanonicalURLCollapsesVariants(t *testing.T) {
	want := NewID("https://ca.topclassactions.com/lawsuit-settlements/x/")
	for _, in := range []string{
		"https://www.ca.topclassactions.com/lawsuit-settlements/x/",
		"http://ca.topclassactions.com/lawsuit-settlements/x",
		"https://ca.topclassactions.com/lawsuit-settlements/x/?utm_source=news#top",
	} {
		if got := NewID(in); got != want {
			t.Errorf("NewID(%q) = %q, want %q", in, got, want)
		}
	}
}
