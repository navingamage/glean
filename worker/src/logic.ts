/**
 * Kleene's strong three-valued logic.
 *
 * Eligibility for a class action is not a boolean question. A class definition
 * says "everyone resident in Ontario who bought bread at a Loblaws banner store
 * between 2001 and 2021", and the honest answer for most people is "I live in
 * Ontario, I have no idea about the bread". Collapsing that to false loses money
 * and collapsing it to true wastes an afternoon on a claim form nobody can
 * complete.
 */
export const Unknown = 0;
export const True = 1;
export const False = 2;
export type Tri = typeof Unknown | typeof True | typeof False;

export function triString(t: Tri): string {
  return t === True ? "yes" : t === False ? "no" : "unknown";
}

/** False dominates, then Unknown, then True. */
export function and(a: Tri, b: Tri): Tri {
  if (a === False || b === False) return False;
  if (a === Unknown || b === Unknown) return Unknown;
  return True;
}

/** True dominates. Used for alternative-group criteria. */
export function or(a: Tri, b: Tri): Tri {
  if (a === True || b === True) return True;
  if (a === Unknown || b === Unknown) return Unknown;
  return False;
}

export function not(a: Tri): Tri {
  return a === True ? False : a === False ? True : Unknown;
}

/** The empty conjunction is True: no constraints means no obstacle. */
export function andAll(vs: Tri[]): Tri {
  return vs.reduce<Tri>((acc, v) => and(acc, v), True);
}

/** The empty disjunction is False. */
export function orAll(vs: Tri[]): Tri {
  return vs.reduce<Tri>((acc, v) => or(acc, v), False);
}
