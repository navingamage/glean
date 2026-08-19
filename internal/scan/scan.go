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
	"crypto/sha256"
	"encoding/hex"
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
	Prime      int // extractions that went to the capable model
	Escalated  int // cheap extractions the capable model had to redo
	New        []*model.Settlement
	Updated    map[string][]store.Change
	Errors     []error
}

func (r *Result) String() string {
	return fmt.Sprintf("%d candidates, %d fetched, %d unchanged, %d not relevant, %d extracted (%d prime, %d escalated), %d new, %d updated, %d errors",
		r.Candidates, r.Fetched, r.Unchanged, r.Irrelevant, r.Extracted, r.Prime, r.Escalated,
		len(r.New), len(r.Updated), len(r.Errors))
}

// Run walks every source once. It returns a result even when some sources
// failed: a scan that reaches fourteen of eighteen sites is a successful scan
// with four errors attached, not a failed one.
func Run(ctx context.Context, st *store.Store, sources []*source.Source, router *extract.Router, opts Options) *Result {
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
				out := visit(ctx, st, client, router, src, cand, opts, &mu)
				mu.Lock()
				merge(res, out)
				mu.Unlock()
			}
		}(src)
	}
	wg.Wait()
	if err := st.SaveHashes(); err != nil {
		res.Errors = append(res.Errors, err)
	}
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
func followPortal(ctx context.Context, client *fetch.Client, router *extract.Router,
	s *model.Settlement, portal string) bool {

	resp, err := client.Get(ctx, portal)
	if err != nil || !resp.IsHTML() {
		return false
	}
	text := htmlx.ExtractText(resp.Body)
	if extract.Relevance(text) < 6 {
		return false
	}
	before := s.ClaimDeadline

	// A claim portal exists to state one deadline and one set of amounts. That
	// is transcription, so it goes to the cheap tier: the class definition has
	// already been read from the case page, and re-reading it here would be
	// paying twice for the same paragraph.
	if router != nil {
		if p, err := router.For(extract.Cheap).Extract(ctx, portal, s.Title, text); err == nil {
			mergePortal(s, p)
		}
	}
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

// mergePortal takes from an administrator's page only the facts it is the
// authority on. Its title, summary and class definition are not imported: a
// portal page often covers several settlements at once, and letting it overwrite
// the case description would replace something specific with something generic.
func mergePortal(s, portal *model.Settlement) {
	if s.ClaimDeadline.Zero() && !portal.ClaimDeadline.Zero() {
		s.ClaimDeadline = portal.ClaimDeadline
	}
	if s.OptOutDeadline.Zero() && !portal.OptOutDeadline.Zero() {
		s.OptOutDeadline = portal.OptOutDeadline
	}
	if s.ClaimsOpenOn.Zero() && !portal.ClaimsOpenOn.Zero() {
		s.ClaimsOpenOn = portal.ClaimsOpenOn
	}
	if s.PayoutLow == 0 && s.PayoutHigh == 0 {
		s.PayoutLow, s.PayoutHigh = portal.PayoutLow, portal.PayoutHigh
		s.PayoutNote = portal.PayoutNote
	}
	if s.SettlementFund == 0 {
		s.SettlementFund = portal.SettlementFund
	}
	if s.Administrator == "" {
		s.Administrator = portal.Administrator
	}
	if !s.ProofRequired && portal.ProofRequired {
		s.ProofRequired, s.ProofNote = true, portal.ProofNote
	}
	// The administrator is the authority on whether claims are open.
	if portal.Stage.Actionable() || portal.Stage == model.StageClaimsClose {
		s.Stage = portal.Stage
	}
}

// outcome is one candidate page's contribution to the result.
type outcome struct {
	tier       extract.Tier
	escalated  bool
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
		if o.tier == extract.Prime {
			res.Prime++
		}
		if o.escalated {
			res.Escalated++
		}
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

func visit(ctx context.Context, st *store.Store, client *fetch.Client, router *extract.Router,
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

	text := htmlx.ExtractText(resp.Body)

	// Change detection runs on the readable text, not on the bytes.
	//
	// The bytes are useless for this. Sites emit a rotating CSRF token, a
	// session id, an ad slot or a "generated at" comment on every response, so
	// a byte hash reports that a notice changed overnight when not one word of
	// it did. The first version compared bytes, and a re-scan of a store that
	// was one day old re-extracted 77 of 91 pages and cost as much as the cold
	// start, which defeats the entire point of scanning on a schedule.
	//
	// Fetching and stripping the markup is microseconds and no money. The model
	// call is all of the money. So the gate belongs after the text is in hand
	// and before the model is asked anything.
	sum := sha256.Sum256([]byte(text))
	textHash := hex.EncodeToString(sum[:])

	id := model.NewID(cand.URL)
	mu.Lock()
	known := st.Settlement(id) != nil
	seen := st.SeenHash(id)
	mu.Unlock()
	if known && seen == textHash && !opts.Refetch {
		out.unchanged = true
		return out
	}

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
	if router != nil {
		// Spend on the pages where a claim can be filed, be cheap elsewhere. A
		// proceeding that has only been commenced cannot be claimed from for
		// years, and its page will have been re-read many times before it can.
		tier := extract.Stakes(text, src.Kind.Authority() >= source.KindRegistry.Authority())
		out.tier = tier
		s, err = router.For(tier).Extract(ctx, cand.URL, title, text)

		// Escalate when the cheap answer is visibly inadequate: a settlement
		// open for claims today with no class definition extracted is one the
		// matcher can say nothing useful about, which is the whole job. Paying
		// twice for those pages is cheaper than routing every page to the
		// capable model, and it is the failure a single-model run showed the
		// cheap tier actually producing.
		if tier == extract.Cheap && err == nil && extract.Underspecified(s) {
			if better, err2 := router.For(extract.Prime).Extract(ctx, cand.URL, title, text); err2 == nil {
				s, out.tier, out.escalated = better, extract.Prime, true
			}
		}
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
		if enriched := followPortal(ctx, client, router, s, portal); enriched {
			s.ClaimURL = portal
		}
	}

	s.SourceID = src.ID
	s.ContentHash = textHash
	if len(s.Jurisdiction) == 0 {
		s.Jurisdiction = src.Jurisdiction
	}
	out.extracted = true

	mu.Lock()
	isNew, changes, err := st.Upsert(s)
	// Recorded whether or not the extraction changed anything. "We read this
	// page and it said the same as last time" is the fact that makes tomorrow's
	// scan free, and it is exactly the case Upsert declines to write an event
	// for.
	st.RecordHash(s.ID, textHash)
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
