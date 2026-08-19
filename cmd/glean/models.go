package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/navingamage/glean/internal/extract"
)

func cmdModels(ctx context.Context, g globalFlags, args []string) error {
	fs := flagSet("models")
	limit := fs.Int("n", 20, "how many to show")
	all := fs.Bool("all", false, "include models glean cannot use")
	pages := fs.Int("pages", 40, "pages per scan, for the cost estimate")
	match := fs.String("match", "", "only models whose id contains this")
	if err := fs.Parse(args); err != nil {
		return err
	}

	models, err := extract.ListModels(ctx, "")
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "$/PAGE\t$/SCAN\tIN $/M\tOUT $/M\tCONTEXT\tMODEL")
	shown := 0
	for _, m := range models {
		if !*all && !m.Usable() {
			continue
		}
		if *match != "" && !strings.Contains(m.ID, *match) {
			continue
		}
		if shown >= *limit {
			break
		}
		shown++
		mark := ""
		if m.ID == extract.DefaultModel {
			mark = "  <- default"
		}
		if !m.Usable() {
			mark += "  (no strict schema)"
		}
		fmt.Fprintf(w, "%.5f\t%.2f\t%.3f\t%.3f\t%s\t%s%s\n",
			m.CostPerPage(), m.CostPerPage()*float64(*pages),
			m.InPerM, m.OutPerM, thousands(m.Context), m.ID, mark)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if shown == 0 {
		fmt.Println("Nothing matched. Try -all, or a looser -match.")
		return nil
	}

	fmt.Printf("\nPriced at %d in / %d out tokens per page, which is what a Canadian\n",
		extract.TypicalInputTokens, extract.TypicalOutputTokens)
	fmt.Printf("settlement notice actually costs, over %d pages per scan.\n", *pages)
	fmt.Println("Only the pages that changed since the last scan are extracted, so a")
	fmt.Println("steady-state daily run costs a small fraction of that.")
	fmt.Println("\nAt this size the choice is about accuracy, not money: a full first scan")
	fmt.Println("costs cents on any of these. Getting AND and OR backwards in a class")
	fmt.Println("definition is the expensive mistake, not the price per token.")
	fmt.Println("\nA :batch suffix is half price because the provider may queue the")
	fmt.Println("request rather than answer it immediately, which a scan waiting on it")
	fmt.Println("will feel as a stall. Try one before committing a schedule to it.")
	fmt.Printf("\nPick one with:  glean -model <id> scan\n")
	fmt.Printf("Or set it once:  export GLEAN_MODEL=<id>\n")
	return nil
}

func thousands(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}
