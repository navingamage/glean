package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/navingamage/glean/internal/logic"
	"github.com/navingamage/glean/internal/match"
	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/store"
)

func cmdList(g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("list")
	all := fs.Bool("all", false, "include expired settlements and ones you are not in")
	openOnly := fs.Bool("open", false, "only settlements accepting claims right now")
	limit := fs.Int("n", 25, "how many to show")
	if err := fs.Parse(args); err != nil {
		return err
	}

	primaries, _ := st.Primaries()
	byID := map[string]*model.Settlement{}
	var as []match.Assessment
	for _, s := range primaries {
		byID[s.ID] = s
		as = append(as, match.Assess(s, st.Profile))
	}
	match.Rank(as, byID)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tYOU\tVALUE\tDEADLINE\tSTAGE\tSETTLEMENT")
	shown := 0
	for _, a := range as {
		s := byID[a.SettlementID]
		if s == nil {
			continue
		}
		if !*all && (s.Expired() || a.Verdict == logic.False) {
			continue
		}
		if *openOnly && !a.Actionable {
			continue
		}
		if shown >= *limit {
			break
		}
		shown++
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			short(s.ID), verdictWord(a), value(a), deadline(s), s.Stage, trim(s.Title, 52))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if shown == 0 {
		fmt.Println("Nothing to show. Run `glean scan` first, or `glean list -all`.")
		return nil
	}

	if qs := match.Questions(as); len(qs) > 0 {
		fmt.Printf("\n%d open question(s) standing between you and about $%.0f of expected value. Run `glean ask`.\n",
			len(qs), match.TotalAtStake(qs, byID, as))
	}
	return nil
}

func cmdShow(g globalFlags, st *store.Store, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: glean show <id>")
	}
	id, err := resolveID(st, args[0])
	if err != nil {
		return err
	}
	s := st.Settlement(id)
	a := match.Assess(s, st.Profile)

	fmt.Printf("%s\n%s\n\n", s.Title, strings.Repeat("=", len(s.Title)))
	if s.Summary != "" {
		fmt.Printf("%s\n\n", wrap(s.Summary, 78))
	}

	line := func(k, v string) {
		if v != "" {
			fmt.Printf("  %-16s %s\n", k+":", v)
		}
	}
	line("id", s.ID)
	line("stage", string(s.Stage))
	line("case", s.CaseName)
	line("court", s.Court)
	line("defendants", strings.Join(s.Defendants, ", "))
	line("jurisdiction", strings.Join(s.Jurisdiction, ", "))
	line("claims open", s.ClaimsOpenOn.String())
	line("claim deadline", deadline(s))
	line("opt out by", s.OptOutDeadline.String())
	if s.SettlementFund > 0 {
		line("fund", fmt.Sprintf("$%s", commas(s.SettlementFund)))
	}
	if p := match.EstimatePayout(s); p > 0 {
		line("your share", fmt.Sprintf("about $%.0f %s", p, s.PayoutNote))
	}
	if s.ProofRequired {
		line("proof needed", orDash(s.ProofNote))
	}
	line("effort", fmt.Sprintf("about %d minutes", s.EffortMinutes))
	line("administrator", s.Administrator)
	line("claim at", s.ClaimURL)
	line("read at", s.URL)
	line("source", fmt.Sprintf("%s, via %s, confidence %.0f%%", s.SourceID, s.Extractor, s.Confidence*100))
	line("first seen", s.FirstSeen.String())

	if c := st.Claim(s.ID); c != nil {
		line("your claim", string(c.State))
	}

	fmt.Printf("\nAre you in the class?  %s\n", strings.ToUpper(verdictWord(a)))
	if a.Note != "" {
		fmt.Printf("  %s\n", a.Note)
	}
	if s.ClassText != "" {
		fmt.Printf("\n  \"%s\"\n", wrapIndent(s.ClassText, 74, "  "))
	}
	fmt.Println()
	for _, gr := range a.Groups {
		if len(gr.Criteria) > 1 {
			fmt.Printf("  any one of  [%s]\n", gr.ValueStr)
			for _, c := range gr.Criteria {
				fmt.Printf("      %-4s %s\n", c.ValueStr, c.Reason)
			}
			continue
		}
		for _, c := range gr.Criteria {
			fmt.Printf("  %-4s %s\n", c.ValueStr, c.Reason)
		}
	}

	if len(a.Questions) > 0 {
		fmt.Printf("\nTo resolve this:\n")
		for _, q := range a.Questions {
			fmt.Printf("  %s\n    glean answer %s yes\n", q.Ask, q.ID)
		}
	}
	return nil
}

