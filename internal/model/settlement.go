package model

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// Stage is where a proceeding sits in its lifecycle. Only some stages are worth
// anything: a filed-but-uncertified action is news, whereas ClaimsOpen is money
// on a table with a clock running.
type Stage string

const (
	StageFiled       Stage = "filed"
	StageCertified   Stage = "certified"
	StageSettled     Stage = "settled"       // approved, claims process not yet open
	StageClaimsOpen  Stage = "claims-open"   // the only stage that pays
	StageClaimsClose Stage = "claims-closed" // window has shut
	StageConcluded   Stage = "concluded"
	StageUnknown     Stage = "unknown"
)

// Actionable reports whether a claim can plausibly be filed right now.
func (s Stage) Actionable() bool { return s == StageClaimsOpen || s == StageSettled }

// CriterionKind partitions the class definition into the kinds of fact a person
// can actually be asked about. The set is deliberately small: every extra kind is
// another axis the matcher has to reason over and another question the user has
// to answer, and in practice Canadian consumer classes are defined by where you
// lived, what you bought, who you banked or subscribed with, and what you drove,
// took or were treated with.
type CriterionKind string

const (
	KindResidency  CriterionKind = "residency"
	KindPurchase   CriterionKind = "purchase"
	KindAccount    CriterionKind = "account"
	KindService    CriterionKind = "service"
	KindEmployment CriterionKind = "employment"
	KindVehicle    CriterionKind = "vehicle"
	KindDevice     CriterionKind = "device"
	KindMedical    CriterionKind = "medical"
	KindOther      CriterionKind = "other"
)

var allKinds = []CriterionKind{
	KindResidency, KindPurchase, KindAccount, KindService,
	KindEmployment, KindVehicle, KindDevice, KindMedical, KindOther,
}

// ValidKind reports whether k is one the matcher understands. Anything else the
// extractor invents is folded into KindOther rather than silently dropped.
func ValidKind(k CriterionKind) bool {
	for _, v := range allKinds {
		if v == k {
			return true
		}
	}
	return false
}

// Criterion is one clause of a class definition, reduced to something a profile
// can be tested against.
//
// Group is what makes alternatives work. A bread price-fixing class covers
// purchases at Loblaws, No Frills, Zehrs, Independent and half a dozen other
// banners; those arrive as separate criteria sharing a Group, are OR'd together,
// and the resulting group is AND'd with everything else. Without it the matcher
// would demand you shopped at all of them.
type Criterion struct {
	Kind    CriterionKind `json:"kind"`
	Entity  string        `json:"entity"`            // normalized key, e.g. "cibc"
	Label   string        `json:"label"`             // human name, e.g. "CIBC"
	Aliases []string      `json:"aliases,omitempty"` // other names the same thing goes by
	Window  Window        `json:"window"`
	Text    string        `json:"text"` // the clause as the source words it
	Group   string        `json:"group,omitempty"`

	// Prevalence is the extractor's estimate of the fraction of Canadian adults
	// who satisfy this criterion, used to price an unanswered question. "Held a
	// bank account at one of the big five" is near 0.9; "was prescribed a
	// specific recalled drug" is near 0.001. It only ever affects ranking, never
	// the verdict.
	Prevalence float64 `json:"prevalence"`
}

// Settlement is one proceeding as glean understands it. Everything here is
// derived from a public web page, so every field is provisional and the ones
// that decide whether to spend an evening on a claim form carry Confidence with
// them.
type Settlement struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	CaseName     string   `json:"case_name,omitempty"`
	Defendants   []string `json:"defendants,omitempty"`
	Court        string   `json:"court,omitempty"`
	Jurisdiction []string `json:"jurisdiction,omitempty"` // province codes, or "CA"

	Stage   Stage  `json:"stage"`
	Summary string `json:"summary"`

	ClassText string      `json:"class_text,omitempty"`
	Criteria  []Criterion `json:"criteria,omitempty"`

	ClaimsOpenOn   Date `json:"claims_open_on"`
	ClaimDeadline  Date `json:"claim_deadline"`
	OptOutDeadline Date `json:"opt_out_deadline"`

	// SettlementFund is the total in Canadian dollars, zero when unstated.
	SettlementFund float64 `json:"settlement_fund"`
	// PayoutLow and PayoutHigh bracket what one claimant can expect. A great many
	// notices state neither, in which case the fund and a guess at class size are
	// all there is to go on.
	PayoutLow  float64 `json:"payout_low"`
	PayoutHigh float64 `json:"payout_high"`
	PayoutNote string  `json:"payout_note,omitempty"`
	// EstimatedClassSize lets a per-claimant figure be recovered from a fund when
	// the notice states only the total. It is a guess and is treated as one.
	EstimatedClassSize int `json:"estimated_class_size"`

	// ProofRequired separates the five-minute attestation claims from the ones
	// that want two decades of grocery receipts. It is the single biggest driver
	// of whether a claim is worth filing.
	ProofRequired bool   `json:"proof_required"`
	ProofNote     string `json:"proof_note,omitempty"`
	EffortMinutes int    `json:"effort_minutes"`

	URL           string `json:"url"`                 // page glean read
	ClaimURL      string `json:"claim_url,omitempty"` // official claims portal
	Administrator string `json:"administrator,omitempty"`

	SourceID   string  `json:"source_id"`
	Extractor  string  `json:"extractor"` // "llm" or "heuristic"
	Confidence float64 `json:"confidence"`

	FirstSeen   Date   `json:"first_seen"`
	LastSeen    Date   `json:"last_seen"`
	ContentHash string `json:"content_hash,omitempty"`
}

