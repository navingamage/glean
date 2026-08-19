package htmlx

import (
	"strings"
	"testing"
)

const page = `<!doctype html>
<html><head><title>Bread &amp; Butter Settlement</title>
<style>a { color: red } /* < not a tag */</style></head>
<body>
<nav><a href="/">Home</a></nav>
<h1>Packaged Bread Settlement</h1>
<p>Claims close on <b>October&nbsp;15, 2026</b>.</p>
<script>if (a < b && c > d) { document.write("<p>nope</p>") }</script>
<ul>
  <li><a href='/claims/bread'>File a claim</a></li>
  <li><a href="https://example.org/notice.pdf" >Read the notice</a></li>
  <li><a href=#top>Back to top</a></li>
  <li><a href="javascript:void(0)">Menu</a></li>
</ul>
<table><tr><td>Deadline</td><td>2026-10-15</td></tr></table>
</body></html>`

func TestExtractTextIgnoresScriptAndStyle(t *testing.T) {
	got := ExtractText(page)
	for _, bad := range []string{"color: red", "document.write", "nope"} {
		if strings.Contains(got, bad) {
			t.Errorf("text leaked %q from a skipped element:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "Claims close on October 15, 2026.") {
		t.Errorf("inline markup should not split a sentence:\n%s", got)
	}
	if strings.Contains(got, "2026.File") {
		t.Errorf("block boundaries were not preserved:\n%s", got)
	}
}

func TestExtractLinksResolvesAndFilters(t *testing.T) {
	links := ExtractLinks(page, "https://law.example.ca/cases/bread")
	var got []string
	for _, l := range links {
		got = append(got, l.URL+" | "+l.Text)
	}
	want := []string{
		"https://law.example.ca/ | Home",
		"https://law.example.ca/claims/bread | File a claim",
		"https://example.org/notice.pdf | Read the notice",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d links %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTitlePrefersTitleElement(t *testing.T) {
	if got := Title(page); got != "Bread & Butter Settlement" {
		t.Errorf("Title = %q", got)
	}
	if got := Title(`<body><h1>Fallback  Heading</h1></body>`); got != "Fallback Heading" {
		t.Errorf("h1 fallback = %q", got)
	}
}

func TestMalformedMarkupDegradesQuietly(t *testing.T) {
	// Unclosed tags, a bare '<' in prose, and an unterminated attribute are all
	// things real pages contain. None of them may lose the surrounding text.
	got := ExtractText(`<p>5 < 6 is true<p class="x>Deadline: soon</p><div>tail`)
	for _, want := range []string{"5 < 6 is true", "tail"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from malformed input:\n%s", want, got)
		}
	}
}

func TestUnterminatedScriptDoesNotSwallowNothing(t *testing.T) {
	// A script that is never closed runs to the end of the document. Consuming
	// the rest is correct; what must not happen is emitting the JavaScript.
	got := ExtractText(`<p>before<script>var x = 1;`)
	if !strings.Contains(got, "before") || strings.Contains(got, "var x") {
		t.Errorf("unterminated script handled as %q", got)
	}
}

func TestCleanTitleStripsMastheadOnly(t *testing.T) {
	cases := []struct{ title, url, want string }{
		{"Bread - CFM Lawyers", "https://cfmlawyers.ca/class-action/bread", "Bread"},
		{"Starbucks Class Action | Canooq.ca", "https://canooq.ca/x", "Starbucks Class Action"},
		{"GM Ignition Switch | Merchant Law Group LLP", "https://merchantlaw.com/x", "GM Ignition Switch"},
		// The tail is part of the name, not the masthead, and must survive.
		{"Smith v. Loblaw - Ontario Class", "https://example.ca/x", "Smith v. Loblaw - Ontario Class"},
		{"Abilify Settlement", "https://mnp.ca/x", "Abilify Settlement"},
		{"CHL Class Action | Home", "https://charneylawyers.com/x", "CHL Class Action"},
		{"Settlement - Yaz and Yasmin | Consumer Law Group Class Actions Canada", "https://clg.org/x", "Settlement - Yaz and Yasmin"},
		// A long tail is prose, not a masthead.
		{"Notice - claims must be filed by October 15 2026", "https://x.ca/y", "Notice - claims must be filed by October 15 2026"},
	}
	for _, c := range cases {
		if got := CleanTitle(c.title, c.url); got != c.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestSourceWrappingDoesNotSplitASentence(t *testing.T) {
	// HTML source is wrapped at whatever column the author's editor used. If
	// those newlines survive into the extracted text, a deadline sentence broken
	// across two source lines becomes unreadable to anything scanning one line
	// at a time, and the deadline is silently lost.
	doc := "<p>The claims deadline is\n  October 15, 2026. Each class member may\nreceive up to $25.</p>"
	got := ExtractText(doc)
	if !strings.Contains(got, "The claims deadline is October 15, 2026.") {
		t.Errorf("source wrapping leaked into the text:\n%q", got)
	}
	if strings.Contains(got, "may\nreceive") {
		t.Errorf("newline survived mid-sentence:\n%q", got)
	}
}

func TestInlineElementsDoNotFuseWords(t *testing.T) {
	if got := ExtractText("<p>closes on <b>October 15</b> this year</p>"); !strings.Contains(got, "closes on October 15 this year") {
		t.Errorf("inline boundary lost a space: %q", got)
	}
}
