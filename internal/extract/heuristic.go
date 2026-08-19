// Package extract turns the prose of a settlement notice into a structured
// record.
//
// There are two extractors and they are not alternatives so much as a floor and
// a ceiling. The heuristic one reads dates and dollar figures out of the text
// with regular expressions; it is free, it runs offline, and it is right about
// deadlines far more often than it has any business being, because notices are
// written to a template. The model-backed one reads the class definition, which
// is prose that no regular expression is going to parse, and is the only way to
// get the criteria the matcher needs.
//
// Running the heuristic first is not just a fallback. It decides whether a page
// is about a class action at all, and that gate is what keeps the model from
// being handed several hundred partner biographies a day.
package extract

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/navingamage/glean/internal/model"
)

// Relevance scores how much a page looks like a settlement notice. Zero or below
// means don't spend a model call on it.
func Relevance(text string) int {
	low := strings.ToLower(text)
	score := 0
	for phrase, weight := range map[string]int{
		"class action": 5, "class proceeding": 5, "action collective": 5,
		"recours collectif": 5, "settlement": 4, "class member": 4,
		"claim form": 5, "claims administrator": 5, "claim deadline": 6,
		"opt out": 3, "opt-out": 3, "class period": 5, "certified": 2,
		"eligible": 2, "compensation": 2, "notice of": 2, "court approved": 3,
		"file a claim": 5, "submit a claim": 5,
	} {
		if n := strings.Count(low, phrase); n > 0 {
			score += weight
			if n > 3 {
				score += weight / 2 // repeated throughout, not a passing mention
			}
		}
	}
	// A page can mention class actions without being one. The firm's practice
	// page and its careers page both do.
	for phrase, weight := range map[string]int{
		"practice areas": 4, "our lawyers": 4, "join our team": 5,
		"privacy policy": 6, "terms of use": 6, "cookie": 3,
	} {
		if strings.Contains(low, phrase) {
			score -= weight
		}
	}
	if len(strings.Fields(text)) < 80 {
		score -= 4 // too short to be a notice
	}
	return score
}

// near is the window a cue is allowed to look in for its value: up to three
// lines, none of which may contain a sentence-ending period. Bounding it by
// lines rather than by characters alone is what keeps a cue from reaching into
// the paragraph after the one it matched.
const near = `[^.\n]{0,60}(?:\n[^.\n]{0,60}){0,2}`

