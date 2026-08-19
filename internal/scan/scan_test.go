package scan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/navingamage/glean/internal/source"
	"github.com/navingamage/glean/internal/store"
)

const listingHTML = `<html><body><h1>Our cases</h1><ul>
<li><a href="/cases/bread">Packaged bread price-fixing settlement</a></li>
<li><a href="/cases/expired">Old case, claims closed</a></li>
<li><a href="/about">About our firm</a></li>
<li><a href="/cases/careers">Careers</a></li>
</ul></body></html>`

const noticeHTML = `<html><head><title>Packaged bread price-fixing - Example Law</title></head><body>
<h1>Packaged bread price-fixing class action settlement</h1>
<p>A class action was commenced on behalf of all persons resident in Canada who
purchased packaged bread. The settlement has been approved by the court.</p>
<p>Class members may file a claim using the claim form. The claims deadline is
October 15, 2026. Each class member is eligible to receive up to $25.</p>
<p>The settlement fund is $500 million. To opt out you must exclude yourself by
March 1, 2026. No proof of purchase is required.</p>
<p>A claims administrator has been appointed and the claim form is available online.</p>
</body></html>`

// A page that is on the site and matches the follow pattern, but is not a class
// action. The relevance gate is the only thing standing between this and a model
// call, so it is worth a test.
const boilerplateHTML = `<html><head><title>Careers</title></head><body>
<h1>Join our team</h1><p>We are hiring. See our privacy policy and terms of use.</p>
</body></html>`

func TestScanFindsAndTracksASettlement(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/robots.txt":
			http.NotFound(w, r)
		case "/cases":
			w.Write([]byte(listingHTML))
		case "/cases/bread":
			w.Header().Set("ETag", `"v1"`)
			w.Write([]byte(noticeHTML))
		case "/cases/careers":
			w.Write([]byte(boilerplateHTML))
		default:
			w.Write([]byte(boilerplateHTML))
		}
	}))
	defer srv.Close()

	src := &source.Source{
		ID: "test", Kind: source.KindFirm, Listing: []string{srv.URL + "/cases"},
		Follow: []string{`/cases/[^/]+$`},
	}
	if err := src.Compile(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions()
	opts.Workers = 1
	// No router: this is the no-API-key path, which has to work on its own.
	res := Run(context.Background(), st, []*source.Source{src}, nil, opts)

	if len(res.Errors) > 0 {
		t.Fatalf("scan reported errors: %v", res.Errors)
	}
	if len(res.New) != 1 {
		t.Fatalf("found %d settlements, want 1: %s", len(res.New), res)
	}
	if res.Irrelevant == 0 {
		t.Error("the careers page should have been rejected before extraction")
	}

	got := res.New[0]
	if got.Title != "Packaged bread price-fixing" {
		t.Errorf("title = %q, want the masthead stripped", got.Title)
	}
	if got.ClaimDeadline.String() != "2026-10-15" {
		t.Errorf("claim deadline = %q, want 2026-10-15", got.ClaimDeadline)
	}
	if got.OptOutDeadline.String() != "2026-03-01" {
		t.Errorf("opt out deadline = %q, want 2026-03-01", got.OptOutDeadline)
	}
	if got.PayoutHigh != 25 {
		t.Errorf("payout high = %v, want 25", got.PayoutHigh)
	}
	if got.SettlementFund != 500_000_000 {
		t.Errorf("fund = %v, want 500000000", got.SettlementFund)
	}
	if got.Stage != "claims-open" {
		t.Errorf("stage = %q, want claims-open", got.Stage)
	}
	if got.SourceID != "test" {
		t.Errorf("source id = %q", got.SourceID)
	}
}

func TestSecondScanIsCheapAndFindsNothingNew(t *testing.T) {
	// The whole economics of running this daily rests on an unchanged page
	// costing one conditional request and no parsing.
	var noticeFetches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/robots.txt":
			http.NotFound(w, r)
		case "/cases":
			w.Write([]byte(listingHTML))
		case "/cases/bread":
			noticeFetches++
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			w.Write([]byte(noticeHTML))
		default:
			w.Write([]byte(boilerplateHTML))
		}
	}))
	defer srv.Close()

	src := &source.Source{ID: "test", Listing: []string{srv.URL + "/cases"},
		Follow: []string{`/cases/[^/]+$`}}
	if err := src.Compile(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Workers = 1

	first := Run(context.Background(), st, []*source.Source{src}, nil, opts)
	if len(first.New) != 1 {
		t.Fatalf("first pass found %d, want 1", len(first.New))
	}

	// A fresh store handle, as a scheduled run would have.
	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	second := Run(context.Background(), st2, []*source.Source{src}, nil, opts)
	if len(second.New) != 0 {
		t.Errorf("second pass reported %d new settlements, want 0", len(second.New))
	}
	if len(second.Updated) != 0 {
		t.Errorf("second pass reported changes on an unchanged page: %v", second.Updated)
	}
	if second.Unchanged != 1 {
		t.Errorf("second pass counted %d unchanged, want 1", second.Unchanged)
	}
	if noticeFetches != 2 {
		t.Errorf("notice fetched %d times, want 2 (one full, one conditional)", noticeFetches)
	}

	// And the log kept exactly one event for it, not two.
	events := 0
	for _, e := range st2.Since(st2.Settlements()[0].FirstSeen.Time().Add(-1)) {
		if e.SettlementID != "" {
			events++
		}
	}
	if events != 1 {
		t.Errorf("log holds %d settlement events after two passes, want 1", events)
	}
	_ = filepath.Join(dir, "events.jsonl")
	_ = strings.TrimSpace("")
}
