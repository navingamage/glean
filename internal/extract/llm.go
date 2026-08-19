package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/navingamage/glean/internal/model"
)

// DefaultModel is what glean extracts with unless told otherwise.
const DefaultModel = "claude-opus-5"

// maxChars bounds how much of a page is sent. Notice pages that matter are well
// under this; the ones that are not are usually a firm's whole case archive
// concatenated onto one page, and the front of it is the part about the case.
const maxChars = 60_000

// ErrNoKey means no credentials were found. Callers fall back to the heuristic
// extractor rather than failing the scan, so glean runs end to end with no
// account at all: it will find deadlines and miss class definitions.
var ErrNoKey = errors.New("extract: no ANTHROPIC_API_KEY set")

const systemPrompt = `You read notices of Canadian class proceedings and reduce them to a structured record.

You are working for one person who wants to know which settlements they can claim from, so the record has to be good enough to act on and honest enough not to waste their evening.

Rules:

1. Extract, do not infer. Every date, dollar figure and clause must be on the page. If a deadline is not stated, leave it empty. An invented deadline is worse than a missing one, because a missing one shows up as a question and an invented one shows up as a plan.

2. The class definition is the important part. Decompose it into clauses a person could answer yes or no about. "All persons resident in Canada who purchased packaged bread at a Loblaw banner store between January 1, 2001 and December 31, 2021" is a residency clause plus one group of purchase clauses, one per banner named, all sharing a group.

3. Alternatives share a group; requirements do not. Getting this backwards either demands that someone shopped at nine stores at once, or lets anyone who shopped anywhere into a class defined by residency. Read the conjunctions in the definition carefully.

4. List banner and brand names as aliases when a parent company is named, and vice versa. People remember the store on the sign, not the holding company on the receipt.

5. Distinguish "a settlement exists" from "you can claim today". stage is claims-open only when there is a live claim form or portal and the deadline has not passed.

6. Note honestly what proof is needed and how long the form takes. This is what decides whether a claim is worth filing.

7. Set is_class_action false for pages that are not about one specific proceeding, and stop there.

Prices and amounts are Canadian dollars unless the page says otherwise.`

// Client wraps the extraction model.
type Client struct {
	api    anthropic.Client
	Model  string
	Effort anthropic.OutputConfigEffort
}

// NewClient builds an extractor. An empty key falls back to ANTHROPIC_API_KEY.
func NewClient(apiKey, modelID string) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, ErrNoKey
	}
	return newClient(modelID, option.WithAPIKey(apiKey)), nil
}

func newClient(modelID string, opts ...option.RequestOption) *Client {
	if modelID == "" {
		modelID = DefaultModel
	}
	return &Client{
		api:    anthropic.NewClient(opts...),
		Model:  modelID,
		Effort: anthropic.OutputConfigEffortMedium,
	}
}

// wire mirrors outputSchema. It exists so that a change to the schema fails to
// compile here rather than silently dropping a field at run time.
type wire struct {
	IsClassAction bool     `json:"is_class_action"`
	Title         string   `json:"title"`
	CaseName      string   `json:"case_name"`
	Court         string   `json:"court"`
	Defendants    []string `json:"defendants"`
	Jurisdiction  []string `json:"jurisdiction"`
	Stage         string   `json:"stage"`
	Summary       string   `json:"summary"`
	ClassText     string   `json:"class_text"`
	Criteria      []struct {
		Kind       string   `json:"kind"`
		Entity     string   `json:"entity"`
		Label      string   `json:"label"`
		Aliases    []string `json:"aliases"`
		From       string   `json:"from"`
		Until      string   `json:"until"`
		Text       string   `json:"text"`
		Group      string   `json:"group"`
		Prevalence float64  `json:"prevalence"`
	} `json:"criteria"`
	ClaimsOpenOn       string  `json:"claims_open_on"`
	ClaimDeadline      string  `json:"claim_deadline"`
	OptOutDeadline     string  `json:"opt_out_deadline"`
	SettlementFund     float64 `json:"settlement_fund"`
	PayoutLow          float64 `json:"payout_low"`
	PayoutHigh         float64 `json:"payout_high"`
	PayoutNote         string  `json:"payout_note"`
	EstimatedClassSize int     `json:"estimated_class_size"`
	ProofRequired      bool    `json:"proof_required"`
	ProofNote          string  `json:"proof_note"`
	EffortMinutes      int     `json:"effort_minutes"`
	ClaimURL           string  `json:"claim_url"`
	Administrator      string  `json:"administrator"`
	Confidence         float64 `json:"confidence"`
}

