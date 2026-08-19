# Deploying glean

glean in production is a Cloudflare Worker with a D1 database. It scans on a
schedule and serves a private dashboard.

A Worker rather than a Pages project for one reason: **Pages Functions cannot be
scheduled**. The premise of glean is that it looks for this money without being
asked, and a deployment that only ran when someone visited it would give that
back. Every other Antipode property is a static Pages site; this one is not, and
that difference is deliberate.

## Status

Live at **https://glean.antipodetech.com**, gated, scanning every ten minutes.
The `workers.dev` URL is deliberately disabled so there is one door.

## Signing in

**Once per device, not once per visit.** The key is exchanged for a cookie that
lasts a year, so after the first time you just open the URL.

```bash
Scripts/signin          # opens the browser on this Mac, already signed in
Scripts/signin qr       # writes a QR to the Desktop; scan it with a phone, once
Scripts/signin copy     # puts the sign-in URL on the clipboard
```

The key lives in the Keychain and none of these print it. The QR is rendered
locally, so the key never leaves the machine and no service sees it; delete the
file afterwards. `signin qr` wants `segno` (`pip install segno`) and says so if
it is missing, in which case `signin copy` needs nothing.

Doing it by hand, if you would rather:

```bash
open "https://glean.antipodetech.com/?k=$(security find-generic-password -a "$USER" -s antipode-glean-access-key -w)"
```

Enabling Cloudflare Access retires all of this: you get an email code on any
device and there is no key to carry.

## What you have to do first

**The existing `CLOUDFLARE_API_TOKEN` cannot deploy this.** It is scoped to
Pages only, which is all the other properties need. Verified:

```
GET /accounts/:id/workers/scripts  -> Authentication error [code 10000]
GET /accounts/:id/d1/database      -> Authentication error [code 10000]
GET /accounts/:id/pages/projects   -> works
```

In the Cloudflare dashboard, under **My Profile → API Tokens**, edit the
existing token (or make a second one) and add:

| permission | scope | why |
| --- | --- | --- |
| Workers Scripts | Edit | deploy the Worker |
| D1 | Edit | create and migrate the database |
| Workers KV Storage | Edit | wrangler's deployment bookkeeping |
| Zone → DNS | Edit | attach `glean.antipodetech.com` |
| Zone → Workers Routes | Edit | see below; currently missing |

The custom domain is attached and working, but every `wrangler deploy` ends with
a non-fatal error reconciling `/zones/:id/workers/routes`. The Worker uploads and
goes live regardless; only the route re-assertion fails. Adding **Workers
Routes: Edit** to the token clears it.

Then replace the Keychain item, which prompts so the token never enters shell
history:

```bash
security add-generic-password -U -a "$USER" -s antipode-cloudflare-api-token -w
```

If you would rather keep the Pages token untouched, add a second item and load
it only for this deploy. Nothing else in the workspace needs the wider scopes.

## Deploy

```bash
cd worker
source ~/.config/antipode/load-secrets.sh

npm install
npm test                     # 31 tests, no network, no account needed

wrangler d1 create glean     # copy the database_id into wrangler.toml
npm run db:remote            # apply schema.sql

wrangler secret put GLEAN_ACCESS_KEY     # see below
wrangler secret put OPENROUTER_API_KEY   # the same key the CLI uses
wrangler secret put GLEAN_WEBHOOK        # optional, for phone notifications

wrangler deploy
```

`wrangler deploy` prints a `glean.<subdomain>.workers.dev` URL. To put it on the
house domain, add to `wrangler.toml`:

```toml
routes = [{ pattern = "glean.antipodetech.com", custom_domain = true }]
```

and deploy again. Cloudflare creates the DNS record and the certificate; expect a
few minutes and some 522s while that provisions.

## The access key

**Generate a long random one and do not reuse a password:**

```bash
openssl rand -base64 32
```

The dashboard is gated by that single shared key, because there is exactly one
user. The Worker refuses to serve anything when the key is unset: a deployment
that forgot the secret should be visibly broken, never quietly public.

Sign in once per device by visiting `https://glean.antipodetech.com/?k=THE_KEY`.
The Worker sets an HttpOnly, Secure, SameSite=Lax cookie for a year and redirects
to the bare path, so the key does not linger in history, in a bookmark or in a
Referer header. Every page is served `no-store` and `noindex, nofollow`.

This matters more here than on a normal site. The profile behind the dashboard is
a list of every bank, shop, insurer and employer you have answered a question
about. It should not be one guessed URL away from anyone.

## Access, and per-person profiles

