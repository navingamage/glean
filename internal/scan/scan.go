// Package scan is one pass over every source: discover, fetch, extract, store.
//
// The ordering of the cheap checks matters more than anything else in here. A
// scan looks at a few hundred pages a day, forever, and almost none of them have
// changed since yesterday. Conditional GET removes most of the bytes, the
// content hash removes most of the parsing, and a relevance score removes most
// of what would otherwise be a model call on a partner biography. What is left
// is the handful of pages that actually said something new.
package scan

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/navingamage/glean/internal/extract"
	"github.com/navingamage/glean/internal/fetch"
	"github.com/navingamage/glean/internal/htmlx"
	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/source"
	"github.com/navingamage/glean/internal/store"
)

type Options struct {
	// PerSource caps how many candidate pages are followed from one source in a
	// single pass. Without it, a firm with a decade of archived cases is a
	// thousand fetches on the first run.
	PerSource int
	// Workers is how many sources are walked at once. Per-host rate limiting
	// happens inside the fetch client, so this parallelises across sites without
	// hammering any one of them.
	Workers int
	// MinRelevance is the score below which a page is not worth extracting.
	MinRelevance int
	// Refetch ignores the content hash and re-extracts pages that have not
	// changed. Used after a change to the prompt or the schema.
	Refetch bool
}

func DefaultOptions() Options {
	return Options{PerSource: 40, Workers: 4, MinRelevance: 8}
}

// Result is what one pass did, in the terms a person cares about.
type Result struct {
	Candidates int
	Fetched    int
	Unchanged  int
	Irrelevant int
	Extracted  int
	New        []*model.Settlement
	Updated    map[string][]store.Change
	Errors     []error
}

func (r *Result) String() string {
	return fmt.Sprintf("%d candidates, %d fetched, %d unchanged, %d not relevant, %d extracted, %d new, %d updated, %d errors",
		r.Candidates, r.Fetched, r.Unchanged, r.Irrelevant, r.Extracted,
		len(r.New), len(r.Updated), len(r.Errors))
}

