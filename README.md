# glean

Finds Canadian class action settlements you can claim from, works out whether
you are in the class, and tells you before the deadline passes.

Written in Go with no dependencies at all: the HTML parser, the robots.txt
parser and the OpenRouter client are all in this repository, on top of the
standard library.

```
$ glean scan
Claim these
-----------
  Packaged bread price-fixing
    new, and you are in the class | deadline 2027-06-30 | about $25
    https://canadianbreadsettlement.ca

Closing soon
------------
  Apple Gift Card Class Action Settlement
    closes in 12 days | deadline 2026-08-31 | proof required
    https://giftcardsettlement.ca

Answer these
------------
  Did you hold an account with CIBC between 2018-01-01 to 2022-12-31?
    unlocks about $340 across 3 settlement(s)  [glean answer account:cibc yes]
```

## Why

Nobody finds this money by looking for it. You do not wake up wondering whether
the bread you bought in 2009 was price-fixed, whether your bank's overdraft
disclosure was defective, or whether a social network you stopped using in 2016
settled a privacy action last spring. The settlements are real, the class
periods are wide enough to include most of the country, and the claim forms
often take under ten minutes. Then the deadline passes and the unclaimed
remainder goes back where it came from.

So the looking has to be automatic, and the interruption has to be rare enough
to be worth reading. glean reads a few dozen sources on a schedule, holds a
small profile of the ordinary facts a class definition turns on, and speaks up
only when something you can actually claim is about to close.

## Getting started

```bash
make build
./bin/glean scan
./bin/glean list
```

That much works with no account and no key. It will find settlements, their
stages, their deadlines and their claim portals. What it will not do is read a
class definition, because a class definition is prose and no regular expression
is going to parse it.

