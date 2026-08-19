package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/navingamage/glean/internal/model"
)

func settlement(id, deadline string, stage model.Stage) *model.Settlement {
	d, _ := model.ParseDate(deadline)
	return &model.Settlement{ID: id, Title: "Bread", Stage: stage, ClaimDeadline: d}
}

func TestUpsertRecordsDiscoveryThenChanges(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	isNew, changes, err := st.Upsert(settlement("a", "2026-10-15", model.StageSettled))
	if err != nil {
		t.Fatal(err)
	}
	if !isNew || len(changes) != 0 {
		t.Fatalf("first sighting: isNew=%v changes=%v", isNew, changes)
	}

	// Same page, same content: no event, no noise in tomorrow's digest.
	isNew, changes, err = st.Upsert(settlement("a", "2026-10-15", model.StageSettled))
	if err != nil {
		t.Fatal(err)
	}
	if isNew || len(changes) != 0 {
		t.Fatalf("unchanged sighting: isNew=%v changes=%v", isNew, changes)
	}

	// A deadline that moves is the single most important thing this program can
	// notice, so it has to survive as a change with both values attached.
	_, changes, err = st.Upsert(settlement("a", "2026-09-01", model.StageClaimsOpen))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("want stage and deadline changes, got %v", changes)
	}
	var found bool
	for _, c := range changes {
		if c.Field == "claim_deadline" && c.From == "2026-10-15" && c.To == "2026-09-01" {
			found = true
		}
	}
	if !found {
		t.Errorf("deadline change did not record both values: %v", changes)
	}
}

func TestFirstSeenSurvivesReExtraction(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := settlement("a", "2026-10-15", model.StageSettled)
	old.FirstSeen, _ = model.ParseDate("2025-01-01")
	if _, _, err := st.Upsert(old); err != nil {
		t.Fatal(err)
	}
	// A record rebuilt this morning carries today's date; taking it would report
	// every settlement in the store as discovered today.
	fresh := settlement("a", "2026-09-01", model.StageClaimsOpen)
	if _, _, err := st.Upsert(fresh); err != nil {
		t.Fatal(err)
	}
	if got := st.Settlement("a").FirstSeen.String(); got != "2025-01-01" {
		t.Errorf("first seen = %q, want 2025-01-01", got)
	}
}

func TestStateIsRebuiltByReplay(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert(settlement("a", "2026-10-15", model.StageClaimsOpen)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClaim(&model.Claim{SettlementID: "a", State: model.StateFiled, Amount: 25}); err != nil {
		t.Fatal(err)
	}
	if err := st.Answer(model.Holding{Kind: model.KindPurchase, Entity: "Loblaws", Label: "Loblaws"}); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Settlement("a") == nil {
		t.Error("settlement did not survive replay")
	}
	if c := reopened.Claim("a"); c == nil || c.State != model.StateFiled {
		t.Errorf("claim did not survive replay: %+v", c)
	}
	if len(reopened.Profile.Holdings) != 1 {
		t.Errorf("profile did not survive: %+v", reopened.Profile.Holdings)
	}
}

func TestCorruptLineDoesNotCostTheHistory(t *testing.T) {
	// A crash mid-write leaves a partial line. Losing everything before it, or
	// refusing to start at all, would be a far worse outcome than skipping it.
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert(settlement("a", "2026-10-15", model.StageClaimsOpen)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"at":"2026-01-01T00:00:00Z","type":"disc` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, _, err := st.Upsert(settlement("b", "2026-11-01", model.StageClaimsOpen)); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("a truncated line made the store unopenable: %v", err)
	}
	if len(reopened.Settlements()) != 2 {
		t.Errorf("recovered %d settlements, want 2", len(reopened.Settlements()))
	}
}

func TestProfileWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Answer(model.Holding{Kind: model.KindAccount, Entity: "CIBC", Label: "CIBC"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "profile.json.tmp")); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
	b, err := os.ReadFile(filepath.Join(dir, "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Error("profile written empty")
	}
}

func TestContentHashSurvivesAnUneventfulReExtraction(t *testing.T) {
	// The regression this exists for: a page whose deadline and stage have not
	// moved produces no event, so the first implementation dropped the new
	// content hash on the floor and re-extracted that page at full price on
	// every scan, forever. The hash index is written separately for exactly this.
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert(settlement("a", "2026-10-15", model.StageClaimsOpen)); err != nil {
		t.Fatal(err)
	}
	st.RecordHash("a", "hash-v1")
	if err := st.SaveHashes(); err != nil {
		t.Fatal(err)
	}

	// A second sighting that changes nothing worth an event.
	_, changes, err := st.Upsert(settlement("a", "2026-10-15", model.StageClaimsOpen))
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("expected an uneventful re-extraction, got %v", changes)
	}
	st.RecordHash("a", "hash-v2")
	if err := st.SaveHashes(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.SeenHash("a"); got != "hash-v2" {
		t.Errorf("hash after reopen = %q, want hash-v2", got)
	}
}

func TestSaveHashesIsAtomicAndSkipsWhenClean(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing recorded: no file, and no error.
	if err := st.SaveHashes(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hashes.json")); !os.IsNotExist(err) {
		t.Error("wrote an index with nothing in it")
	}

	st.RecordHash("a", "h")
	if err := st.SaveHashes(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hashes.json.tmp")); !os.IsNotExist(err) {
		t.Error("left the temporary file behind")
	}
	if got := st.SeenHash("a"); got != "h" {
		t.Errorf("SeenHash = %q", got)
	}
	if got := st.SeenHash("missing"); got != "" {
		t.Errorf("SeenHash for an unknown id = %q, want empty", got)
	}
}

func TestCorruptHashIndexCostsMoneyNotCorrectness(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hashes.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("a corrupt index made the store unopenable: %v", err)
	}
	if got := st.SeenHash("a"); got != "" {
		t.Errorf("SeenHash = %q, want empty so the page is re-read", got)
	}
}