func cmdAsk(g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("ask")
	limit := fs.Int("n", 10, "how many questions to ask")
	if err := fs.Parse(args); err != nil {
		return err
	}

	primaries, _ := st.Primaries()
	byID := map[string]*model.Settlement{}
	var as []match.Assessment
	for _, s := range primaries {
		byID[s.ID] = s
		as = append(as, match.Assess(s, st.Profile))
	}
	qs := match.Questions(as)
	if len(qs) == 0 {
		// Saying "everything is resolved" when the truth is "no class definition
		// was ever read" would hide the single thing the user needs to fix.
		unread := 0
		for _, a := range as {
			if a.Note != "" {
				unread++
			}
		}
		switch {
		case len(as) == 0:
			fmt.Println("Nothing tracked yet. Run `glean scan` first.")
		case unread == len(as):
			fmt.Printf("No questions to ask, because no class definition was read on any of the %d tracked settlements.\n", len(as))
			fmt.Println("Reading a class definition needs the extraction model. Set OPENROUTER_API_KEY and scan again.")
		default:
			fmt.Println("Nothing to ask. Every tracked settlement is resolved against your profile.")
		}
		return nil
	}
	if len(qs) > *limit {
		qs = qs[:*limit]
	}

	fmt.Printf("%d question(s), about $%.0f of expected value behind them. y / n / s to skip / q to stop.\n\n",
		len(qs), match.TotalAtStake(qs, byID, as))
	in := bufio.NewScanner(os.Stdin)
	answered := 0

	for _, q := range qs {
		worth := fmt.Sprintf("worth about $%.0f across %d", q.Worth, len(q.Unlocks))
		if q.Worth <= 0 {
			worth = fmt.Sprintf("settles %d", len(q.Unlocks))
		}
		fmt.Printf("%s\n  (%s settlement(s)) [y/n/s/q] ", q.Ask, worth)
		if !in.Scan() {
			break
		}
		switch strings.ToLower(strings.TrimSpace(in.Text())) {
		case "y", "yes":
			if err := st.Answer(holdingFor(q, false)); err != nil {
				return err
			}
			answered++
		case "n", "no":
			if err := st.Answer(holdingFor(q, true)); err != nil {
				return err
			}
			answered++
		case "q", "quit":
			fmt.Println()
			goto done
		default:
			// Skipping is a real answer to "I do not remember", and recording it
			// as a no would be a lie that costs money later.
		}
		fmt.Println()
	}
done:
	fmt.Printf("Recorded %d answer(s). Run `glean list` to see what changed.\n", answered)
	return nil
}

func cmdAnswer(g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("answer")
	from := fs.String("from", "", "start of the period this applies to, YYYY-MM-DD")
	until := fs.String("until", "", "end of the period, YYYY-MM-DD")
	label := fs.String("label", "", "how you would name this, if not the question's own wording")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: glean answer <question-id> yes|no [-from YYYY-MM-DD] [-until YYYY-MM-DD]")
	}
	kindStr, entity, ok := strings.Cut(rest[0], ":")
	if !ok {
		return fmt.Errorf("question ids look like purchase:loblaw; got %q", rest[0])
	}

	var negative bool
	switch strings.ToLower(rest[1]) {
	case "yes", "y", "true":
	case "no", "n", "false":
		negative = true
	default:
		return fmt.Errorf("answer must be yes or no, got %q", rest[1])
	}

	f, err := model.ParseDate(*from)
	if err != nil {
		return err
	}
	u, err := model.ParseDate(*until)
	if err != nil {
		return err
	}

	kind := model.CriterionKind(kindStr)
	if !model.ValidKind(kind) {
		return fmt.Errorf("unknown criterion kind %q", kindStr)
	}
	name := *label
	if name == "" {
		name = entity
	}

	// Residency is not a holding; it is where the person lives, and the profile
	// keeps it separately so the wildcard rules in the matcher can see it.
	if kind == model.KindResidency {
		code := model.NormalizeProvince(entity)
		if code == "" {
			return fmt.Errorf("%q is not a province glean knows", entity)
		}
		if negative {
			st.Profile.Residences = dropProvince(st.Profile.Residences, code)
		} else {
			st.Profile.Residences = append(dropProvince(st.Profile.Residences, code),
				model.Residence{Province: code, Window: model.Window{From: f, Until: u}})
		}
		if err := st.SaveProfile(); err != nil {
			return err
		}
		fmt.Printf("Recorded. Residences: %s\n", provinceList(st.Profile.Residences))
		return nil
	}

	h := model.Holding{
		Kind: kind, Entity: entity, Label: name,
		Window: model.Window{From: f, Until: u}, Negative: negative,
	}
	if err := st.Answer(h); err != nil {
		return err
	}
	fmt.Printf("Recorded: %s %s %s\n", kind, name, yesNo(!negative))
	return nil
}

