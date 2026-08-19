// Package web serves the dashboard.
//
// It is server-rendered HTML with no client-side framework and no API, because
// the whole application is one person's list of a few dozen settlements and
// anything more is machinery for its own sake. The pages that matter are the
// question queue, which is how the profile fills in, and the per-settlement
// page, which shows the reasoning rather than only the verdict: the value of
// knowing you are probably in a class is entirely in being able to check why.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/navingamage/glean/internal/logic"
	"github.com/navingamage/glean/internal/match"
	"github.com/navingamage/glean/internal/model"
	"github.com/navingamage/glean/internal/notify"
	"github.com/navingamage/glean/internal/store"
)

//go:embed templates/*.html
var files embed.FS

type Server struct {
	store *store.Store
	tmpl  *template.Template
}

func New(st *store.Store) (*Server, error) {
	funcs := template.FuncMap{
		// dict lets one item-list template be reused with a different heading,
		// which is the only templating trick this UI needs.
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict needs an even number of arguments")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
	}
	t, err := template.New("").Funcs(funcs).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return &Server{store: st, tmpl: t}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /s/{id}", s.show)
	mux.HandleFunc("POST /answer", s.answer)
	mux.HandleFunc("POST /claim", s.claim)
	return mux
}

type page struct {
	Title string
	Count int
	Dir   string
}

func (s *Server) page(title string) page {
	return page{Title: title, Count: len(s.store.Settlements()), Dir: s.store.Dir}
}

type listRow struct {
	ID, Verdict, Value, Deadline, Stage, Title string
	Class, DeadlineClass                       string
}

type claimRow struct {
	SettlementID, Title, State, FiledOn string
	Amount                              float64
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	primaries, canonical := s.store.Primaries()
	byID := map[string]*model.Settlement{}
	var as []match.Assessment
	for _, st := range primaries {
		byID[st.ID] = st
		as = append(as, match.Assess(st, s.store.Profile))
	}
	match.Rank(as, byID)

	d := notify.View(s.store)
	questions := match.Questions(as)
	if len(questions) > 6 {
		questions = questions[:6]
	}

	var rows []listRow
	for _, a := range as {
		st := byID[a.SettlementID]
		if st == nil {
			continue
		}
		rows = append(rows, listRow{
			ID: st.ID, Verdict: verdict(a), Value: value(a),
			Deadline: deadlineText(st), Stage: string(st.Stage), Title: st.Title,
			Class: verdictClass(a), DeadlineClass: deadlineClass(st),
		})
	}

	var claims []claimRow
	for _, c := range s.store.Claims() {
		st := byID[canonical[c.SettlementID]]
		if st == nil || c.State == model.StateNew {
			continue
		}
		claims = append(claims, claimRow{
			SettlementID: c.SettlementID, Title: st.Title,
			State: string(c.State), FiledOn: c.FiledOn.String(), Amount: c.Amount,
		})
	}

	s.render(w, "index.html", struct {
		page
		Digest    *notify.Digest
		Questions []match.Question
		All       []listRow
		Claims    []claimRow
		Headline  string
	}{
		page:      s.page("Dashboard"),
		Digest:    d,
		Questions: questions,
		All:       rows,
		Claims:    claims,
		Headline:  headline(d, questions, match.TotalAtStake(questions, byID, as)),
	})
}

func headline(d *notify.Digest, qs []match.Question, atStake float64) string {
	var parts []string
	if n := len(d.Closing); n > 0 {
		parts = append(parts, fmt.Sprintf("%d closing soon", n))
	}
	if n := len(d.Maybes); n > 0 {
		parts = append(parts, fmt.Sprintf("%d possible", n))
	}
	if len(qs) > 0 {
		parts = append(parts, fmt.Sprintf("%d questions worth about $%.0f", len(qs), atStake))
	}
	if len(parts) == 0 {
		return "Nothing needs you. Run a scan to look again."
	}
	return strings.Join(parts, " · ")
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	st := s.store.Settlement(r.PathValue("id"))
	if st == nil {
		http.NotFound(w, r)
		return
	}
	a := match.Assess(st, s.store.Profile)

	state := string(model.StateNew)
	if c := s.store.Claim(st.ID); c != nil {
		state = string(c.State)
	}

	s.render(w, "show.html", struct {
		page
		S             *model.Settlement
		A             match.Assessment
		Verdict       string
		Deadline      string
		DeadlineClass string
		Payout        float64
		Confidence    float64
		ClaimState    string
		States        []string
	}{
		page:          s.page(st.Title),
		S:             st,
		A:             a,
		Verdict:       verdict(a),
		Deadline:      deadlineText(st),
		DeadlineClass: deadlineClass(st),
		Payout:        a.Payout,
		Confidence:    st.Confidence * 100,
		ClaimState:    state,
		States: []string{
			string(model.StateInterested), string(model.StateFiled),
			string(model.StatePaid), string(model.StateSkipped),
		},
	})
}

