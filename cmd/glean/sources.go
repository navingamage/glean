package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/navingamage/glean/internal/fetch"
	"github.com/navingamage/glean/internal/source"
)

func cmdSources(ctx context.Context, g globalFlags, args []string) error {
	all, err := source.Load(g.registry)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "check" {
		fs := flagSet("sources check")
		links := fs.Bool("links", false, "print the top-scoring links on each listing page, ignoring follow patterns")
		only := fs.String("only", "", "comma-separated source ids")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		all = source.Enabled(all, splitList(*only))
		if *links {
			return dumpLinks(ctx, all)
		}
		return checkSources(ctx, all)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tWHERE\tNAME")
	for _, s := range all {
		state := ""
		if s.Disabled {
			state = " (disabled)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s%s\n",
			s.ID, s.Kind, joinOr(s.Jurisdiction, "CA"), s.Name, state)
	}
	return w.Flush()
}

// checkSources probes every listing page and reports what came back.
//
// This exists because a registry of eighteen third-party websites is wrong the
// moment it is written and gets wronger. A scraper that quietly finds nothing is
// the failure mode that costs money, so there has to be a way to ask which of
// these still work, and the answer has to come from the network rather than from
// anyone's memory of what the URL used to be.
func checkSources(ctx context.Context, all []*source.Source) error {
	client := fetch.New("")
	client.IgnoreCache = true

	type row struct {
		id, listing, status string
		found               int
	}
	var mu sync.Mutex
	var rows []row
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)

	for _, s := range all {
		for _, listing := range s.Listing {
			wg.Add(1)
			go func(s *source.Source, listing string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
				defer cancel()

				r := row{id: s.ID, listing: listing}
				resp, err := client.Get(cctx, listing)
				switch {
				case err != nil:
					r.status = shortErr(err)
				case !resp.IsHTML():
					r.status = "not html"
				default:
					one := &source.Source{
						ID: s.ID, Listing: []string{listing}, Follow: s.Follow, Exclude: s.Exclude,
					}
					if err := one.Compile(); err != nil {
						r.status = "bad pattern"
						break
					}
					cands, _ := source.Discover(cctx, client, one, 0)
					r.status, r.found = "ok", len(cands)
				}
				mu.Lock()
				rows = append(rows, r)
				mu.Unlock()
			}(s, listing)
		}
	}
	wg.Wait()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tSTATUS\tCASES\tLISTING")
	working, dead := 0, 0
	for _, s := range all {
		for _, r := range rows {
			if r.id != s.ID {
				continue
			}
			found := "-"
			if r.status == "ok" {
				found = fmt.Sprint(r.found)
				if r.found > 0 {
					working++
				} else {
					dead++
				}
			} else {
				dead++
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.id, r.status, found, r.listing)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\n%d listing page(s) yielding cases, %d not. Prune or fix the rest in sources.json.\n",
		working, dead)
	return nil
}

// dumpLinks prints what a listing page actually links to, which is the input to
// writing a correct follow pattern for it.
func dumpLinks(ctx context.Context, all []*source.Source) error {
	client := fetch.New("")
	client.IgnoreCache = true
	for _, s := range all {
		for _, listing := range s.Listing {
			cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			links, err := source.RawLinks(cctx, client, listing, 14)
			cancel()
			fmt.Printf("\n=== %s  %s\n", s.ID, listing)
			if err != nil {
				fmt.Printf("    %v\n", err)
				continue
			}
			for _, l := range links {
				fmt.Printf("  %3d  %s\n       %s\n", l.Score, l.URL, trim(l.Title, 70))
			}
		}
	}
	return nil
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 44 {
		return s[len(s)-44:]
	}
	return s
}

func joinOr(xs []string, fallback string) string {
	if len(xs) == 0 {
		return fallback
	}
	out := xs[0]
	for _, x := range xs[1:] {
		out += "," + x
	}
	return out
}
