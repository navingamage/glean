package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/navingamage/glean/internal/model"
)

// stub answers the chat completions endpoint with a canned structured output,
// and hands the request it received back to the test.
//
// This exercises everything glean owns on both sides of the model: that the
// strict schema is sent, that the response decodes into the fields the matcher
// reads, and that entity names and provinces are normalized on the way in. What
// it cannot test is the model's judgment about a class definition, which is the
// one part that has to be checked against real notices by hand.
func stub(t *testing.T, payload any) (*Client, *map[string]any) {
	t.Helper()
	captured := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)

		content, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1200,"completion_tokens":400,"cost":0.0009}}`,
			mustJSONString(string(content)))
	}))
	t.Cleanup(srv.Close)
	return testClient(srv.URL), &captured
}

func testClient(base string) *Client {
	return &Client{
		Key: "test", Model: "google/gemini-3.7-flash", BaseURL: base,
		HTTP: &http.Client{Timeout: 10 * time.Second}, MaxTokens: 8000, Usage: &Usage{},
	}
}

func mustJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func breadPayload() map[string]any {
	return map[string]any{
		"is_class_action": true,
		"title":           "Loblaw packaged bread price-fixing",
		"case_name":       "David v. Loblaw Companies Ltd.",
		"court":           "Ontario Superior Court of Justice",
		"defendants":      []string{"Loblaw Companies Limited"},
		"jurisdiction":    []string{"Canada"},
		"stage":           "claims-open",
		"summary":         "An alleged industry-wide arrangement to fix the price of packaged bread.",
		"class_text":      "All persons resident in Canada who purchased packaged bread.",
		"criteria": []map[string]any{
			{"kind": "residency", "entity": "canada", "label": "Canada", "aliases": []string{},
				"from": "", "until": "", "text": "resident in Canada", "group": "", "prevalence": 1.0},
			{"kind": "purchase", "entity": "No Frills Inc.", "label": "No Frills",
				"aliases": []string{"Loblaws"}, "from": "2001-01-01", "until": "2021-12-31",
				"text": "purchased packaged bread at No Frills", "group": "banner", "prevalence": 0.5},
			{"kind": "bogus-kind", "entity": "something", "label": "Something", "aliases": []string{},
				"from": "", "until": "", "text": "", "group": "", "prevalence": 0.1},
			{"kind": "purchase", "entity": "", "label": "", "aliases": []string{},
				"from": "", "until": "", "text": "", "group": "", "prevalence": 0.1},
		},
		"claims_open_on": "2026-01-05", "claim_deadline": "2027-06-30", "opt_out_deadline": "",
		"settlement_fund": 500000000.0, "payout_low": 25.0, "payout_high": 25.0,
		"payout_note": "flat amount", "estimated_class_size": 12000000,
		"proof_required": false, "proof_note": "", "effort_minutes": 8,
		"claim_url": "https://canadianbreadsettlement.ca/claim", "administrator": "RicePoint",
		"confidence": 0.85,
	}
}

func TestExtractSendsAStrictSchemaAndNormalizesTheResult(t *testing.T) {
	c, req := stub(t, breadPayload())
	s, err := c.Extract(context.Background(), "https://cfmlawyers.ca/class-action/bread",
		"Bread", "a long notice about packaged bread")
	if err != nil {
		t.Fatal(err)
	}

	// Without a strict schema the model is free to return prose, and every field
	// below becomes a parsing problem instead of a value.
	rf, ok := (*req)["response_format"].(map[string]any)
	if !ok {
		t.Fatal("request carried no response_format")
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format type = %v", rf["type"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatal("response_format carried no json_schema")
	}
	if js["strict"] != true {
		t.Error("schema was not marked strict")
	}
	if _, ok := js["schema"].(map[string]any); !ok {
		t.Error("request carried no schema body")
	}
	if got := (*req)["temperature"]; got != 0.0 {
		t.Errorf("temperature = %v, want 0: a class definition that varies between runs shows up as a spurious nightly change", got)
	}

	if s.ID != model.NewID("https://cfmlawyers.ca/class-action/bread") {
		t.Error("id is not derived from the page URL")
	}
	if s.Stage != model.StageClaimsOpen {
		t.Errorf("stage = %q", s.Stage)
	}
	if s.ClaimDeadline.String() != "2027-06-30" {
		t.Errorf("deadline = %q", s.ClaimDeadline)
	}
	if len(s.Jurisdiction) != 1 || s.Jurisdiction[0] != "CA" {
		t.Errorf("jurisdiction = %v, want [CA]", s.Jurisdiction)
	}

	// Two of the four criteria are junk and must be handled rather than trusted:
	// an unknown kind is folded into "other", and a clause with nothing to test
	// is dropped entirely.
	if len(s.Criteria) != 3 {
		t.Fatalf("kept %d criteria, want 3: %+v", len(s.Criteria), s.Criteria)
	}
	if s.Criteria[0].Entity != "CA" {
		t.Errorf("residency entity = %q, want the province code CA", s.Criteria[0].Entity)
	}
	if s.Criteria[1].Entity != "loblaw" {
		t.Errorf("purchase entity = %q, want the normalized banner key loblaw", s.Criteria[1].Entity)
	}
	if s.Criteria[1].Group != "banner" {
		t.Error("alternative group was lost, which would demand every banner at once")
	}
	if s.Criteria[2].Kind != model.KindOther {
		t.Errorf("unknown kind became %q, want other", s.Criteria[2].Kind)
	}

	// Spend has to be visible or nobody keeps the scan running.
	if c.Usage.Calls != 1 || c.Usage.Cost != 0.0009 {
		t.Errorf("usage = %+v", c.Usage)
	}
}

func TestExtractRejectsANonClassActionPage(t *testing.T) {
	p := breadPayload()
	p["is_class_action"] = false
	c, _ := stub(t, p)
	_, err := c.Extract(context.Background(), "https://firm.ca/careers", "Careers", "we are hiring")
	if !errors.Is(err, ErrNotClassAction) {
		t.Fatalf("err = %v, want ErrNotClassAction", err)
	}
}

func TestExtractRefusesAFabricatedClaimURL(t *testing.T) {
	// The claim URL is the one field a person clicks straight through to, so a
	// relative path or a bare hostname invented by the model is dropped rather
	// than resolved into something that looks official.
	for _, bad := range []string{"/claim", "canadianbreadsettlement.ca", "javascript:alert(1)", ""} {
		p := breadPayload()
		p["claim_url"] = bad
		c, _ := stub(t, p)
		s, err := c.Extract(context.Background(), "https://firm.ca/x", "x", "y")
		if err != nil {
			t.Fatal(err)
		}
		if s.ClaimURL != "" {
			t.Errorf("claim_url %q was kept as %q", bad, s.ClaimURL)
		}
	}
}

func TestRateLimitIsWaitedOutAndBadRequestsAreNot(t *testing.T) {
	// A scan touches a few dozen pages in a burst, so a 429 is expected rather
	// than exceptional. A 400 is a bug in the request, and retrying it just
	// spends money more slowly.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
			return
		}
		content, _ := json.Marshal(breadPayload())
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}],"usage":{}}`,
			mustJSONString(string(content)))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := testClient(srv.URL).Extract(ctx, "https://firm.ca/x", "x", "y"); err != nil {
		t.Fatalf("a 429 should have been waited out: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("made %d calls, want 2", got)
	}

	var bad atomic.Int32
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bad.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"model does not support response_format"}}`)
	}))
	defer badSrv.Close()

	_, err := testClient(badSrv.URL).Extract(context.Background(), "https://firm.ca/x", "x", "y")
	if err == nil {
		t.Fatal("a 400 should be reported, not swallowed")
	}
	if bad.Load() != 1 {
		t.Errorf("retried a 400 %d times", bad.Load())
	}
}

func TestUpstreamErrorInsideA200IsRetriedThenReported(t *testing.T) {
	// OpenRouter reports some upstream provider failures in the body of a 200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"message":"upstream provider is down","code":502}}`)
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	c.HTTP.Timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	_, err := c.Extract(ctx, "https://firm.ca/x", "x", "y")
	if err == nil {
		t.Fatal("an error body inside a 200 must not read as success")
	}
}

func TestNewClientWithoutAKeyIsARecoverableCondition(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	if _, err := NewClient("", ""); !errors.Is(err, ErrNoKey) {
		t.Fatalf("err = %v, want ErrNoKey", err)
	}
}
