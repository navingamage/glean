package notify

import (
	"strings"
	"testing"

	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/store"
)

func inDays(n int) model.Date {
	return model.DateOf(model.Today().Time().AddDate(0, 0, n))
}

func seed(t *testing.T, sets ...*model.Settlement) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sets {
		if _, _, err := st.Upsert(s); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestDigestFiresOnlyOnReminderMarks(t *testing.T) {
	// A notification that arrives every day is a notification nobody reads, so
	// the digest is silent on day twelve and speaks on day fourteen.
	quiet := seed(t, &model.Settlement{ID: "a", Title: "A", Stage: model.StageClaimsOpen,
		ClaimDeadline: inDays(12), PayoutLow: 50, PayoutHigh: 50})
	if d := Build(quiet, nil, nil); len(d.Closing) != 0 {
		t.Errorf("digest spoke on day 12: %v", d.Closing)
	}

	loud := seed(t, &model.Settlement{ID: "a", Title: "A", Stage: model.StageClaimsOpen,
		ClaimDeadline: inDays(14), PayoutLow: 50, PayoutHigh: 50})
	if d := Build(loud, nil, nil); len(d.Closing) != 1 {
		t.Errorf("digest stayed quiet on day 14")
	}
}

func TestViewShowsEverythingClosing(t *testing.T) {
	// A screen someone opened deliberately must not hide a claim that closes in
	// twelve days because today is not a reminder day.
	st := seed(t, &model.Settlement{ID: "a", Title: "A", Stage: model.StageClaimsOpen,
		ClaimDeadline: inDays(12), PayoutLow: 50, PayoutHigh: 50})
	d := View(st)
	if len(d.Closing) != 1 {
		t.Fatalf("dashboard hid a claim closing in 12 days: %+v", d)
	}
	if !strings.Contains(d.Closing[0].Reason, "12 days") {
		t.Errorf("reason = %q", d.Closing[0].Reason)
	}
}

func TestExpiredAndFiledClaimsAreNotNagged(t *testing.T) {
	st := seed(t,
		&model.Settlement{ID: "gone", Title: "Gone", Stage: model.StageClaimsOpen, ClaimDeadline: inDays(-3)},
		&model.Settlement{ID: "done", Title: "Done", Stage: model.StageClaimsOpen, ClaimDeadline: inDays(5)},
	)
	if err := st.SetClaim(&model.Claim{SettlementID: "done", State: model.StateFiled}); err != nil {
		t.Fatal(err)
	}
	d := View(st)
	for _, it := range d.Closing {
		t.Errorf("nagged about %s, which is %s", it.Settlement.ID, it.Reason)
	}
	if d.Empty() != true {
		t.Errorf("digest not empty: %+v", d)
	}
}

func TestTextRendersSomethingActionable(t *testing.T) {
	st := seed(t, &model.Settlement{
		ID: "a", Title: "Packaged bread", Stage: model.StageClaimsOpen,
		ClaimDeadline: inDays(7), PayoutLow: 25, PayoutHigh: 25,
		ClaimURL: "https://canadianbreadsettlement.ca",
	})
	out := View(st).Text()
	for _, want := range []string{"Packaged bread", "closes in 7 days", "$25", "canadianbreadsettlement.ca"} {
		if !strings.Contains(out, want) {
			t.Errorf("digest text missing %q:\n%s", want, out)
		}
	}
}
