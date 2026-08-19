package htmlx

import (
	"net/url"
	"strings"

	stdhtml "html"
)

// Unescape resolves HTML character references. The standard library handles the
// full named-entity table, so there is nothing to gain from writing another one.
func Unescape(s string) string { return stdhtml.UnescapeString(s) }

// skipped elements contribute nothing a reader would see. Their contents are
// consumed wholesale rather than tokenized, because script and style bodies are
// not markup.
var skipped = map[string]bool{
	"script": true, "style": true, "noscript": true,
	"svg": true, "head": true, "template": true, "iframe": true,
}

// blocks force a line break so that separate paragraphs, list items and table
// rows do not run together into one sentence. That matters more than it sounds:
// an extractor reading "claims deadlineOctober 15 2026opt out" has to guess at
// boundaries that the markup already knew.
var blocks = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true, "li": true, "tr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"section": true, "article": true, "header": true, "footer": true, "main": true,
	"table": true, "ul": true, "ol": true, "dl": true, "dt": true, "dd": true,
	"blockquote": true, "form": true, "figure": true, "nav": true, "aside": true,
}

// ExtractText returns the visible prose of a document, with runs of whitespace
// collapsed and block boundaries preserved as newlines.
func ExtractText(doc string) string {
	var b strings.Builder
	z := NewTokenizer(doc)
	for {
		tok, ok := z.Next()
		if !ok {
			break
		}
		switch tok.Kind {
		case StartTag:
			if skipped[tok.Name] {
				z.SkipTo(tok.Name)
				continue
			}
			if blocks[tok.Name] {
				b.WriteByte('\n')
			}
			if tok.Name == "td" || tok.Name == "th" {
				b.WriteString("\t")
			}
		case EndTag:
			if blocks[tok.Name] {
				b.WriteByte('\n')
			}
		case Text:
			// Whitespace inside a text node, newlines included, is just
			// whitespace: HTML source is wrapped at whatever column the author's
			// editor used. Collapsing it here rather than in tidy is what keeps a
			// sentence that the markup wrapped mid-phrase from being read as two
			// lines, which would put "the claims deadline is" and the date that
			// follows it out of reach of the same regular expression.
			b.WriteString(collapseSpace(Unescape(tok.Data)))
		}
	}
	return tidy(b.String())
}

// collapseSpace reduces every run of whitespace to one space, preserving a
// leading and trailing space so that adjacent inline elements do not fuse.
func collapseSpace(s string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			space = true
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteByte(c)
		}
	}
	out := b.String()
	if out == "" && space {
		return " "
	}
	if space && out != "" {
		out += " "
	}
	if len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t' || s[0] == '\r') && out != "" {
		out = " " + out
	}
	return out
}

// tidy collapses horizontal whitespace within a line and blank runs between
// lines, leaving text that reads the way the page looks.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimSpace(strings.Join(strings.Fields(line), " "))
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Link is an anchor with its visible text. The text is as informative as the
// href for deciding whether a link is worth following: "2026 settlement claims
// now open" is a stronger signal than any URL pattern.
type Link struct {
	URL  string
	Text string
}

