// Command glean finds Canadian class action settlements you can claim from, and
// keeps track of when you have to act.
//
// The premise is that this money is not found by looking for it. Nobody wakes up
// wondering whether the bread they bought in 2009 was price-fixed, or whether
// their bank's overdraft disclosure was defective in 2016. The settlements exist,
// the deadlines pass, and the unclaimed remainder goes back where it came from.
// So glean does the looking on a schedule, holds a small profile of the ordinary
// facts a class definition turns on, and interrupts you only when something you
// can actually claim is about to close.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/navingamage/glean/internal/extract"
	"github.com/navingamage/glean/internal/store"
)

const usage = `glean finds Canadian class action settlements you can claim from.

Usage: glean [flags] <command> [arguments]

Commands:
  scan          Read every source, extract what is new, report what to act on
  watch         Scan on a schedule and notify when something needs you
  list          Show tracked settlements, best first
  show <id>     Everything known about one settlement, and why it matched
  ask           Answer the questions that unlock the most money
  answer <q> yes|no
                Answer one question without the prompts
  profile       Show your profile, or edit it with the flags below
  claim <id> <new|interested|filed|paid|rejected|skipped>
                Record what you have done about a settlement
  sources       List configured sources; "sources check" probes each one
  serve         Run the web dashboard
  digest        Re-render the current digest without scanning

Flags:
`

type globalFlags struct {
	dir      string
	registry string
	model    string
	noLLM    bool
	verbose  bool
}

func main() {
	var g globalFlags
	flag.StringVar(&g.dir, "dir", "", "state directory (default $GLEAN_HOME or ~/.local/share/glean)")
	flag.StringVar(&g.registry, "sources", "", "path to a sources.json replacing the built-in registry")
	flag.StringVar(&g.model, "model", extract.DefaultModel, "extraction model")
	flag.BoolVar(&g.noLLM, "no-llm", false, "extract with regular expressions only, never call a model")
	flag.BoolVar(&g.verbose, "v", false, "log each page as it is visited")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, g, args); err != nil {
		fmt.Fprintf(os.Stderr, "glean: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, g globalFlags, args []string) error {
	cmd, rest := args[0], args[1:]

	// sources check needs no state directory and no profile.
	if cmd == "sources" {
		return cmdSources(ctx, g, rest)
	}

	st, err := store.Open(g.dir)
	if err != nil {
		return err
	}

	switch cmd {
	case "scan":
		return cmdScan(ctx, g, st, rest)
	case "watch":
		return cmdWatch(ctx, g, st, rest)
	case "list":
		return cmdList(g, st, rest)
	case "show":
		return cmdShow(g, st, rest)
	case "ask":
		return cmdAsk(g, st, rest)
	case "answer":
		return cmdAnswer(g, st, rest)
	case "profile":
		return cmdProfile(g, st, rest)
	case "claim":
		return cmdClaim(g, st, rest)
	case "serve":
		return cmdServe(ctx, g, st, rest)
	case "digest":
		return cmdDigest(g, st, rest)
	case "help", "-h", "--help":
		flag.Usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: glean help)", cmd)
	}
}

// extractor builds the model-backed extractor, or returns nil when there is no
// key or the user asked for regular expressions only. A nil extractor is a
// supported mode, not an error: glean still tracks deadlines.
func extractor(g globalFlags) (*extract.Client, string) {
	if g.noLLM {
		return nil, "extraction: regular expressions only (-no-llm)"
	}
	ex, err := extract.NewClient("", g.model)
	if err != nil {
		return nil, "extraction: regular expressions only (no ANTHROPIC_API_KEY; class definitions will not be read)"
	}
	return ex, "extraction: " + ex.Model
}

func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

// short renders an id short enough to type. Full ids are sixteen hex characters
// and every command accepts any unambiguous prefix.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// resolveID accepts a prefix of a settlement id and requires it to be unique,
// which is the behaviour anyone who has used git already expects.
func resolveID(st *store.Store, prefix string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("no settlement id given")
	}
	var hits []string
	for id := range st.ByID() {
		if strings.HasPrefix(id, prefix) {
			hits = append(hits, id)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no settlement matches %q", prefix)
	case 1:
		return hits[0], nil
	default:
		return "", fmt.Errorf("%q matches %d settlements; be more specific", prefix, len(hits))
	}
}