// CanonicalURL cleans a URL without changing where it points. Fragments and
// tracking parameters are removed and a trailing slash is dropped, because none
// of those change the page; the scheme and host are left exactly as the site
// wrote them, because both of those very much can.
//
// This is the form that gets fetched. Rewriting http to https here would be a
// quiet way to lose every site that has not migrated, and stripping "www" would
// break the hosts that only answer on it.
func CanonicalURL(raw string) string {
	raw = stripDoubledScheme(strings.TrimSpace(raw))
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || lk == "fbclid" || lk == "gclid" || lk == "ref" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}
	return u.String()
}

// IdentityURL is the aggressive normalization: one settlement reached over http
// and over https, with and without "www", is one settlement. Nothing fetches
// this form; it exists so that two spellings of the same page collapse to one
// record and so that source patterns can be written one way.
func IdentityURL(raw string) string {
	u, err := url.Parse(CanonicalURL(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	return u.String()
}

// stripDoubledScheme repairs hrefs of the form "https://https://example.com/x",
// which are produced by content management systems that prepend a scheme to a
// field an author already filled in with one. Left alone they resolve to a host
// of "https:" and quietly disappear from the candidate list, which is a bad way
// to lose a settlement page.
func stripDoubledScheme(raw string) string {
	for {
		lower := strings.ToLower(raw)
		switch {
		case strings.HasPrefix(lower, "https://https://"), strings.HasPrefix(lower, "https://http://"):
			raw = raw[len("https://"):]
		case strings.HasPrefix(lower, "http://https://"), strings.HasPrefix(lower, "http://http://"):
			raw = raw[len("http://"):]
		default:
			return raw
		}
	}
}

// NewID derives a stable identifier from the canonical URL. Content-addressing
// the identity means a re-scan of the same page updates a record instead of
// duplicating it, and it survives the store being deleted and rebuilt.
func NewID(rawURL string) string {
	sum := sha256.Sum256([]byte(IdentityURL(rawURL)))
	return hex.EncodeToString(sum[:8])
}

// DaysLeft is days until the claim deadline; math.MaxInt32 when there is no
// known deadline, so that unknown-deadline settlements sort last by urgency
// rather than appearing to have expired.
func (s *Settlement) DaysLeft() int {
	if s.ClaimDeadline.Zero() {
		return 1 << 30
	}
	return s.ClaimDeadline.DaysUntil()
}

// Expired reports whether the claim window has demonstrably closed.
func (s *Settlement) Expired() bool {
	if s.Stage == StageClaimsClose || s.Stage == StageConcluded {
		return true
	}
	return !s.ClaimDeadline.Zero() && s.ClaimDeadline.DaysUntil() < 0
}

// Groups partitions criteria into alternative groups. Criteria with an empty
// Group are each their own group, since an unlabelled clause is a requirement in
// its own right.
func (s *Settlement) Groups() [][]Criterion {
	var out [][]Criterion
	index := map[string]int{}
	for _, c := range s.Criteria {
		if c.Group == "" {
			out = append(out, []Criterion{c})
			continue
		}
		if i, ok := index[c.Group]; ok {
			out[i] = append(out[i], c)
			continue
		}
		index[c.Group] = len(out)
		out = append(out, []Criterion{c})
	}
	return out
}