// ExtractLinks returns every anchor in the document, with relative hrefs
// resolved against base. Fragment-only, mailto and javascript links are dropped.
func ExtractLinks(doc, base string) []Link {
	baseURL, err := url.Parse(base)
	if err != nil {
		baseURL = nil
	}

	var out []Link
	var cur *Link
	var text strings.Builder

	flush := func() {
		if cur == nil {
			return
		}
		cur.Text = tidy(text.String())
		out = append(out, *cur)
		cur, text = nil, strings.Builder{}
	}

	z := NewTokenizer(doc)
	for {
		tok, ok := z.Next()
		if !ok {
			break
		}
		switch tok.Kind {
		case StartTag:
			if skipped[tok.Name] {
				z.SkipTo(tok.Name)
				continue
			}
			if tok.Name != "a" {
				continue
			}
			flush() // nested anchors are invalid; treat the inner one as a restart
			href := strings.TrimSpace(tok.Attr["href"])
			if href == "" || strings.HasPrefix(href, "#") {
				continue
			}
			switch {
			case strings.HasPrefix(strings.ToLower(href), "javascript:"),
				strings.HasPrefix(strings.ToLower(href), "mailto:"),
				strings.HasPrefix(strings.ToLower(href), "tel:"):
				continue
			}
			resolved := href
			if baseURL != nil {
				if u, err := url.Parse(href); err == nil {
					resolved = baseURL.ResolveReference(u).String()
				}
			}
			cur = &Link{URL: resolved}
		case EndTag:
			if tok.Name == "a" {
				flush()
			}
		case Text:
			if cur != nil {
				text.WriteString(Unescape(tok.Data))
			}
		}
	}
	flush()
	return out
}

// CleanTitle strips the site name that content management systems append to
// every page title. "Bread - CFM Lawyers" and "Starbucks Class Action |
// Canooq.ca" are both really one settlement's name plus the masthead, and
// carrying the masthead into a list of forty settlements makes the list harder
// to read for no gain.
//
// The tail is only removed when it looks like the site rather than like part of
// the name: it has to be short, and it has to either echo the domain or read
// like a firm's letterhead. Anything else is left alone, because a settlement
// genuinely called "Smith v. Loblaw - Ontario" should keep its second half.
func CleanTitle(title, pageURL string) string {
	host := ""
	if u, err := url.Parse(pageURL); err == nil {
		host = strings.ToLower(strings.TrimPrefix(u.Host, "www."))
		if i := strings.IndexByte(host, '.'); i > 0 {
			host = host[:i]
		}
	}

	for _, sep := range []string{" | ", " - ", " — ", " · "} {
		i := strings.LastIndex(title, sep)
		if i <= 0 {
			continue
		}
		head, tail := strings.TrimSpace(title[:i]), strings.TrimSpace(title[i+len(sep):])
		if head == "" || len(tail) > 60 || len(strings.Fields(tail)) > 7 {
			continue
		}
		if looksLikeMasthead(tail, host) {
			return head
		}
	}
	return title
}

func looksLikeMasthead(tail, host string) bool {
	key := strings.ToLower(strings.Join(strings.Fields(tail), ""))
	key = strings.NewReplacer(".", "", "-", "", "&", "", "'", "").Replace(key)
	if host != "" && len(host) > 2 && strings.Contains(key, host) {
		return true
	}
	// A firm's name anywhere in the tail, not only at the end: pages get titled
	// "... | Consumer Law Group Class Actions Canada" as readily as
	// "... | CFM Lawyers", and neither trailing word carries information.
	for _, mark := range []string{
		"llp", "lawyers", "lawgroup", "barristers", "avocats", "attorneys", "lawfirm",
	} {
		if strings.Contains(key, mark) {
			return true
		}
	}
	// Navigation chrome that a template appended.
	switch key {
	case "home", "index", "classactions", "classaction", "settlements",
		"cases", "ourcases", "news", "recourscollectifs":
		return true
	}
	return strings.HasSuffix(key, "law")
}

// Title returns the document title, or the first h1, or "".
func Title(doc string) string {
	z := NewTokenizer(doc)
	var capture string
	var buf strings.Builder
	for {
		tok, ok := z.Next()
		if !ok {
			break
		}
		switch tok.Kind {
		case StartTag:
			if tok.Name == "title" || tok.Name == "h1" {
				capture, buf = tok.Name, strings.Builder{}
				continue
			}
			if skipped[tok.Name] && tok.Name != "head" {
				z.SkipTo(tok.Name)
			}
		case EndTag:
			if tok.Name == capture {
				if t := tidy(buf.String()); t != "" {
					return t
				}
				capture = ""
			}
		case Text:
			if capture != "" {
				buf.WriteString(Unescape(tok.Data))
			}
		}
	}
	return ""
}