// Run walks every source once. It returns a result even when some sources
// failed: a scan that reaches fourteen of eighteen sites is a successful scan
// with four errors attached, not a failed one.
func Run(ctx context.Context, st *store.Store, sources []*source.Source, ex *extract.Client, opts Options) *Result {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	client := fetch.New(st.CacheDir())
	client.IgnoreCache = opts.Refetch

	res := &Result{Updated: map[string][]store.Change{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, opts.Workers)

	for _, src := range sources {
		wg.Add(1)
		go func(src *source.Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cands, errs := source.Discover(ctx, client, src, opts.PerSource)
			mu.Lock()
			res.Candidates += len(cands)
			for _, err := range errs {
				res.Errors = append(res.Errors, fmt.Errorf("%s: %w", src.ID, err))
			}
			mu.Unlock()

			for _, cand := range cands {
				if ctx.Err() != nil {
					return
				}
				out := visit(ctx, st, client, ex, src, cand, opts, &mu)
				mu.Lock()
				merge(res, out)
				mu.Unlock()
			}
		}(src)
	}
	wg.Wait()
	return res
}

// portalFor returns the claim portal worth a second fetch, or "" when the page
// already told us everything or points nowhere useful.
func portalFor(s *model.Settlement, doc, pageURL string) string {
	if !s.ClaimDeadline.Zero() && s.PayoutHigh > 0 {
		return "" // nothing left to learn that is worth a request
	}
	if s.ClaimURL != "" {
		return s.ClaimURL // the extractor already found it; verify it and read it
	}
	return extract.FindPortal(doc, pageURL)
}

// followPortal fetches the portal and backfills from it, reporting whether the
// page turned out to be about this settlement at all. A portal that reads as
// unrelated is discarded rather than merged, because importing a stranger's
// deadline is worse than having none.
func followPortal(ctx context.Context, client *fetch.Client, s *model.Settlement, portal string) bool {
	resp, err := client.Get(ctx, portal)
	if err != nil || !resp.IsHTML() {
		return false
	}
	text := htmlx.ExtractText(resp.Body)
	if extract.Relevance(text) < 6 {
		return false
	}
	before := s.ClaimDeadline
	extract.Backfill(s, text)
	if s.ClaimDeadline != before && !s.ClaimDeadline.Zero() {
		// The administrator is the authority on its own deadline, so a date found
		// here displaces the confidence the case page was carrying.
		if s.Confidence < 0.5 {
			s.Confidence = 0.5
		}
	}
	return true
}

// outcome is one candidate page's contribution to the result.
type outcome struct {
	fetched    bool
	unchanged  bool
	irrelevant bool
	extracted  bool
	isNew      *model.Settlement
	changes    []store.Change
	changedID  string
	err        error
}

func merge(res *Result, o outcome) {
	if o.fetched {
		res.Fetched++
	}
	if o.unchanged {
		res.Unchanged++
	}
	if o.irrelevant {
		res.Irrelevant++
	}
	if o.extracted {
		res.Extracted++
	}
	if o.isNew != nil {
		res.New = append(res.New, o.isNew)
	}
	if len(o.changes) > 0 {
		res.Updated[o.changedID] = o.changes
	}
	if o.err != nil {
		res.Errors = append(res.Errors, o.err)
	}
}

func visit(ctx context.Context, st *store.Store, client *fetch.Client, ex *extract.Client,
	src *source.Source, cand source.Candidate, opts Options, mu *sync.Mutex) outcome {

	resp, err := client.Get(ctx, cand.URL)
	if err != nil {
		if errors.Is(err, fetch.ErrDisallowed) {
			return outcome{}
		}
		return outcome{err: fmt.Errorf("%s: %w", src.ID, err)}
	}
	out := outcome{fetched: true}
	if !resp.IsHTML() {
		return out
	}

	// A page that has not changed since the last pass, and that we already have a
	// record for, needs nothing further. The hash check is what makes a daily
	// scan nearly free.
	mu.Lock()
	known := st.Settlement(model.NewID(cand.URL)) != nil
	mu.Unlock()
	if known && !resp.Changed && !opts.Refetch {
		out.unchanged = true
		return out
	}

	text := htmlx.ExtractText(resp.Body)
	if extract.Relevance(text) < opts.MinRelevance {
		out.irrelevant = true
		return out
	}

	title := cand.Title
	if t := htmlx.Title(resp.Body); t != "" {
		title = t
	}
	title = htmlx.CleanTitle(title, cand.URL)

	var s *model.Settlement
	if ex != nil {
		s, err = ex.Extract(ctx, cand.URL, title, text)
		switch {
		case errors.Is(err, extract.ErrNotClassAction):
			out.irrelevant = true
			return out
		case err != nil:
			out.err = fmt.Errorf("%s: %w", src.ID, err)
			// Fall through to the heuristic rather than losing the page. A model
			// outage should degrade the record, not drop the settlement.
		}
	}
	if s == nil {
		s = &model.Settlement{ID: model.NewID(cand.URL), Title: title, URL: model.CanonicalURL(cand.URL)}
		extract.Heuristic(s, text)
	} else {
		extract.Backfill(s, text)
	}
	if s.Title == "" {
		s.Title = title
	}

	// Follow the claim portal when the case page did not answer the question the
	// user is actually asking. A firm's page says a settlement was approved; the
	// administrator's page says when claims close, and the deadline is the whole
	// reason to run this program. One hop, only off-site, only when something is
	// still missing.
	if portal := portalFor(s, resp.Body, cand.URL); portal != "" {
		if enriched := followPortal(ctx, client, s, portal); enriched {
			s.ClaimURL = portal
		}
	}

	s.SourceID = src.ID
	s.ContentHash = resp.Hash
	if len(s.Jurisdiction) == 0 {
		s.Jurisdiction = src.Jurisdiction
	}
	out.extracted = true

	mu.Lock()
	isNew, changes, err := st.Upsert(s)
	mu.Unlock()
	if err != nil {
		out.err = err
		return out
	}
	if isNew {
		out.isNew = s
	}
	if len(changes) > 0 {
		out.changedID, out.changes = s.ID, changes
	}
	return out
}
