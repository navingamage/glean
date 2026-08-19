package main

import (
	"fmt"
	"strings"

	"github.com/navingamage/glean/internal/logic"
	"github.com/navingamage/glean/internal/match"
	"github.com/navingamage/glean/internal/model"
)

func verdictWord(a match.Assessment) string {
	switch a.Verdict {
	case logic.True:
		return "yes"
	case logic.False:
		return "no"
	default:
		if a.Probability > 0 {
			return fmt.Sprintf("%d%%", int(a.Probability*100))
		}
		return "?"
	}
}

func value(a match.Assessment) string {
	if a.Payout <= 0 {
		return "?"
	}
	if a.Verdict == logic.True {
		return fmt.Sprintf("$%.0f", a.Payout)
	}
	return fmt.Sprintf("~$%.0f", a.ExpectedValue)
}

// deadline renders the claim deadline with the number of days attached, because
// a date on its own does not make anyone act and "9 days" does.
func deadline(s *model.Settlement) string {
	if s.ClaimDeadline.Zero() {
		return "unknown"
	}
	d := s.ClaimDeadline.DaysUntil()
	switch {
	case d < 0:
		return s.ClaimDeadline.String() + " (past)"
	case d == 0:
		return s.ClaimDeadline.String() + " (today)"
	case d == 1:
		return s.ClaimDeadline.String() + " (1 day)"
	default:
		return fmt.Sprintf("%s (%dd)", s.ClaimDeadline, d)
	}
}

func totalWorth(qs []match.Question) float64 {
	var sum float64
	for _, q := range qs {
		sum += q.Worth
	}
	return sum
}

func provinceList(rs []model.Residence) string {
	if len(rs) == 0 {
		return "(nowhere on file)"
	}
	var parts []string
	for _, r := range rs {
		name := model.Provinces[r.Province]
		if name == "" {
			name = r.Province
		}
		if !r.Window.Zero() {
			name += " (" + r.Window.String() + ")"
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ", ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func trim(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// commas groups a dollar figure for reading. Settlement funds are the one place
// in this program where the number is large enough for it to matter.
func commas(f float64) string {
	s := fmt.Sprintf("%.0f", f)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func wrap(s string, width int) string { return wrapIndent(s, width, "") }

// wrapIndent hard-wraps prose to a width, which matters for the class
// definition: it is one sentence of two hundred words and reading it is the
// whole point of `glean show`.
func wrapIndent(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		switch {
		case i == 0:
			b.WriteString(w)
			lineLen = len(w)
		case lineLen+1+len(w) > width:
			b.WriteString("\n" + indent + w)
			lineLen = len(indent) + len(w)
		default:
			b.WriteString(" " + w)
			lineLen += 1 + len(w)
		}
	}
	return b.String()
}