For that it needs a model. glean talks to [OpenRouter](https://openrouter.ai),
so any model on it that supports strict JSON schema will do:

```bash
export OPENROUTER_API_KEY=...
./bin/glean models          # live price list, sorted by what a scan costs
./bin/glean scan
./bin/glean ask
```

`glean models` prices every usable model against a real workload rather than
against a token count nobody can picture: what one settlement notice costs to
read, and what a full scan of the registry costs. The answer is cents, on every
model in the list, which is the useful thing to know. Pick on whether a model
gets the conjunctions in a class definition right, not on the price per token.

The default is `google/gemini-3.7-flash`. Override it per run with `-model`, or
once with `GLEAN_MODEL`.

`ask` is where the profile fills in. It puts the highest-value unanswered
question first, records the answer once, and never asks it again.

```bash
./bin/glean serve          # dashboard on localhost
./bin/glean watch          # scan every twelve hours and notify
```

## How eligibility works

A class definition is not a boolean question. "All persons resident in Canada
who purchased packaged bread at a Loblaw banner store between 2001 and 2021"
has an honest answer for most people, and that answer is "I live in Canada, I
have no idea about the bread".

Collapsing that to false loses money. Collapsing it to true wastes an evening on
a claim form you cannot complete. So eligibility is evaluated in
[Kleene's strong three-valued logic](internal/logic/tri.go): an unanswered
criterion is `unknown`, `unknown` propagates through conjunction, and a
settlement whose verdict is `unknown` becomes a question rather than a result.

Three things fall out of that, and they are the design:

**One disqualifying answer ends the interview.** `false` dominates conjunction,
so a single "no, I have never banked with BMO" rules out every BMO action
without any further questions about them.

**Alternatives are a group, not a list.** A bread class covers purchases at
Loblaws, No Frills, Zehrs, Independent and half a dozen other banners. Those
share a group, are OR'd, and the group is AND'd with everything else. Modelled
as a flat conjunction it would demand that you shopped at all of them; modelled
as a flat disjunction it would let anyone in. Once one banner comes back `true`
the rest of the group stops being asked about, because their answers cannot
change anything.

**Questions are priced and merged.** Each unanswered criterion carries the
extractor's estimate of how common it is, which turns an `unknown` verdict into
a probability, and the probability times the payout into an expected value. The
same question asked by three settlements is asked once, worth the sum. What you
see is the shortest interview that turns the most maybes into claims.

Province is deliberately asymmetric. A nationwide class covers a resident of
anywhere, so Alberta answers `yes` to it. A profile that says only "Canada"
cannot answer a class restricted to Ontario, so that comes back `unknown` and
becomes a question. Getting this backwards in either direction is how a matcher
tells someone confidently wrong things about their own eligibility.

## How a settlement gets found

Each pass is a funnel, and the ordering of the cheap checks is most of the
engineering. A daily scan looks at a few hundred pages and almost none of them
have changed since yesterday.

| | |
| --- | --- |
| `source/` | The registry is data, not code: a start URL and a rough idea of what a case URL looks like. Every site in it will be redesigned, and a scraper pinned to CSS selectors breaks silently the day that happens, which is the failure mode that costs money. A redesign here costs recall, not correctness. |
| `fetch/` | One request per second per host, robots.txt honoured as a hard gate, and conditional GET so an unchanged page costs a 304 and no bytes. |
| `htmlx/` | A linear tokenizer rather than a tree. Everything glean wants from a page is either the anchors or the visible prose, and malformed markup degrades into slightly worse text instead of an error. |
| `extract/` | A relevance score decides whether a page is a notice at all, which is what keeps the model from being handed several hundred partner biographies a day. Regular expressions read dates and amounts; the model reads the class definition. Every scan prints what it spent. |
| `store/` | An append-only event log with state derived by replaying it. The most important thing this program can tell you is that a deadline moved, and a store that overwrites cannot tell you that. |
| `dedupe/` | One proceeding reported by three sites is one row. Merging is conservative: a disagreement on a stated deadline or claim portal is treated as proof that two similar-sounding records are different cases. |
| `match/` | The three-valued evaluation above. |
| `notify/` | Thirty, fourteen, seven, three and one day out. The thirty-day one is the one that matters, because a claim needing a document you have to find is not something to learn about on the last morning. |

### The second hop

A law firm's case page says a settlement was approved. The administrator's page
says claims close on the fifteenth. The deadline is the entire reason to run
this program, and it is usually one link away on a domain registered for that
one settlement: `canadianbreadsettlement.ca`, `giftcardsettlement.ca`,
`trufflesettlement.ca`.

So when a case page leaves the deadline unanswered, glean scores the outbound
links, follows the one that looks like the official portal, and backfills from
it. One hop, off-site only, and only when something is still missing. In a scan
of the current registry this is the difference between one deadline and nine.

See [docs/models.md](docs/models.md) for how the model is chosen and what it
costs.

### What a real scan looks like

Against the current registry, with `google/gemini-3.7-flash`:

```
91 candidates, 91 fetched, 46 not relevant, 45 extracted (28 prime, 0 escalated), 45 new, 0 errors in 2m24s
59 model call(s), 187263 in / 71017 out tokens, $0.1448 (gemini-2.5-flash-lite / gemini-3.7-flash)
```

Forty-five records, forty-one distinct proceedings once duplicates are merged,
for fourteen cents. A second scan the next day re-reads none of the unchanged
pages and costs almost nothing.

Two model tiers, because reading a deadline is transcription and decomposing a
class definition is not. See [docs/models.md](docs/models.md).

## Sources

Eighteen in the registry, eleven enabled. The rest are listed with their
patterns intact and a note saying why they are off, because "we could not read
this one" is information and deleting it just means someone adds it again next
year.

```bash
glean sources check          # probe every listing page, report what still works
glean sources check -links   # print what a page actually links to
```

That command exists because a registry of third-party websites is wrong the
moment it is written and gets wronger. A scraper that quietly finds nothing is
the expensive failure, so the answer to "does this still work" has to come from
the network rather than from anyone's memory of what the URL used to be.

Four sources are disabled because they answer 403 to Go's TLS client hello while
accepting the identical request from curl. That is bot management fingerprinting
the TLS handshake, and getting past it would mean forging a browser fingerprint.
glean does not do that. It sends a User-Agent that says what it is and links to
this repository, retries once with a common browser agent when a site refuses
outright, and treats robots.txt as a hard stop that no retry touches.

## What it does not do

It does not file claims for you. A claim form is a legal attestation about your
own life, some of them ask for banking details, and none of that is a decision
to hand to a program that inferred your eligibility from a web page. glean gets
you to the right form with the right deadline and the reasoning it used, and
stops.

It does not know anything the pages do not say. Roughly half the case pages in
the current registry state no deadline anywhere, on the page or on the portal,
because one has not been set. Those show as `unknown` rather than as a guess.

It is not legal advice, and the extraction is a program reading a web page.
Check the official notice before you rely on a date.

## Tests

```bash
make test
```

The parts worth testing are the ones where being subtly wrong is expensive: the
Kleene truth tables written out cell by cell, the province asymmetry, the
alternative-group behaviour, class periods that do and do not overlap, robots.txt
longest-match, HTML that is malformed in the ways real HTML is malformed, and a
full scan against a local server that checks the second pass costs one
conditional request and produces no events.

The model's judgment about a class definition is not tested, because it cannot
be tested from here. Everything around it is: that the schema is sent, that the
response decodes, that an unknown criterion kind is folded rather than trusted,
and that a fabricated claim URL is dropped rather than shown to someone about to
click it.
