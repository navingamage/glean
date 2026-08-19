// Package htmlx pulls readable text and links out of HTML.
//
// It is not a conforming HTML parser and does not build a tree. Everything glean
// needs from a page is answered by a linear scan: the anchors, so a listing page
// can be turned into a work list, and the visible prose, so a notice page can be
// handed to an extractor. A tokenizer does both in one pass over the bytes and
// has the useful property that malformed markup degrades into slightly worse
// text rather than into an error, which matters when the corpus is several dozen
// law firm websites of varying vintage.
package htmlx

import (
	"strings"
)

type TokenKind uint8

const (
	Text TokenKind = iota
	StartTag
	EndTag
	Comment
)

type Token struct {
	Kind TokenKind
	Name string // lowercased element name, empty for Text and Comment
	Attr map[string]string
	Data string // text content, for Text and Comment
}

// Tokenizer walks an HTML document. The zero value is not usable; call NewTokenizer.
type Tokenizer struct {
	s string
	i int
}

func NewTokenizer(s string) *Tokenizer { return &Tokenizer{s: s} }

// Next returns the next token, and false once the input is exhausted.
func (t *Tokenizer) Next() (Token, bool) {
	if t.i >= len(t.s) {
		return Token{}, false
	}
	if t.s[t.i] != '<' {
		start := t.i
		if j := strings.IndexByte(t.s[t.i:], '<'); j < 0 {
			t.i = len(t.s)
		} else {
			t.i += j
		}
		return Token{Kind: Text, Data: t.s[start:t.i]}, true
	}

	// A '<' that does not begin a tag is literal text, which is common in
	// hand-written pages and in unescaped code samples.
	if t.i+1 >= len(t.s) || !isTagStart(t.s[t.i+1]) {
		t.i++
		return Token{Kind: Text, Data: "<"}, true
	}

	if strings.HasPrefix(t.s[t.i:], "<!--") {
		end := strings.Index(t.s[t.i+4:], "-->")
		if end < 0 {
			data := t.s[t.i+4:]
			t.i = len(t.s)
			return Token{Kind: Comment, Data: data}, true
		}
		data := t.s[t.i+4 : t.i+4+end]
		t.i += 4 + end + 3
		return Token{Kind: Comment, Data: data}, true
	}
	// Doctypes and processing instructions carry nothing we want.
	if t.s[t.i+1] == '!' || t.s[t.i+1] == '?' {
		if end := strings.IndexByte(t.s[t.i:], '>'); end >= 0 {
			t.i += end + 1
		} else {
			t.i = len(t.s)
		}
		return Token{Kind: Comment}, true
	}

	return t.tag()
}

func (t *Tokenizer) tag() (Token, bool) {
	i := t.i + 1
	tok := Token{Kind: StartTag}
	if i < len(t.s) && t.s[i] == '/' {
		tok.Kind = EndTag
		i++
	}
	start := i
	for i < len(t.s) && !isSpace(t.s[i]) && t.s[i] != '>' && t.s[i] != '/' {
		i++
	}
	tok.Name = strings.ToLower(t.s[start:i])

	if tok.Kind == StartTag {
		tok.Attr = map[string]string{}
	}
	for i < len(t.s) && t.s[i] != '>' {
		for i < len(t.s) && (isSpace(t.s[i]) || t.s[i] == '/') {
			i++
		}
		if i >= len(t.s) || t.s[i] == '>' {
			break
		}
		nameStart := i
		for i < len(t.s) && !isSpace(t.s[i]) && t.s[i] != '=' && t.s[i] != '>' && t.s[i] != '/' {
			i++
		}
		name := strings.ToLower(t.s[nameStart:i])
		for i < len(t.s) && isSpace(t.s[i]) {
			i++
		}
		value := ""
		if i < len(t.s) && t.s[i] == '=' {
			i++
			for i < len(t.s) && isSpace(t.s[i]) {
				i++
			}
			if i < len(t.s) && (t.s[i] == '"' || t.s[i] == '\'') {
				quote := t.s[i]
				i++
				vs := i
				if close := strings.IndexByte(t.s[vs:], quote); close >= 0 {
					value = t.s[vs : vs+close]
					i = vs + close + 1
				} else {
					// An unterminated quote consumes the rest of the document
					// under the HTML spec, and browsers do exactly that. A
					// scraper cannot afford to: one typo'd attribute would cost
					// us every settlement further down the page. Treat the
					// value as ending at the next '>' instead, which recovers
					// the author's evident intent and bounds the damage to one
					// tag.
					if gt := strings.IndexByte(t.s[vs:], '>'); gt >= 0 {
						value = t.s[vs : vs+gt]
						i = vs + gt
					} else {
						value = t.s[vs:]
						i = len(t.s)
					}
				}
			} else {
				vs := i
				for i < len(t.s) && !isSpace(t.s[i]) && t.s[i] != '>' {
					i++
				}
				value = t.s[vs:i]
			}
		}
		if name != "" && tok.Attr != nil {
			tok.Attr[name] = Unescape(value)
		}
	}
	if i < len(t.s) {
		i++ // consume '>'
	}
	t.i = i
	return tok, true
}

// SkipTo consumes everything up to the matching end tag, without interpreting
// it. Script and style bodies are not markup and must not be tokenized as such,
// or a '<' inside a JavaScript comparison starts a phantom element.
func (t *Tokenizer) SkipTo(name string) {
	needle := "</" + name
	if j := indexFold(t.s[t.i:], needle); j >= 0 {
		t.i += j
		if end := strings.IndexByte(t.s[t.i:], '>'); end >= 0 {
			t.i += end + 1
			return
		}
	}
	t.i = len(t.s)
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isTagStart(c byte) bool {
	return c == '!' || c == '?' || c == '/' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func indexFold(s, substr string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}
