package model

import "testing"

func d(s string) Date {
	v, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func TestWindowOverlaps(t *testing.T) {
	cases := []struct {
		name string
		a, b Window
		want bool
	}{
		{"identical", Window{d("2019-01-01"), d("2020-01-01")}, Window{d("2019-01-01"), d("2020-01-01")}, true},
		{"partial", Window{d("2015-01-01"), d("2020-01-01")}, Window{d("2018-01-01"), d("2022-01-01")}, true},
		{"touching at a point", Window{d("2015-01-01"), d("2018-01-01")}, Window{d("2018-01-01"), d("2022-01-01")}, true},
		{"disjoint", Window{d("2010-01-01"), d("2012-01-01")}, Window{d("2018-01-01"), d("2022-01-01")}, false},
		{"open start meets closed", Window{Date{}, d("2019-01-01")}, Window{d("2018-01-01"), d("2022-01-01")}, true},
		{"open start misses closed", Window{Date{}, d("2016-01-01")}, Window{d("2018-01-01"), d("2022-01-01")}, false},
		{"open end", Window{d("2021-01-01"), Date{}}, Window{d("2018-01-01"), d("2022-01-01")}, true},
		{"both unbounded", Window{}, Window{d("2018-01-01"), d("2022-01-01")}, true},
	}
	for _, c := range cases {
		if got := c.a.Overlaps(c.b); got != c.want {
			t.Errorf("%s: %v.Overlaps(%v) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
		if got := c.b.Overlaps(c.a); got != c.want {
			t.Errorf("%s (reversed): %v.Overlaps(%v) = %v, want %v", c.name, c.b, c.a, got, c.want)
		}
	}
}

func TestZeroDateIsUnbounded(t *testing.T) {
	var z Date
	if !z.Zero() {
		t.Fatal("zero Date should report Zero")
	}
	if z.Before(d("2020-01-01")) || d("2020-01-01").Before(z) {
		t.Fatal("an unbounded date must not order against a real one")
	}
	if s := z.String(); s != "" {
		t.Fatalf("zero Date renders as %q, want empty", s)
	}
}

func TestDateJSONRoundTrip(t *testing.T) {
	for _, in := range []string{"2026-08-18", ""} {
		var out Date
		b, err := d(in).MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if err := out.UnmarshalJSON(b); err != nil {
			t.Fatal(err)
		}
		if out.String() != in {
			t.Errorf("round trip of %q gave %q", in, out.String())
		}
	}
}
