package logic

import "testing"

func TestKleeneTables(t *testing.T) {
	// The full 3x3 tables, written out rather than generated, because the whole
	// point of the package is that these particular cells are right.
	and := map[[2]Tri]Tri{
		{True, True}: True, {True, Unknown}: Unknown, {True, False}: False,
		{Unknown, True}: Unknown, {Unknown, Unknown}: Unknown, {Unknown, False}: False,
		{False, True}: False, {False, Unknown}: False, {False, False}: False,
	}
	for in, want := range and {
		if got := And(in[0], in[1]); got != want {
			t.Errorf("And(%v,%v) = %v, want %v", in[0], in[1], got, want)
		}
	}
	or := map[[2]Tri]Tri{
		{True, True}: True, {True, Unknown}: True, {True, False}: True,
		{Unknown, True}: True, {Unknown, Unknown}: Unknown, {Unknown, False}: Unknown,
		{False, True}: True, {False, Unknown}: Unknown, {False, False}: False,
	}
	for in, want := range or {
		if got := Or(in[0], in[1]); got != want {
			t.Errorf("Or(%v,%v) = %v, want %v", in[0], in[1], got, want)
		}
	}
	for in, want := range map[Tri]Tri{True: False, False: True, Unknown: Unknown} {
		if got := Not(in); got != want {
			t.Errorf("Not(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestFoldIdentities(t *testing.T) {
	if got := AndAll(nil); got != True {
		t.Errorf("empty conjunction = %v, want True", got)
	}
	if got := OrAll(nil); got != False {
		t.Errorf("empty disjunction = %v, want False", got)
	}
	// One disqualifying clause outranks any number of open questions: this is
	// what stops the interview asking about a settlement already ruled out.
	if got := AndAll([]Tri{Unknown, Unknown, False, Unknown}); got != False {
		t.Errorf("AndAll with a False = %v, want False", got)
	}
	if got := AndAll([]Tri{True, Unknown, True}); got != Unknown {
		t.Errorf("AndAll with an Unknown = %v, want Unknown", got)
	}
}

func TestZeroValueIsUnknown(t *testing.T) {
	var z Tri
	if z != Unknown {
		t.Fatalf("zero Tri = %v, want Unknown", z)
	}
}
