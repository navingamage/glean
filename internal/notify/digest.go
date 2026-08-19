// Package notify turns a scan into something a person reads once and then acts
// on, and gets that in front of them without their having to remember to look.
//
// The reminder schedule is the part worth arguing about. A class action pays out
// on a deadline that was set eighteen months earlier and mentioned once. Telling
// someone about it the day it closes is worse than useless if the claim needs a
// document they have to find. So reminders fire at thirty, fourteen, seven and
// one day out, and the thirty-day one is the one that matters.
package notify

import (
	"fmt"
	"sort"
	"strings"

	"github.com/navingamage/glean/internal/logic"
	"github.com/navingamage/glean/internal/match"
	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/store"
)

// reminderDays are the days-remaining marks that produce a nudge. A settlement
// crossing one of these between two scans is worth interrupting someone for.
var reminderDays = []int{30, 14, 7, 3, 1, 0}

type Item struct {
	Settlement *model.Settlement
	Assessment match.Assessment
	Reason     string
}

type Digest struct {
	Matches   []Item           // newly found, and you are in the class
	Maybes    []Item           // newly found, and you might be
	Closing   []Item           // deadline crossing a reminder mark
	Changed   []ChangeItem     // a field moved on something already tracked
	Questions []match.Question // answering these unlocks the most money
}

type ChangeItem struct {
	Settlement *model.Settlement
	Changes    []store.Change
}

func (d *Digest) Empty() bool {
	return len(d.Matches) == 0 && len(d.Maybes) == 0 &&
		len(d.Closing) == 0 && len(d.Changed) == 0
}

// Build assembles the digest for one scan: the day's news, and nothing else.
//
// newIDs is what the scan discovered this pass; everything else in the store is
// checked only for a deadline crossing a reminder mark or a changed field, so a
// daily run says what changed today rather than restating the whole ledger.
func Build(st *store.Store, newIDs map[string]bool, changed map[string][]store.Change) *Digest {
	return build(st, newIDs, changed, 0)
}

// View assembles the same digest for a screen someone chose to open.
//
// The difference from Build is the whole reason both exist. A notification has
// to be rare or it gets ignored, so it fires only on the day a deadline crosses
// thirty, fourteen, seven, three or one. A dashboard is looked at deliberately,
// and showing nothing under "closing soon" because today happens to be day
// twelve rather than day fourteen would be absurd.
func View(st *store.Store) *Digest {
	return build(st, nil, nil, 60)
}

func build(st *store.Store, newIDs map[string]bool, changed map[string][]store.Change, closingWithin int) *Digest {
	d := &Digest{}
	profile := st.Profile
	byID := st.ByID()

	var all []match.Assessment
	for _, s := range st.Settlements() {
		a := match.Assess(s, profile)
		all = append(all, a)

		if s.Expired() {
			continue
		}
		claim := st.Claim(s.ID)
		filed := claim != nil && !claim.Open()

		item := Item{Settlement: s, Assessment: a}
		switch {
		case newIDs[s.ID] && a.Verdict == logic.True && a.Actionable:
			item.Reason = "new, and you are in the class"
			d.Matches = append(d.Matches, item)
		case newIDs[s.ID] && a.Verdict == logic.Unknown && a.ExpectedValue > 0:
			item.Reason = fmt.Sprintf("new, %d%% chance you qualify", int(a.Probability*100))
			d.Maybes = append(d.Maybes, item)
		case !filed && a.Verdict != logic.False && closing(s.DaysLeft(), closingWithin):
			item.Reason = closingReason(s.DaysLeft())
			d.Closing = append(d.Closing, item)
		}
	}

	for id, ch := range changed {
		if s := byID[id]; s != nil && interesting(ch) {
			d.Changed = append(d.Changed, ChangeItem{Settlement: s, Changes: ch})
		}
	}

	d.Questions = match.Questions(all)
	if len(d.Questions) > 8 {
		d.Questions = d.Questions[:8]
	}

	for _, list := range [][]Item{d.Matches, d.Maybes, d.Closing} {
		sort.Slice(list, func(i, j int) bool {
			return list[i].Assessment.ExpectedValue > list[j].Assessment.ExpectedValue
		})
	}
	return d
}

