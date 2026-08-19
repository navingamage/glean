package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/navingamage/glean/internal/notify"
	"github.com/navingamage/glean/internal/scan"
	"github.com/navingamage/glean/internal/source"
	"github.com/navingamage/glean/internal/store"
)

func cmdScan(ctx context.Context, g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("scan")
	only := fs.String("only", "", "comma-separated source ids to scan")
	perSource := fs.Int("per-source", 40, "maximum candidate pages to follow per source")
	workers := fs.Int("workers", 4, "how many sources to walk at once")
	refetch := fs.Bool("refetch", false, "re-extract pages even when they have not changed")
	quiet := fs.Bool("quiet", false, "suppress the digest, print only the summary line")
	send := fs.Bool("notify", false, "deliver the digest over the configured channels")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sources, err := source.Load(g.registry)
	if err != nil {
		return err
	}
	sources = source.Enabled(sources, splitList(*only))
	if len(sources) == 0 {
		return fmt.Errorf("no sources enabled")
	}

	ex, note := extractor(g)
	if g.verbose {
		fmt.Fprintf(os.Stderr, "%s\n%d sources\n", note, len(sources))
	}

	opts := scan.DefaultOptions()
	opts.PerSource, opts.Workers, opts.Refetch = *perSource, *workers, *refetch

	started := time.Now()
	res := scan.Run(ctx, st, sources, ex, opts)

	newIDs := map[string]bool{}
	for _, s := range res.New {
		newIDs[s.ID] = true
	}
	d := notify.Build(st, newIDs, res.Updated)

	if !*quiet {
		if d.Empty() && len(d.Questions) == 0 {
			fmt.Println("Nothing new.")
		} else {
			fmt.Print(d.Text())
		}
		fmt.Println()
	}
	fmt.Printf("%s in %s\n", res, time.Since(started).Round(time.Second))
	if ex != nil {
		fmt.Printf("%s (%s)\n", ex.Usage(), ex)
	}

	for _, err := range res.Errors {
		fmt.Fprintf(os.Stderr, "  ! %v\n", err)
	}

	if *send {
		cfg, err := loadNotifyConfig(st.Dir)
		if err != nil {
			return err
		}
		for _, err := range notify.Send(cfg, d) {
			fmt.Fprintf(os.Stderr, "  ! %v\n", err)
		}
	}
	return nil
}

func cmdWatch(ctx context.Context, g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("watch")
	every := fs.Duration("every", 12*time.Hour, "how often to scan")
	only := fs.String("only", "", "comma-separated source ids to scan")
	perSource := fs.Int("per-source", 40, "maximum candidate pages to follow per source")
	quiet := fs.Bool("quiet", false, "print only the summary line for each pass")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *every < time.Minute {
		return fmt.Errorf("-every must be at least a minute; these sites update daily at best")
	}

	scanArgs := []string{"-notify", "-per-source", fmt.Sprint(*perSource)}
	if *only != "" {
		scanArgs = append(scanArgs, "-only", *only)
	}
	if *quiet {
		scanArgs = append(scanArgs, "-quiet")
	}

	fmt.Printf("watching %d sources every %s; state in %s\n",
		countSources(g), *every, st.Dir)

	// Scan immediately, then on the interval. Waiting twelve hours to find out
	// whether the thing works is not a reasonable first experience.
	for {
		fmt.Printf("\n--- %s ---\n", time.Now().Format("2006-01-02 15:04"))
		if err := cmdScan(ctx, g, st, scanArgs); err != nil {
			fmt.Fprintf(os.Stderr, "glean: scan failed: %v\n", err)
		}
		select {
		case <-ctx.Done():
			fmt.Println("stopping")
			return nil
		case <-time.After(*every):
		}
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func countSources(g globalFlags) int {
	all, err := source.Load(g.registry)
	if err != nil {
		return 0
	}
	return len(source.Enabled(all, nil))
}

func cmdDigest(g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("digest")
	send := fs.Bool("notify", false, "deliver over the configured channels")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// `glean digest` is something a person typed, so it uses the screen's rules
	// rather than the notification's: show everything closing, not only what
	// crosses a reminder mark today.
	d := notify.View(st)
	if d.Empty() && len(d.Questions) == 0 {
		fmt.Println("Nothing needs you right now.")
		return nil
	}
	fmt.Print(d.Text())
	if *send {
		cfg, err := loadNotifyConfig(st.Dir)
		if err != nil {
			return err
		}
		for _, err := range notify.Send(cfg, d) {
			fmt.Fprintf(os.Stderr, "  ! %v\n", err)
		}
	}
	return nil
}

// loadNotifyConfig reads notify.json from the state directory. A missing file
// means "print to the terminal and nothing else", which is a valid setup.
func loadNotifyConfig(dir string) (notify.Config, error) {
	var cfg notify.Config
	b, err := os.ReadFile(filepath.Join(dir, "notify.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing notify.json: %w", err)
	}
	return cfg, nil
}
