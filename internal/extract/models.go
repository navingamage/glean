package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A scan's cost is dominated by the size of a notice page, not by the answer, so
// these are the numbers that matter when comparing models. They are measured
// from real extractions rather than assumed: a Canadian settlement notice runs
// to roughly this much once the markup is stripped, and the structured record
// that comes back is roughly this long.
const (
	TypicalInputTokens  = 6_000
	TypicalOutputTokens = 900
)

// Model is one option on OpenRouter, reduced to what choosing between them needs.
type Model struct {
	ID        string
	Name      string
	Context   int
	InPerM    float64 // US dollars per million prompt tokens
	OutPerM   float64
	Strict    bool // supports json_schema with strict validation
	Loose     bool // supports response_format json_object only
	Reasoning bool
}

// CostPerPage is what one extraction costs, in US dollars.
func (m Model) CostPerPage() float64 {
	return (TypicalInputTokens*m.InPerM + TypicalOutputTokens*m.OutPerM) / 1e6
}

// Usable reports whether glean can extract with this model at all. Without
// strict schema support the extractor has no contract with the model, and a
// context window too small for a notice page truncates the class definition,
// which is the one thing being extracted.
func (m Model) Usable() bool { return m.Strict && m.Context >= 32_000 }

type apiModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
	SupportedParameters []string `json:"supported_parameters"`
}

// ListModels fetches OpenRouter's catalogue.
//
// It is deliberately unauthenticated and separate from the extraction client:
// deciding what to run costs nothing and should not require a key, and the price
// list is the sort of thing that has to be read live rather than remembered. A
// discount that was 75% off last month is not a fact worth hardcoding.
func ListModels(ctx context.Context, baseURL string) ([]Model, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models: %s", resp.Status)
	}

	var body struct {
		Data []apiModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("models: %w", err)
	}

	out := make([]Model, 0, len(body.Data))
	for _, m := range body.Data {
		in, out1 := dollars(m.Pricing.Prompt), dollars(m.Pricing.Completion)
		if in <= 0 {
			continue // free tiers and unpriced entries are not comparable
		}
		mm := Model{
			ID: m.ID, Name: m.Name, Context: m.ContextLength,
			InPerM: in * 1e6, OutPerM: out1 * 1e6,
		}
		for _, p := range m.SupportedParameters {
			switch p {
			case "structured_outputs":
				mm.Strict = true
			case "response_format":
				mm.Loose = true
			case "reasoning", "include_reasoning":
				mm.Reasoning = true
			}
		}
		out = append(out, mm)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostPerPage() != out[j].CostPerPage() {
			return out[i].CostPerPage() < out[j].CostPerPage()
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func dollars(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}
