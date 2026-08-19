package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Date is a calendar date with no clock and no zone. Class periods are written
// in court documents as plain dates, and dragging a time zone into the
// comparison only creates off-by-one-day bugs at the boundaries of a class
// period that spans twenty years.
//
// The zero Date is "unbounded" rather than "year zero". Class periods are
// frequently open at one end ("from January 1, 2019 to the present"), and a
// deadline that has not been announced yet is not a deadline in the past.
type Date struct {
	Y, M, D int
}

// DateOf converts a time.Time to a Date using its own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Y: y, M: int(m), D: d}
}

// Today is the current date in the local zone. Deadlines are legal deadlines,
// which are quoted in local time, so the local zone is the honest reference.
func Today() Date { return DateOf(time.Now()) }

// Zero reports whether the date is unbounded.
func (d Date) Zero() bool { return d.Y == 0 && d.M == 0 && d.D == 0 }

func (d Date) Time() time.Time {
	return time.Date(d.Y, time.Month(d.M), d.D, 0, 0, 0, 0, time.Local)
}

func (d Date) String() string {
	if d.Zero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Y, d.M, d.D)
}

// Before reports whether d falls strictly before e. Unbounded dates are not
// ordered, so a zero operand always answers false; callers check Zero first.
func (d Date) Before(e Date) bool {
	if d.Zero() || e.Zero() {
		return false
	}
	return d.Time().Before(e.Time())
}

func (d Date) After(e Date) bool { return e.Before(d) }

// DaysUntil counts whole days from today to d. Negative means d has passed.
func (d Date) DaysUntil() int {
	if d.Zero() {
		return 0
	}
	return int(d.Time().Sub(Today().Time()).Hours() / 24)
}

func ParseDate(s string) (Date, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Date{}, nil
	}
	// Accept the ISO date the extractor is asked for, and the RFC3339 stamps
	// that occasionally come back from sources that store deadlines as times.
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return DateOf(t), nil
		}
	}
	return Date{}, fmt.Errorf("model: cannot parse date %q", s)
}

func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// Window is a date range, either end of which may be unbounded.
type Window struct {
	From  Date `json:"from"`
	Until Date `json:"until"`
}

func (w Window) Zero() bool { return w.From.Zero() && w.Until.Zero() }

func (w Window) String() string {
	switch {
	case w.Zero():
		return "any time"
	case w.From.Zero():
		return "up to " + w.Until.String()
	case w.Until.Zero():
		return w.From.String() + " onward"
	}
	return w.From.String() + " to " + w.Until.String()
}

// Phrase renders the window as a clause that can be dropped into a question.
//
// String is for tables, where "2001-01-01 to 2021-12-31" is what you want.
// Questions are read aloud in someone's head, and gluing String onto "between"
// produces "between up to 2012-03-12", which is the kind of thing that makes a
// person distrust everything else on the screen.
func (w Window) Phrase() string {
	switch {
	case w.Zero():
		return ""
	case w.From.Zero():
		return "on or before " + w.Until.String()
	case w.Until.Zero():
		return "at any time since " + w.From.String()
	}
	return "between " + w.From.String() + " and " + w.Until.String()
}

// Contains reports whether d falls inside the window, inclusive at both ends.
func (w Window) Contains(d Date) bool {
	if d.Zero() {
		return false
	}
	if !w.From.Zero() && d.Before(w.From) {
		return false
	}
	if !w.Until.Zero() && d.After(w.Until) {
		return false
	}
	return true
}

// Overlaps reports whether two windows share at least one day. This is the
// operation eligibility actually needs: the user held a CIBC account from 2015
// to 2020 and the class period runs 2018 to 2022, so they were a customer during
// the class period even though neither range contains the other.
func (w Window) Overlaps(o Window) bool {
	if !w.Until.Zero() && !o.From.Zero() && w.Until.Before(o.From) {
		return false
	}
	if !o.Until.Zero() && !w.From.Zero() && o.Until.Before(w.From) {
		return false
	}
	return true
}