// closing reports whether a deadline deserves the "closing soon" list. With a
// window of zero it fires only on the reminder marks, which is what a digest
// wants; with a window it fires on everything inside it, which is what a screen
// wants.
func closing(days, within int) bool {
	if days < 0 {
		return false
	}
	if within > 0 {
		return days <= within
	}
	for _, m := range reminderDays {
		if days == m {
			return true
		}
	}
	return false
}

func closingReason(days int) string {
	switch {
	case days <= 0:
		return "closes today"
	case days == 1:
		return "closes tomorrow"
	default:
		return fmt.Sprintf("closes in %d days", days)
	}
}

// interesting filters the change log down to movements a person would act on. A
// summary that got reworded is a change; a deadline that moved is news.
func interesting(cs []store.Change) bool {
	for _, c := range cs {
		switch c.Field {
		case "stage", "claim_deadline", "opt_out_deadline", "claims_open_on", "claim_url":
			return true
		}
	}
	return false
}

// Text renders the digest as plain text, which is what goes to a terminal, an
// email body and a webhook alike.
func (d *Digest) Text() string {
	var b strings.Builder
	section := func(title string, items []Item) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s\n%s\n", title, strings.Repeat("-", len(title)))
		for _, it := range items {
			s := it.Settlement
			fmt.Fprintf(&b, "  %s\n", s.Title)
			fmt.Fprintf(&b, "    %s", it.Reason)
			if !s.ClaimDeadline.Zero() {
				fmt.Fprintf(&b, " | deadline %s", s.ClaimDeadline)
			}
			if v := it.Assessment.Payout; v > 0 {
				fmt.Fprintf(&b, " | about $%.0f", v)
			}
			if s.ProofRequired {
				fmt.Fprint(&b, " | proof required")
			}
			fmt.Fprintln(&b)
			if url := claimLink(s); url != "" {
				fmt.Fprintf(&b, "    %s\n", url)
			}
		}
		fmt.Fprintln(&b)
	}

	section("Claim these", d.Matches)
	section("Closing soon", d.Closing)
	section("Possible matches", d.Maybes)

	if len(d.Changed) > 0 {
		fmt.Fprintf(&b, "Changed\n-------\n")
		for _, c := range d.Changed {
			fmt.Fprintf(&b, "  %s\n", c.Settlement.Title)
			for _, ch := range c.Changes {
				fmt.Fprintf(&b, "    %s: %s -> %s\n", ch.Field, orUnset(ch.From), orUnset(ch.To))
			}
		}
		fmt.Fprintln(&b)
	}

	if len(d.Questions) > 0 {
		fmt.Fprintf(&b, "Answer these\n------------\n")
		for _, q := range d.Questions {
			fmt.Fprintf(&b, "  %s\n", q.Ask)
			fmt.Fprintf(&b, "    unlocks about $%.0f across %d settlement(s)  [glean answer %s yes]\n",
				q.Worth, len(q.Unlocks), q.ID)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// Subject is a one-line summary for an email or a desktop notification.
func (d *Digest) Subject() string {
	switch {
	case len(d.Matches) > 0:
		return fmt.Sprintf("glean: %d claim(s) you qualify for", len(d.Matches))
	case len(d.Closing) > 0:
		return fmt.Sprintf("glean: %d claim(s) closing soon", len(d.Closing))
	case len(d.Maybes) > 0:
		return fmt.Sprintf("glean: %d possible match(es)", len(d.Maybes))
	default:
		return "glean: nothing new"
	}
}

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func claimLink(s *model.Settlement) string {
	if s.ClaimURL != "" {
		return s.ClaimURL
	}
	return s.URL
}
