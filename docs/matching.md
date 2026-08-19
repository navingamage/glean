# Matching

How glean decides whether you are in a class, and what to ask you next.

## Three values, not two

The core is [`internal/logic`](../internal/logic/tri.go), Kleene's strong
three-valued logic. Truth values are `yes`, `no` and `unknown`, and the zero
value is `unknown`, which is the right default because a profile starts out
saying nothing about anybody.

```
AND     yes      unknown  no        OR      yes  unknown  no
yes     yes      unknown  no        yes     yes  yes      yes
unknown unknown  unknown  no        unknown yes  unknown  unknown
no      no       no       no        no      yes  unknown  no
```

Two cells do most of the work.

`AND(unknown, no) = no` is what makes the interview terminate. One disqualifying
answer rules a settlement out no matter how much else is unresolved, so glean
stops asking about it entirely.

`OR(yes, unknown) = yes` is what stops the interview being tedious. Once you have
said you shopped at Loblaws, the class's other eight banner names are settled and
their questions are never put to you, because the answers cannot change anything.

## Criteria and groups

A class definition is decomposed into criteria, each with a kind, an entity, and
a class period:

```
residency  CA        any time            resident in Canada
purchase   loblaw    2001-01-01..2021-12-31   group: banner
purchase   sobeys    2001-01-01..2021-12-31   group: banner
```

Criteria sharing a `group` are alternatives, OR'd together. Everything else is
required, AND'd. Getting this backwards in either direction produces a matcher
that is confidently wrong: as a flat conjunction it demands you shopped at nine
stores at once, as a flat disjunction it lets anyone who shopped anywhere into a
class defined by residency.

Criteria with no group are each their own group, because an unlabelled clause is
a requirement in its own right.

## Evaluating one criterion

Everything except residency looks for a holding on the profile with the same
kind and a matching entity key. Matching is by normalized key, and the
normalizer folds banner names onto their parent, so a profile that says
"Loblaws" answers a criterion phrased as "No Frills".

| profile state | value |
| --- | --- |
| no holding on file | `unknown`, and a question is raised |
| holding marked negative | `no` |
| holding with no stated window | `yes` |
| holding whose window overlaps the class period | `yes` |
| holding whose window is disjoint from the class period | `no` |

The undated case matters. A holding with no window is an assertion about the
person, not about a period, so it cannot contradict a class period. Reading
"I bank with CIBC" as "I banked with CIBC only today" would rule out almost
every class.

## Residency is asymmetric

Canada is a wildcard in one direction only.

- Class is `CA`, you live in Alberta: **yes**. A nationwide class covers a
  resident of anywhere.
- Class is `ON`, you live in Alberta: **no**.
- Class is `ON`, your profile says only `CA`: **unknown**, and you are asked
  which province. A profile that has only ever said "Canada" does not contain the
  answer.

The default profile is a single `CA` residence, so a new user is `unknown`
against every provincial class and `yes` against every national one, which is
exactly what is known about them.

## Turning unknown into money

Each criterion carries `prevalence`, the extractor's estimate of the fraction of
Canadian adults who satisfy it. "Resident in Canada" is 1.0; "shopped at a major
grocery chain over twenty years" is around 0.85; "was prescribed a specific
recalled drug" is around 0.001.

For a group that is still `unknown`, the chance that none of its members turns
out to be satisfied is the product of `1 - prevalence` over the open members, and
the group contributes one minus that. Multiplied across groups this gives
`P(you are in the class)`, and multiplied by the payout estimate it gives an
expected value.

Prevalence only ever affects ranking. It never affects the verdict, and a
settlement is never reported as a match on the strength of a prior.

Payout is taken in order of trustworthiness: a stated per-claimant range, then a
stated single figure, then the fund divided by an estimated class size with sixty
percent assumed to survive fees and administration. When a notice gives none of
the three the answer is zero, and zero means unknown rather than worthless: it
sorts below anything with a real number and above anything ruled out.

## No criteria means unknown, not eligible

The empty conjunction is `yes` as a matter of logic, and a settlement whose class
definition was never extracted has no criteria. Reporting that as "you are in the
class" would be the single most damaging thing this program could say, so it is
special-cased: no criteria gives `unknown`, a note explaining that the definition
could not be read, and a low prior. Most classes are narrower than "anyone".

This is the normal state of every settlement when there is no API key.

## Ranking

Sorting purely by expected value buries a forty dollar claim closing on Friday
underneath a two hundred dollar claim closing next spring, and the forty dollars
is the one at risk. So urgency multiplies value rather than sorting separately:
2.5x inside a week, 1.6x inside a month, 1.15x inside a quarter. A settlement not
yet accepting claims is worth watching rather than an evening, and is quartered.
Expired and ruled-out settlements sort to the bottom rather than disappearing,
because seeing why something was excluded is how you catch a wrong answer in your
own profile.

## Questions

An `unknown` criterion becomes a question with a stable id of `kind:entity`, so
answering it once retires it everywhere and forever. Questions are merged across
settlements and worth the sum of what they unlock, which is why `glean ask` puts
"did you hold an account with CIBC" ahead of a question about one obscure class:
the first one settles three files.

Skipping is a real answer to "I do not remember", and is recorded as neither yes
nor no. Recording a skip as a no would be a lie that costs money later.