func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	kindStr, entity, ok := strings.Cut(r.FormValue("id"), ":")
	if !ok {
		http.Error(w, "malformed question id", http.StatusBadRequest)
		return
	}
	kind := model.CriterionKind(kindStr)
	if !model.ValidKind(kind) {
		http.Error(w, "unknown criterion kind", http.StatusBadRequest)
		return
	}
	negative := r.FormValue("a") != "yes"
	label := r.FormValue("label")
	if label == "" {
		label = entity
	}

	if kind == model.KindResidency {
		code := model.NormalizeProvince(entity)
		if code == "" {
			http.Error(w, "unknown province", http.StatusBadRequest)
			return
		}
		p := s.store.Profile
		var kept []model.Residence
		for _, res := range p.Residences {
			if res.Province == code || (res.Province == "CA" && code != "CA") {
				continue
			}
			kept = append(kept, res)
		}
		if !negative {
			kept = append(kept, model.Residence{Province: code})
		}
		p.Residences = kept
		if err := s.store.SaveProfile(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else if err := s.store.Answer(model.Holding{
		Kind: kind, Entity: entity, Label: label, Negative: negative,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r)
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if s.store.Settlement(id) == nil {
		http.Error(w, "no such settlement", http.StatusNotFound)
		return
	}
	state := model.ClaimState(r.FormValue("state"))
	switch state {
	case model.StateNew, model.StateInterested, model.StateFiled,
		model.StatePaid, model.StateRejected, model.StateSkipped:
	default:
		http.Error(w, "unknown state", http.StatusBadRequest)
		return
	}
	c := s.store.Claim(id)
	if c == nil {
		c = &model.Claim{SettlementID: id}
	}
	c.State = state
	if state == model.StateFiled && c.FiledOn.Zero() {
		c.FiledOn = model.Today()
	}
	if state == model.StatePaid {
		c.PaidOn = model.Today()
	}
	if err := s.store.SetClaim(c); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/s/"+id, http.StatusSeeOther)
}

// redirectBack returns to the page the form was submitted from, defaulting to
// the dashboard. Only same-site paths are honoured, so a crafted form cannot
// turn the dashboard into an open redirect.
func redirectBack(w http.ResponseWriter, r *http.Request) {
	back := r.FormValue("back")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t, err := s.tmpl.Clone()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := t.ParseFS(files, "templates/"+name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func verdict(a match.Assessment) string {
	switch a.Verdict {
	case logic.True:
		return "yes"
	case logic.False:
		return "no"
	default:
		if a.Probability > 0 {
			return fmt.Sprintf("%d%%", int(a.Probability*100))
		}
		return "?"
	}
}

func verdictClass(a match.Assessment) string {
	if a.Verdict == logic.True {
		return "yes"
	}
	return ""
}

func value(a match.Assessment) string {
	if a.Payout <= 0 {
		return "?"
	}
	if a.Verdict == logic.True {
		return fmt.Sprintf("$%.0f", a.Payout)
	}
	return fmt.Sprintf("~$%.0f", a.ExpectedValue)
}

func deadlineText(s *model.Settlement) string {
	if s.ClaimDeadline.Zero() {
		return "unknown"
	}
	d := s.ClaimDeadline.DaysUntil()
	if d < 0 {
		return s.ClaimDeadline.String() + " (past)"
	}
	return fmt.Sprintf("%s (%dd)", s.ClaimDeadline, d)
}

func deadlineClass(s *model.Settlement) string {
	switch {
	case s.Expired():
		return "past"
	case !s.ClaimDeadline.Zero() && s.ClaimDeadline.DaysUntil() <= 30:
		return "soon"
	}
	return ""
}
