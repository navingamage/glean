# Sources

The registry lives in [`internal/source/sources.json`](../internal/source/sources.json)
and is embedded into the binary. Point `-sources` at your own copy to override it
without rebuilding.

## Shape of an entry

```json
{
  "id": "canooq",
  "name": "Canooq class action tracker",
  "kind": "aggregator",
  "jurisdiction": ["CA"],
  "listing": ["https://www.canooq.ca/class-actions"],
  "follow": ["^https://canooq\\.ca/class-actions/[^/]+$"],
  "exclude": ["start-a-class-action"],
  "disabled": false,
  "notes": "why this entry is the way it is"
}
```

`listing` is where a pass starts. `follow` decides which links off those pages
are case pages, matched against the identity form of the URL: scheme normalized
to https, `www` removed. That is why a pattern can be written once as
`^https://canooq\.ca/` and still catch a site that links to itself as
`http://www.canooq.ca/`.

`kind` sets source authority, which is a statement about how far to trust a date
found there. An administrator publishes the deadline it will enforce; an
aggregator is repeating what it read somewhere else.

| kind | authority |
| --- | --- |
| `administrator` | 4 |
| `registry` | 3 |
| `firm` | 2 |
| `aggregator` | 1 |

## Adding one

```bash
glean sources check -links -only <id>
```

prints the top-scoring links on each listing page with the follow patterns
switched off, which is how you find out what a site's case URLs actually look
like without opening a browser. Write a pattern against that, then:

```bash
glean sources check -only <id>
```

A working entry reports `ok` with a non-zero case count. Zero means the listing
page fetched but the pattern matched nothing, which is the failure that looks
like success in a scan.

## Why the patterns are loose

Every site in this list will be redesigned. A scraper pinned to CSS selectors
breaks silently the day that happens: it keeps returning zero results and nobody
notices until a deadline has passed.

So a source says only where to start and roughly what a case URL looks like, and
everything after that is scoring on the page's own words: keywords in the link
text and the URL to order the queue, then a relevance score on the full page
text to decide whether it is a notice at all. A redesign costs recall, not
correctness, and `sources check` will say so.

## Disabled entries, and why they stay

Eighteen entries, eleven enabled. The disabled ones keep their patterns and
carry a note saying what went wrong, because "we could not read this one" is
information, and deleting it just means someone adds it again next year.

**TLS fingerprinting.** Top Class Actions Canada, Koskie Minsky, Strosberg and
RicePoint answer 403 to Go's TLS client hello and 200 to the identical request
from curl. The block is on the shape of the handshake, not on anything in the
request, so no header changes it. Getting past it would mean forging a browser
fingerprint, which is where this program stops. Top Class Actions in particular
is the best consumer source in the country, so it is worth re-checking.

**Robots.** The Canadian Bar Association's national database disallows the path
in robots.txt. That is a hard stop; nothing in glean retries past it. The
database is also voluntary and postback-driven, so coverage would be partial
even with permission.

**Rendered behind a form.** The Quebec register fetches fine, but its case list
comes from a search form rather than from links, so a linear scrape reaches only
the explanatory pages. Making it work would mean submitting the form.

**Nothing to list.** Epiq's Canadian site markets the administration service
without naming the settlements it administers, so every link is a service page.

## Politeness

One request per second per host, or the site's own `Crawl-delay` if it asks for
longer. robots.txt is fetched once per host and cached for the run, with a
missing or unreadable file read as no restrictions. The User-Agent names the
program and links to the repository, so an administrator who wants it gone has
something to act on rather than an anonymous scraper to block.

A 403 or 406 is retried once with a common browser User-Agent. That is a
deliberate line: robots.txt is the machine-readable statement of what a site
wants crawled and is honoured absolutely, whereas a 403 on a public page that
robots.txt explicitly permits is a firewall rule matching on the shape of a
string. Retrying once, at one request per second, for one person's own
settlement notices, is a different thing from evading a stated policy.