var (
	monthNames = map[string]int{
		"january": 1, "jan": 1, "janvier": 1,
		"february": 2, "feb": 2, "fevrier": 2, "février": 2,
		"march": 3, "mar": 3, "mars": 3,
		"april": 4, "apr": 4, "avril": 4,
		"may": 5, "mai": 5,
		"june": 6, "jun": 6, "juin": 6,
		"july": 7, "jul": 7, "juillet": 7,
		"august": 8, "aug": 8, "aout": 8, "août": 8,
		"september": 9, "sep": 9, "sept": 9, "septembre": 9,
		"october": 10, "oct": 10, "octobre": 10,
		"november": 11, "nov": 11, "novembre": 11,
		"december": 12, "dec": 12, "decembre": 12, "décembre": 12,
	}

	// "October 15, 2026" and "15 October 2026" and "2026-10-15".
	reMonthDay = regexp.MustCompile(`(?i)\b([a-zéûôà]{3,10})\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(\d{4})\b`)
	reDayMonth = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)?\s+(?:de\s+)?([a-zéûôà]{3,10})\.?,?\s+(\d{4})\b`)
	reISO      = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)

	// The hyphen is not optional decoration: Canadian notices and the press that
	// reports them write "$26-million" about as often as "$26 million", and
	// reading the second group as absent turns twenty-six million dollars into
	// twenty-six.
	reMoney = regexp.MustCompile(`(?i)\$\s?([\d,]+(?:\.\d+)?)\s*[-\x{2010}-\x{2015}]?\s*(million|billion|m\b|b\b|k\b)?`)

	// Sentences that introduce a deadline. Ordered by how unambiguous they are.
	//
	// The window after each cue is `near`, which spans up to three lines rather
	// than stopping at the first newline. Notices lay deadlines out as label and
	// value in separate table cells and separate divs at least as often as they
	// write them in a sentence, and those become separate lines in the extracted
	// text. A cue that cannot cross a line break reads "Opt-out deadline" and
	// then stops, one line above the date it was looking for.
	deadlineCues = []*regexp.Regexp{
		regexp.MustCompile(`(?i)claims?[- ]deadline` + near),
		regexp.MustCompile(`(?i)deadline (?:to|for) (?:file|submit|apply)` + near),
		regexp.MustCompile(`(?i)(?:claims?|claim forms?) must be (?:submitted|received|postmarked|filed)` + near),
		regexp.MustCompile(`(?i)(?:file|submit) (?:your |a )?claim (?:by|before|on or before)` + near),
		regexp.MustCompile(`(?i)claims? period (?:closes|ends)` + near),
		regexp.MustCompile(`(?i)on or before` + near),
		regexp.MustCompile(`(?i)deadline` + near),
	}
	optOutCues = []*regexp.Regexp{
		regexp.MustCompile(`(?i)opt[- ]?out deadline` + near),
		regexp.MustCompile(`(?i)deadline (?:to|for) opt(?:ing)?[- ]out` + near),
		regexp.MustCompile(`(?i)(?:to )?opt out` + near),
		regexp.MustCompile(`(?i)exclude yourself` + near),
	}
	payoutCues = []*regexp.Regexp{
		regexp.MustCompile(`(?i)up to \$\s?[\d,]+(?:\.\d+)?` + near),
		regexp.MustCompile(`(?i)between \$\s?[\d,]+(?:\.\d+)? and \$\s?[\d,]+(?:\.\d+)?` + near),
		regexp.MustCompile(`(?i)(?:each|per) (?:class member|claimant|eligible)` + near),
	}
	fundCues = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\$\s?[\d,.]+\s*(?:million|billion)?[- ]?(?:dollar )?settlement`),
		regexp.MustCompile(`(?i)settlement (?:fund|amount)[^.\n]{0,60}`),
		regexp.MustCompile(`(?i)(?:agreed|settled) (?:to pay|for)[^.\n]{0,60}`),
	}
)

// ParseDatePhrase pulls the first date out of a fragment of prose.
func ParseDatePhrase(s string) model.Date {
	if m := reISO.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		if valid(y, mo, d) {
			return model.Date{Y: y, M: mo, D: d}
		}
	}
	if m := reMonthDay.FindStringSubmatch(s); m != nil {
		if mo, ok := monthNames[strings.ToLower(m[1])]; ok {
			d, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			if valid(y, mo, d) {
				return model.Date{Y: y, M: mo, D: d}
			}
		}
	}
	if m := reDayMonth.FindStringSubmatch(s); m != nil {
		if mo, ok := monthNames[strings.ToLower(m[2])]; ok {
			d, _ := strconv.Atoi(m[1])
			y, _ := strconv.Atoi(m[3])
			if valid(y, mo, d) {
				return model.Date{Y: y, M: mo, D: d}
			}
		}
	}
	return model.Date{}
}

func valid(y, m, d int) bool {
	return y >= 1980 && y <= 2100 && m >= 1 && m <= 12 && d >= 1 && d <= 31
}

// ParseMoney reads the first dollar figure out of a fragment, expanding the
// million and billion suffixes that notices always use for the fund total.
func ParseMoney(s string) float64 {
	m := reMoney.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(strings.TrimSpace(m[2])) {
	case "billion", "b":
		v *= 1e9
	case "million", "m":
		v *= 1e6
	case "k":
		v *= 1e3
	}
	return v
}