// ErrNotClassAction means the page turned out to be something else. It is an
// ordinary outcome of a scan, not a failure.
var ErrNotClassAction = errors.New("extract: page is not about a class proceeding")

// Extract reads one page into a settlement record.
func (c *Client) Extract(ctx context.Context, pageURL, title, text string) (*model.Settlement, error) {
	if len(text) > maxChars {
		text = text[:maxChars]
	}
	user := fmt.Sprintf("URL: %s\nPage title: %s\n\n---\n%s", pageURL, title, text)

	resp, err := c.api.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.Model),
		MaxTokens: 8000,
		System: []anthropic.TextBlockParam{{
			Text: systemPrompt,
			// The system prompt and the schema are identical on every page in a
			// scan, so caching the prefix turns a few hundred extractions from a
			// few hundred full-price prompts into one.
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: c.Effort,
			Format: anthropic.JSONOutputFormatParam{Schema: outputSchema},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("extract %s: %w", pageURL, err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, fmt.Errorf("extract %s: model declined (%s)", pageURL, resp.StopDetails.Category)
	}

	var raw strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			raw.WriteString(t.Text)
		}
	}
	var w wire
	if err := json.Unmarshal([]byte(raw.String()), &w); err != nil {
		return nil, fmt.Errorf("extract %s: decoding model output: %w", pageURL, err)
	}
	if !w.IsClassAction {
		return nil, ErrNotClassAction
	}
	return w.settlement(pageURL), nil
}

func (w *wire) settlement(pageURL string) *model.Settlement {
	s := &model.Settlement{
		ID:                 model.NewID(pageURL),
		Title:              strings.TrimSpace(w.Title),
		CaseName:           strings.TrimSpace(w.CaseName),
		Court:              strings.TrimSpace(w.Court),
		Defendants:         w.Defendants,
		Summary:            strings.TrimSpace(w.Summary),
		ClassText:          strings.TrimSpace(w.ClassText),
		SettlementFund:     w.SettlementFund,
		PayoutLow:          w.PayoutLow,
		PayoutHigh:         w.PayoutHigh,
		PayoutNote:         strings.TrimSpace(w.PayoutNote),
		EstimatedClassSize: w.EstimatedClassSize,
		ProofRequired:      w.ProofRequired,
		ProofNote:          strings.TrimSpace(w.ProofNote),
		EffortMinutes:      w.EffortMinutes,
		URL:                model.CanonicalURL(pageURL),
		Administrator:      strings.TrimSpace(w.Administrator),
		Extractor:          "llm",
		Confidence:         w.Confidence,
	}

	s.Stage = model.Stage(w.Stage)
	if s.Stage == "" {
		s.Stage = model.StageUnknown
	}
	s.ClaimsOpenOn, _ = model.ParseDate(w.ClaimsOpenOn)
	s.ClaimDeadline, _ = model.ParseDate(w.ClaimDeadline)
	s.OptOutDeadline, _ = model.ParseDate(w.OptOutDeadline)

	// A claim URL is the one field a user acts on directly, so it is checked
	// rather than trusted. Anything that is not an absolute web URL is dropped.
	if u := strings.TrimSpace(w.ClaimURL); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		s.ClaimURL = u
	}

	for _, code := range w.Jurisdiction {
		if p := model.NormalizeProvince(code); p != "" {
			s.Jurisdiction = append(s.Jurisdiction, p)
		}
	}

	for _, c := range w.Criteria {
		kind := model.CriterionKind(c.Kind)
		if !model.ValidKind(kind) {
			kind = model.KindOther
		}
		from, _ := model.ParseDate(c.From)
		until, _ := model.ParseDate(c.Until)

		entity := c.Entity
		if kind == model.KindResidency {
			if p := model.NormalizeProvince(entity); p != "" {
				entity = p
			} else if p := model.NormalizeProvince(c.Label); p != "" {
				entity = p
			}
		} else {
			entity = model.NormalizeEntity(entity)
		}
		if entity == "" {
			continue // a clause with nothing to test is not a criterion
		}

		s.Criteria = append(s.Criteria, model.Criterion{
			Kind:       kind,
			Entity:     entity,
			Label:      strings.TrimSpace(c.Label),
			Aliases:    c.Aliases,
			Window:     model.Window{From: from, Until: until},
			Text:       strings.TrimSpace(c.Text),
			Group:      strings.TrimSpace(c.Group),
			Prevalence: c.Prevalence,
		})
	}

	if s.EffortMinutes <= 0 {
		s.EffortMinutes = 15
		if s.ProofRequired {
			s.EffortMinutes = 45
		}
	}
	return s
}
