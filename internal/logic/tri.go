// Package logic implements Kleene's strong three-valued logic.
//
// Eligibility for a class action is not a boolean question. A class definition
// says "everyone resident in Ontario who bought bread at a Loblaws banner store
// between 2001 and 2021", and the honest answer for most people is "I live in
// Ontario, I have no idea about the bread". Collapsing that to false loses money
// and collapsing it to true wastes the user's afternoon on a claim form they
// cannot complete.
//
// So an unanswered criterion evaluates to Unknown, Unknown propagates through
// conjunction unless some other criterion is already False, and a settlement
// whose overall verdict is Unknown becomes a question to put to the user rather
// than a result to show them. Answering that question once turns it into a fact
// that every future settlement can reuse.
package logic

// Tri is a three-valued truth value. The zero value is Unknown, which is the
// right default: a profile starts out saying nothing about anybody.
type Tri uint8

const (
	Unknown Tri = iota
	True
	False
)

func (t Tri) String() string {
	switch t {
	case True:
		return "yes"
	case False:
		return "no"
	default:
		return "unknown"
	}
}

// FromBool lifts a boolean into Tri. There is no inverse: collapsing Unknown to
// a boolean is exactly the mistake this package exists to prevent, so callers
// have to say which way they want it to fall.
func FromBool(b bool) Tri {
	if b {
		return True
	}
	return False
}

// Not is Kleene negation. Unknown negates to itself.
func Not(a Tri) Tri {
	switch a {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

// And is Kleene conjunction: False dominates, then Unknown, then True. A single
// disqualifying criterion is enough to rule a settlement out no matter how much
// else is unresolved, which is what keeps the question list short.
func And(a, b Tri) Tri {
	if a == False || b == False {
		return False
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return True
}

// Or is Kleene disjunction: True dominates. Used for alternative-group criteria,
// where a class covers purchases from any one of a list of banner stores.
func Or(a, b Tri) Tri {
	if a == True || b == True {
		return True
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return False
}

// AndAll folds And over a slice. The empty conjunction is True: a settlement
// with no extracted criteria is open to everyone until proven otherwise.
func AndAll(vs []Tri) Tri {
	out := True
	for _, v := range vs {
		out = And(out, v)
	}
	return out
}

// OrAll folds Or over a slice. The empty disjunction is False.
func OrAll(vs []Tri) Tri {
	out := False
	for _, v := range vs {
		out = Or(out, v)
	}
	return out
}
