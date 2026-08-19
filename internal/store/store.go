// Package store persists what glean has found.
//
// It is an append-only log of events with the current state derived by replaying
// it, rather than a table of rows that get overwritten. That choice is not
// architectural fashion: the single most important thing this program can tell
// you is that a deadline moved, and a store that overwrites cannot tell you
// that. Replaying also means the state can be thrown away and rebuilt, and that
// a bug in the matcher is a bug you can re-run history against.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/navingamage/glean/internal/dedupe"
	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/source"
)

type EventType string

const (
	EvDiscovered EventType = "discovered"
	EvUpdated    EventType = "updated"
	EvClaim      EventType = "claim"
	EvAnswered   EventType = "answered"
	EvDropped    EventType = "dropped"
)

// Change is one field that moved between two sightings of the same settlement.
type Change struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

type Event struct {
	At           time.Time         `json:"at"`
	Type         EventType         `json:"type"`
	SettlementID string            `json:"settlement_id,omitempty"`
	Settlement   *model.Settlement `json:"settlement,omitempty"`
	Claim        *model.Claim      `json:"claim,omitempty"`
	Holding      *model.Holding    `json:"holding,omitempty"`
	Changes      []Change          `json:"changes,omitempty"`
	Note         string            `json:"note,omitempty"`
}

type Store struct {
	Dir     string
	Profile *model.Profile

	// sources is the registry, used to weigh which of two records for one
	// proceeding is the more authoritative. It defaults to the built-in list so
	// that a Store is usable without one being handed in.
	sources []*source.Source

	settlements map[string]*model.Settlement
	claims      map[string]*model.Claim
	events      []Event
}

// DefaultDir is where glean keeps its state when nothing says otherwise.
func DefaultDir() string {
	if v := os.Getenv("GLEAN_HOME"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "glean")
	}
	return ".glean"
}

func Open(dir string) (*Store, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	s := &Store{
		Dir:         dir,
		settlements: map[string]*model.Settlement{},
		claims:      map[string]*model.Claim{},
	}
	if err := s.replay(); err != nil {
		return nil, err
	}
	if err := s.loadProfile(); err != nil {
		return nil, err
	}
	if all, err := source.Load(""); err == nil {
		s.sources = all
	}
	return s, nil
}

// SetSources replaces the registry used for deduplication, so that a custom
// registry passed with -sources is reflected in which record wins.
func (s *Store) SetSources(all []*source.Source) { s.sources = all }

func (s *Store) logPath() string     { return filepath.Join(s.Dir, "events.jsonl") }
func (s *Store) profilePath() string { return filepath.Join(s.Dir, "profile.json") }

// CacheDir is where the fetcher keeps conditional-GET state. It lives under the
// store so that deleting one directory resets everything.
func (s *Store) CacheDir() string { return filepath.Join(s.Dir, "cache") }

func (s *Store) replay() error {
	f, err := os.Open(s.logPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("store: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			// One corrupt line, from a crash mid-write, must not cost the whole
			// history. Skip it and carry on.
			continue
		}
		s.events = append(s.events, e)
		s.apply(e)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("store: reading log at line %d: %w", line, err)
	}
	return nil
}

func (s *Store) apply(e Event) {
	switch e.Type {
	case EvDiscovered, EvUpdated:
		if e.Settlement != nil {
			s.settlements[e.Settlement.ID] = e.Settlement
		}
	case EvDropped:
		delete(s.settlements, e.SettlementID)
	case EvClaim:
		if e.Claim != nil {
			s.claims[e.Claim.SettlementID] = e.Claim
		}
	}
}