func cmdClaim(g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("claim")
	amount := fs.Float64("amount", 0, "amount received, for a paid claim")
	ref := fs.String("ref", "", "confirmation or reference number")
	note := fs.String("note", "", "anything worth remembering")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: glean claim <id> <new|interested|filed|paid|rejected|skipped>")
	}
	id, err := resolveID(st, rest[0])
	if err != nil {
		return err
	}
	state := model.ClaimState(strings.ToLower(rest[1]))
	switch state {
	case model.StateNew, model.StateInterested, model.StateFiled,
		model.StatePaid, model.StateRejected, model.StateSkipped:
	default:
		return fmt.Errorf("unknown state %q", rest[1])
	}

	c := st.Claim(id)
	if c == nil {
		c = &model.Claim{SettlementID: id}
	}
	c.State = state
	if *amount > 0 {
		c.Amount = *amount
	}
	if *ref != "" {
		c.Reference = *ref
	}
	if *note != "" {
		c.Note = *note
	}
	switch state {
	case model.StateFiled:
		if c.FiledOn.Zero() {
			c.FiledOn = model.Today()
		}
	case model.StatePaid:
		c.PaidOn = model.Today()
	}
	if err := st.SetClaim(c); err != nil {
		return err
	}
	fmt.Printf("%s: %s\n", trim(st.Settlement(id).Title, 60), state)
	return nil
}

func cmdProfile(g globalFlags, st *store.Store, args []string) error {
	fs := flagSet("profile")
	province := fs.String("province", "", "add a province you have lived in")
	minPayout := fs.Float64("min-payout", -1, "hide settlements worth less than this")
	maxEffort := fs.Int("max-effort", -1, "hide claims that take longer than this many minutes")
	forget := fs.String("forget", "", "remove an answer, e.g. purchase:loblaw")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p := st.Profile
	dirty := false

	if *province != "" {
		code := model.NormalizeProvince(*province)
		if code == "" {
			return fmt.Errorf("%q is not a province glean knows", *province)
		}
		p.Residences = append(dropProvince(p.Residences, code), model.Residence{Province: code})
		dirty = true
	}
	if *minPayout >= 0 {
		p.MinPayout, dirty = *minPayout, true
	}
	if *maxEffort >= 0 {
		p.MaxEffort, dirty = *maxEffort, true
	}
	if *forget != "" {
		kind, entity, _ := strings.Cut(*forget, ":")
		var kept []model.Holding
		for _, h := range p.Holdings {
			if string(h.Kind) == kind && h.Entity == model.NormalizeEntity(entity) {
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) == len(p.Holdings) {
			return fmt.Errorf("no answer on file for %q", *forget)
		}
		p.Holdings, dirty = kept, true
	}
	if dirty {
		if err := st.SaveProfile(); err != nil {
			return err
		}
	}

	fmt.Printf("Lived in:   %s\n", provinceList(p.Residences))
	fmt.Printf("Thresholds: at least $%.0f, at most %d minutes\n", p.MinPayout, p.MaxEffort)
	if len(p.Holdings) == 0 {
		fmt.Println("\nNo answers on file. Run `glean ask`.")
		return nil
	}
	fmt.Printf("\n%d answer(s):\n", len(p.Holdings))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, h := range p.Holdings {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
			h.Kind, h.Label, yesNo(!h.Negative), h.Window)
	}
	return w.Flush()
}

func holdingFor(q match.Question, negative bool) model.Holding {
	return model.Holding{
		Kind: q.Kind, Entity: q.Entity, Label: q.Label,
		Window: q.Window, Negative: negative,
	}
}

func dropProvince(rs []model.Residence, code string) []model.Residence {
	var out []model.Residence
	for _, r := range rs {
		// A specific province supersedes the bare "Canada" the profile starts
		// with, since keeping both would leave the vague one answering questions
		// the specific one can now answer properly.
		if r.Province == code || (r.Province == "CA" && code != "CA") {
			continue
		}
		out = append(out, r)
	}
	return out
}