Settlements are shared; profiles and claims are not. A settlement is a public
fact about a proceeding that costs money to extract, and scanning once for
everybody is the whole efficiency argument. What somebody answered about their
own bank accounts belongs to them.

Identity comes from Cloudflare Access when it is in front. Set two vars and
redeploy:

```toml
GLEAN_ACCESS_TEAM = "your-team"    # <team>.cloudflareaccess.com
GLEAN_ACCESS_AUD  = "the application audience tag"
```

Until those are set, every signed-in request is a single `owner` identity, which
is the right answer when the shared key is the only gate.

The `Cf-Access-Authenticated-User-Email` header is never trusted. Access sets it,
but so can anyone reaching the Worker without going through Access, and trusting
it would let a stranger read any user's profile by naming them. What is verified
is `Cf-Access-Jwt-Assertion`: an RS256 signature checked against the team's
published keys, with issuer, audience and expiry all checked. There are tests for
the forgeries, including `alg: none` and a token signed with the wrong key.

At the time of deployment the Cloudflare API reported Access as **not enabled**
on this account (`access.api.error.not_enabled` from two separate endpoints), so
the shared key is doing the work. That is worth confirming in the Zero Trust
dashboard before relying on per-person profiles.

## Scheduling, and why it runs so often

Every ten minutes, each run doing very little. That is not a preference; it is
what the platform allows, and it took three attempts to find out.

A first deployment ran the scan on a daily cron with a four-minute budget, the
same shape the Go version uses. It stored three settlements and never recorded
that it had run at all. The second attempt assumed the cause was the subrequest
cap, fifty per invocation on the free plan against a registry needing several
hundred, and added a request budget with least-recently-scanned source rotation.
It got to six settlements and still never recorded a summary.

The actual constraint is wall clock inside `waitUntil`. A prime extraction takes
five to twenty seconds, so any pass measured in minutes is killed by the platform
before it can commit anything about itself, which is exactly how a run that had
quietly stored six settlements managed to look like it had done nothing.

So a pass is held to twenty seconds and forty outbound requests, and reports
honestly when it stops:

```
50 candidates, 10 fetched, 0 unchanged, 7 not relevant, 3 extracted, 3 new,
15 requests over 2 source(s), stopped on budget and will resume next run
3 model call(s), $0.0017
```

Stopping is the normal case, not a failure. Everything extracted is committed as
it goes, sources are taken least-recently-scanned first so coverage rotates
rather than rescanning the top of the list, and a page already read costs one
request because its content hash is stored. A cold registry takes most of a day
to work through at roughly three pages per run. After that the runs are nearly
free: a page whose readable text has not changed is never sent to a model, so
frequent invocations cost requests rather than money.

Trigger a pass by hand from the dashboard's **Scan now** button, or:

```bash
curl -X POST -H "Cookie: glean_session=$(security find-generic-password -a "$USER" -s antipode-glean-access-key -w)" \
  https://glean.antipodetech.com/scan
```

Locally: `wrangler dev --test-scheduled`, then `curl localhost:8788/__scheduled`.

## Cost

The scan's cost is the model, not Cloudflare. Workers and D1 free tiers cover
this comfortably: one scheduled invocation a day and a few dozen dashboard views.

A cold first scan over the full registry runs to roughly twenty to thirty cents.
After that a daily scan costs close to nothing, because a page whose readable
text has not changed is never sent to a model. See `../docs/models.md`.

## Local development

```bash
cp .dev.vars.example .dev.vars    # gitignored; put a throwaway access key in it
npm run db:local
npx wrangler dev --local --test-scheduled
```

`--local` runs D1 in Miniflare and never calls the Cloudflare API, so the whole
thing including a live scan can be exercised without an account. The port was
built and verified this way before it was ever deployed: 43 settlements scraped
from the real Canadian sources, inside the Worker runtime, with no key set.

## Relationship to the Go CLI

Both exist and neither replaces the other. The Go program in the repository root
is the local tool and the reference implementation; this is the hosted one.

The domain logic is duplicated across two languages, which is a real cost and is
not an accident: Workers runs JavaScript and WASM, and Go's networking and
filesystem do not survive that transition intact. The pure parts, three-valued
matching, entity normalization, deduplication, ranking and the heuristic
extractor, were ported with their tests so the behaviour is pinned rather than
approximated. Where the runtime is genuinely better, the port uses it: HTML
parsing here is `HTMLRewriter` rather than a hand-written tokenizer.

If the two ever disagree about whether someone is in a class, that is a bug in
this file's premise, and the tests on both sides are where to look.
