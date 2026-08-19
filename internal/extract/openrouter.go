package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/navingamage/glean/internal/model"
)

// DefaultModel is what glean extracts with unless told otherwise.
//
// The job is narrow and mechanical, which is why a small model can do it: read a
// class definition and split it into clauses, with the conjunctions in the right
// places. It is not narrow enough for the very cheapest tier, because getting
// AND and OR backwards in a class definition produces a confidently wrong answer
// rather than a visibly bad one. `glean models` prints the live price list so
// this can be revisited whenever the discounts move.
const DefaultModel = "google/gemini-3.7-flash"

// DefaultBaseURL is OpenRouter's OpenAI-compatible endpoint.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// maxChars bounds how much of a page is sent. Notice pages that matter are well
// under this; the ones that are not are usually a firm's whole case archive
// concatenated onto one page, and the front of it is the part about the case.
const maxChars = 60_000

// ErrNoKey means no credentials were found. Callers fall back to the heuristic
// extractor rather than failing the scan, so glean runs end to end with no
// account at all: it will find deadlines and miss class definitions.
var ErrNoKey = errors.New("extract: no OPENROUTER_API_KEY set")

// ErrNotClassAction means the page turned out to be something else. It is an
// ordinary outcome of a scan, not a failure.
var ErrNotClassAction = errors.New("extract: page is not about a class proceeding")

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

Prices and amounts are Canadian dollars unless the page says otherwise.

Reply with JSON matching the schema and nothing else.`

// Usage accumulates what a run has spent, in US dollars as OpenRouter reports it.
//
// Reporting the number is the point. A scan that quietly costs four dollars a
// day is a scan someone turns off in a month; one that prints eleven cents at
// the end is one they leave running, which is the only way this program is worth
// anything.
type Usage struct {
	mu        sync.Mutex
	Calls     int
	InTokens  int
	OutTokens int
	Cost      float64
}

func (u *Usage) add(in, out int, cost float64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Calls++
	u.InTokens += in
	u.OutTokens += out
	u.Cost += cost
}

func (u *Usage) String() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Calls == 0 {
		return "no model calls"
	}
	return fmt.Sprintf("%d model call(s), %d in / %d out tokens, $%.4f",
		u.Calls, u.InTokens, u.OutTokens, u.Cost)
}

// Client talks to OpenRouter's chat completions endpoint.
//
// This is a hand-rolled client rather than an SDK because the surface glean uses
// is one POST with a JSON body, and carrying a vendor SDK for that would be the
// only dependency in the program.
type Client struct {
	Key     string
	Model   string
	BaseURL string
	HTTP    *http.Client

	// Referer and Title are the optional attribution headers OpenRouter uses to
	// identify a calling application. They cost nothing and make a support
	// conversation possible.
	Referer string
	Title   string

	MaxTokens int
	Usage     *Usage
}

// NewClient builds an extractor. An empty key falls back to OPENROUTER_API_KEY.
func NewClient(apiKey, modelID string) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		return nil, ErrNoKey
	}
	base := os.Getenv("OPENROUTER_BASE_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	if modelID == "" {
		modelID = DefaultModel
	}
	return &Client{
		Key:       apiKey,
		Model:     modelID,
		BaseURL:   strings.TrimSuffix(base, "/"),
		HTTP:      &http.Client{Timeout: 3 * time.Minute},
		Referer:   "https://github.com/navingamage/glean",
		Title:     "glean",
		MaxTokens: 8000,
		Usage:     &Usage{},
	}, nil
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature"`
	Usage          *usageRequest   `json:"usage,omitempty"`
}

type usageRequest struct {
	Include bool `json:"include"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		Cost             float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// Extract reads one page into a settlement record.
func (c *Client) Extract(ctx context.Context, pageURL, title, text string) (*model.Settlement, error) {
	if len(text) > maxChars {
		text = text[:maxChars]
	}
	user := fmt.Sprintf("URL: %s\nPage title: %s\n\n---\n%s", pageURL, title, text)

	body, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: user},
		},
		ResponseFormat: &responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchema{Name: "settlement", Strict: true, Schema: outputSchema},
		},
		MaxTokens: c.MaxTokens,
		// Extraction is not a creative task, and a class definition that comes
		// back differently on two runs of the same page would show up as a
		// spurious change in the log every night.
		Temperature: 0,
		Usage:       &usageRequest{Include: true},
	})
	if err != nil {
		return nil, fmt.Errorf("extract %s: %w", pageURL, err)
	}

	raw, err := c.post(ctx, body, pageURL)
	if err != nil {
		return nil, err
	}

	var w wire
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return nil, fmt.Errorf("extract %s: decoding model output: %w", pageURL, err)
	}
	if !w.IsClassAction {
		return nil, ErrNotClassAction
	}
	return w.settlement(pageURL), nil
}

// post sends one request, retrying on the failures that are worth retrying.
//
// A scan touches a few dozen pages in a burst, so rate limiting is expected
// rather than exceptional and has to be waited out rather than reported as an
// error. A 4xx that is not 429 is a bug in the request and retrying it just
// spends money slower.
func (c *Client) post(ctx context.Context, body []byte, pageURL string) (string, error) {
	const attempts = 4
	var lastErr error

	for attempt := range attempts {
		if attempt > 0 {
			delay := time.Duration(1<<attempt) * time.Second
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.BaseURL+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("Content-Type", "application/json")
		if c.Referer != "" {
			req.Header.Set("HTTP-Referer", c.Referer)
		}
		if c.Title != "" {
			req.Header.Set("X-Title", c.Title)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s: %s", resp.Status, snippet(payload))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("extract %s: %s: %s", pageURL, resp.Status, snippet(payload))
		}

		var out chatResponse
		if err := json.Unmarshal(payload, &out); err != nil {
			return "", fmt.Errorf("extract %s: %w", pageURL, err)
		}
		// OpenRouter reports some upstream failures inside a 200 body.
		if out.Error != nil {
			lastErr = fmt.Errorf("%s", out.Error.Message)
			continue
		}
		if len(out.Choices) == 0 {
			lastErr = errors.New("no choices returned")
			continue
		}
		if r := out.Choices[0].Message.Refusal; r != "" {
			return "", fmt.Errorf("extract %s: model declined: %s", pageURL, r)
		}
		if c.Usage != nil {
			c.Usage.add(out.Usage.PromptTokens, out.Usage.CompletionTokens, out.Usage.Cost)
		}
		if out.Choices[0].FinishReason == "length" {
			return "", fmt.Errorf("extract %s: output truncated at %d tokens", pageURL, c.MaxTokens)
		}
		return out.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("extract %s: after %d attempts: %w", pageURL, attempts, lastErr)
}

func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
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