// Backfill fills in only the fields that are still empty. After a model
// extraction this recovers the cases where the model left a deadline blank
// because it was in a footer or a sidebar rather than in the prose; before one,
// with an empty record, it does the whole job.
//
// It never overwrites. A regular expression that finds "October 15" in a
// sentence about a court hearing has no way of knowing it is not the claims
// deadline, so where the model has already committed to an answer, that answer
// stands.
func Backfill(s *model.Settlement, text string) {
	if s.ClaimDeadline.Zero() {
		s.ClaimDeadline = firstDate(text, deadlineCues)
	}
	if s.OptOutDeadline.Zero() {
		s.OptOutDeadline = firstDate(text, optOutCues)
	}
	if s.SettlementFund == 0 {
		if v := firstMoney(text, fundCues); v >= 100_000 {
			s.SettlementFund = v
		}
	}
	if s.PayoutLow == 0 && s.PayoutHigh == 0 {
		s.PayoutLow, s.PayoutHigh = payoutRange(text)
	}
	if s.Stage == "" || s.Stage == model.StageUnknown {
		s.Stage = guessStage(text, s)
	}
	if s.EffortMinutes <= 0 {
		s.EffortMinutes = 15
		if s.ProofRequired {
			s.EffortMinutes = 45
		}
	}
}

// Heuristic builds the whole record from text alone. This is what runs when
// there is no API key: glean still tracks deadlines, stages and amounts, and
// simply has no class definition to match against, so every settlement it finds
// is reported as unresolved rather than as a match.
func Heuristic(s *model.Settlement, text string) {
	Backfill(s, text)
	s.ProofRequired = mentionsProof(text)
	if s.ProofRequired && s.EffortMinutes < 45 {
		s.EffortMinutes = 45
	}
	s.Extractor = "heuristic"
	s.Confidence = 0.35
}

func firstDate(text string, cues []*regexp.Regexp) model.Date {
	for _, re := range cues {
		for _, frag := range re.FindAllString(text, 4) {
			if d := ParseDatePhrase(frag); !d.Zero() {
				return d
			}
		}
	}
	return model.Date{}
}

func firstMoney(text string, cues []*regexp.Regexp) float64 {
	for _, re := range cues {
		for _, frag := range re.FindAllString(text, 4) {
			if v := ParseMoney(frag); v > 0 {
				return v
			}
		}
	}
	return 0
}

func payoutRange(text string) (low, high float64) {
	for _, re := range payoutCues {
		for _, frag := range re.FindAllString(text, 4) {
			all := reMoney.FindAllStringSubmatch(frag, 2)
			if len(all) == 0 {
				continue
			}
			v := ParseMoney(frag)
			if v <= 0 || v > 1_000_000 {
				continue // a fund total caught by a payout cue
			}
			if len(all) >= 2 {
				second := ParseMoney("$" + all[1][1] + " " + all[1][2])
				if second > 0 && second <= 1_000_000 {
					return v, second
				}
			}
			return 0, v // "up to $X" gives a ceiling and no floor
		}
	}
	return 0, 0
}

func guessStage(text string, s *model.Settlement) model.Stage {
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "claims period has closed"),
		strings.Contains(low, "deadline has passed"),
		strings.Contains(low, "no longer accepting claims"):
		return model.StageClaimsClose
	case strings.Contains(low, "file a claim"),
		strings.Contains(low, "submit a claim"),
		strings.Contains(low, "claim form"),
		strings.Contains(low, "claims process is open"):
		if !s.ClaimDeadline.Zero() && s.ClaimDeadline.DaysUntil() < 0 {
			return model.StageClaimsClose
		}
		return model.StageClaimsOpen
	case strings.Contains(low, "settlement has been approved"),
		strings.Contains(low, "settlement agreement"),
		strings.Contains(low, "approval hearing"):
		return model.StageSettled
	case strings.Contains(low, "has been certified"), strings.Contains(low, "certification order"):
		return model.StageCertified
	case strings.Contains(low, "statement of claim"), strings.Contains(low, "has been commenced"),
		strings.Contains(low, "was filed"):
		return model.StageFiled
	}
	return model.StageUnknown
}

func mentionsProof(text string) bool {
	low := strings.ToLower(text)
	for _, phrase := range []string{
		"proof of purchase", "supporting document", "receipts", "documentation is required",
		"must provide proof", "provide documentation", "medical records", "statutory declaration",
	} {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	return false
}