func (s *Store) append(e Event) error {
	e.At = time.Now()
	f, err := os.OpenFile(s.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	s.events = append(s.events, e)
	s.apply(e)
	return nil
}

// Upsert records a settlement, returning whether it is new and what moved.
//
// The FirstSeen date is carried forward from the existing record rather than
// taken from the incoming one, because the incoming one was built a moment ago
// and would otherwise report every settlement as discovered today.
func (s *Store) Upsert(in *model.Settlement) (isNew bool, changes []Change, err error) {
	in.LastSeen = model.Today()
	prev, ok := s.settlements[in.ID]
	if !ok {
		if in.FirstSeen.Zero() {
			in.FirstSeen = model.Today()
		}
		return true, nil, s.append(Event{Type: EvDiscovered, SettlementID: in.ID, Settlement: in})
	}
	in.FirstSeen = prev.FirstSeen
	changes = Diff(prev, in)
	if len(changes) == 0 {
		// Nothing worth an event; still refresh LastSeen in memory so a report
		// can distinguish "unchanged today" from "not seen in three months".
		prev.LastSeen = in.LastSeen
		return false, nil, nil
	}
	return false, changes, s.append(Event{
		Type: EvUpdated, SettlementID: in.ID, Settlement: in, Changes: changes,
	})
}

// watched are the fields whose movement is worth a line in the log and a line in
// the morning digest. Summary wording and confidence drift on every re-extraction
// and are deliberately not among them.
func Diff(a, b *model.Settlement) []Change {
	var out []Change
	add := func(field, from, to string) {
		if from != to {
			out = append(out, Change{Field: field, From: from, To: to})
		}
	}
	add("stage", string(a.Stage), string(b.Stage))
	add("claim_deadline", a.ClaimDeadline.String(), b.ClaimDeadline.String())
	add("opt_out_deadline", a.OptOutDeadline.String(), b.OptOutDeadline.String())
	add("claims_open_on", a.ClaimsOpenOn.String(), b.ClaimsOpenOn.String())
	add("claim_url", a.ClaimURL, b.ClaimURL)
	add("payout_low", money(a.PayoutLow), money(b.PayoutLow))
	add("payout_high", money(a.PayoutHigh), money(b.PayoutHigh))
	add("settlement_fund", money(a.SettlementFund), money(b.SettlementFund))
	add("criteria", fmt.Sprint(len(a.Criteria)), fmt.Sprint(len(b.Criteria)))
	return out
}

func money(f float64) string {
	if f == 0 {
		return ""
	}
	return fmt.Sprintf("%.2f", f)
}

// Settlements returns every known settlement, newest deadline first.
func (s *Store) Settlements() []*model.Settlement {
	out := make([]*model.Settlement, 0, len(s.settlements))
	for _, v := range s.settlements {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DaysLeft() < out[j].DaysLeft() })
	return out
}

func (s *Store) Settlement(id string) *model.Settlement { return s.settlements[id] }

// Primaries returns one settlement per proceeding, collapsing records that two
// or more sources reported separately, plus a lookup from any record's id to
// the primary that represents it.
func (s *Store) Primaries() ([]*model.Settlement, map[string]string) {
	groups := dedupe.Collapse(s.Settlements(), s.sources)
	out := make([]*model.Settlement, 0, len(groups))
	canonical := make(map[string]string, len(s.settlements))
	for _, g := range groups {
		out = append(out, g.Primary)
		canonical[g.Primary.ID] = g.Primary.ID
		for _, dup := range g.Also {
			canonical[dup.ID] = g.Primary.ID
		}
	}
	return out, canonical
}

func (s *Store) ByID() map[string]*model.Settlement {
	out := make(map[string]*model.Settlement, len(s.settlements))
	for k, v := range s.settlements {
		out[k] = v
	}
	return out
}

func (s *Store) Claim(id string) *model.Claim { return s.claims[id] }

func (s *Store) Claims() []*model.Claim {
	out := make([]*model.Claim, 0, len(s.claims))
	for _, c := range s.claims {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SettlementID < out[j].SettlementID })
	return out
}

func (s *Store) SetClaim(c *model.Claim) error {
	c.UpdatedOn = model.Today()
	return s.append(Event{Type: EvClaim, SettlementID: c.SettlementID, Claim: c})
}

// Answer records a fact about the user, both in the profile and in the log, so
// that a wrong answer can be found later and undone.
func (s *Store) Answer(h model.Holding) error {
	s.Profile.Record(h)
	if err := s.SaveProfile(); err != nil {
		return err
	}
	return s.append(Event{Type: EvAnswered, Holding: &h})
}

// Since returns events newer than t, oldest first.
func (s *Store) Since(t time.Time) []Event {
	var out []Event
	for _, e := range s.events {
		if e.At.After(t) {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) loadProfile() error {
	b, err := os.ReadFile(s.profilePath())
	if err != nil {
		if os.IsNotExist(err) {
			s.Profile = model.DefaultProfile()
			return nil
		}
		return fmt.Errorf("store: %w", err)
	}
	var p model.Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("store: parsing profile: %w", err)
	}
	s.Profile = &p
	return nil
}

// SaveProfile writes through a temporary file so an interrupted write cannot
// leave the user without the answers they have already given.
func (s *Store) SaveProfile() error {
	s.Profile.Sort()
	b, err := json.MarshalIndent(s.Profile, "", "  ")
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	tmp := s.profilePath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if err := os.Rename(tmp, s.profilePath()); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	return nil
}
