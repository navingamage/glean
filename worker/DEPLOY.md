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

A username and a password on a form at `/login`. Out of the box they are:

```
test / test
```

Which is deliberate: the deployment is meant to be openable by anyone it is
shown to, without a key being carried between devices first. Signing in sets a
cookie that lasts a year, so it is once per device rather than once per visit,
and **Sign out** on the dashboard clears it.

To change them, set both as secrets and redeploy:

```bash
wrangler secret put GLEAN_USERNAME
wrangler secret put GLEAN_PASSWORD
```

The cookie is not the password. It is an HMAC of the username under the
password, so a stolen cookie cannot be read back into the credential that minted
it, and changing the password signs every device out at once. It is HttpOnly,
Secure and SameSite=Lax, every page is served `no-store` and `noindex, nofollow`,
and a wrong guess says only that the pair did not match, never which half was
wrong.

Do change them before the store holds a profile worth protecting. The profile
behind the dashboard is a list of every bank, shop, insurer and employer you have
answered a question about, and `test / test` is not a gate, it is a formality.

Enabling Cloudflare Access retires the form entirely: you get an email code on
any device and there is nothing to remember.

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
npm test                     # 48 tests, no network, no account needed

wrangler d1 create glean     # copy the database_id into wrangler.toml
npm run db:remote            # apply schema.sql

wrangler secret put OPENROUTER_API_KEY   # the same key the CLI uses
wrangler secret put GLEAN_USERNAME       # optional, "test" when unset
wrangler secret put GLEAN_PASSWORD       # optional, "test" when unset
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

## What the sign-in is protecting

The profile behind the dashboard is a list of every bank, shop, insurer and
employer you have answered a question about. It should not be one guessed URL
away from anyone, which is why the Worker serves nothing at all to a request
without a session, redirecting it to `/login` and carrying where it was going so
a bookmarked settlement page survives a signed-out visit.

The default `test / test` is a demo credential, not a gate. A deployment holding
anything real wants `GLEAN_USERNAME` and `GLEAN_PASSWORD` set to something else,
and a password generated rather than chosen:

```bash
openssl rand -base64 24
```

## Sending the model calls through a gateway

`GLEAN_OPENROUTER_BASE` moves every extraction onto a Cloudflare AI Gateway
without changing the request:

```bash
wrangler secret put GLEAN_OPENROUTER_BASE
# https://gateway.ai.cloudflare.com/v1/<account_id>/<gateway_id>/openrouter
```

Unset, the calls go straight to OpenRouter. That is the point of it being
configuration: the gateway sits in the request path, and if it has a bad minute
the way back is one command and no deploy.

The gateway's OpenRouter endpoint forwards the body and the `Authorization`
header untouched, so the model, the schema and the answer are the same. What is
gained is token counts and dollar cost per request, retries and model fallbacks
as configuration, and an optional response cache. Caching is off until enabled,
and its key is a hash of provider, endpoint, model, auth header and the whole
request body, so changing a model or a prompt cannot serve a stale extraction.
In production the cache is nearly worthless anyway, because `hashes` already
stops a page whose text has not changed from being sent at all. It earns its
place while developing the extractor against the same few dozen pages.

Logs are on by default and store request and response bodies. That is public
notice text and model output, and nothing from a profile is ever sent to a
model, so there is nothing private in them. `cf-aig-collect-log-payload: false`
keeps the counts without the bodies if that changes.

Creating the gateway needs **AI Gateway: Edit** on the API token, which the
current one does not have. The dashboard under AI > AI Gateway does it without
touching the token.

## Where a person's own data sits

Three tables in D1, each row tagged with the signed-in identity:

| table | holds |
| --- | --- |
| `profile` | answers, provinces, payout and effort thresholds, muted terms |
| `claims` | interested, filed, paid, skipped, with the dates |
| `events` | the audit trail: what was discovered, what moved, what was answered |

The preferences page at `/preferences` is the front end for all of it, and
`/export.json` hands the profile and the claims back as a file. Settlements are
deliberately not in this list. They are public facts about proceedings, they cost
money to extract, and scanning once for everybody is the whole efficiency
argument.

Nothing from a profile is ever sent to a model. The only thing an extraction
call carries is the public settlement page.

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
is the right answer when the login form is the only gate.

The `Cf-Access-Authenticated-User-Email` header is never trusted. Access sets it,
but so can anyone reaching the Worker without going through Access, and trusting
it would let a stranger read any user's profile by naming them. What is verified
is `Cf-Access-Jwt-Assertion`: an RS256 signature checked against the team's
published keys, with issuer, audience and expiry all checked. There are tests for
the forgeries, including `alg: none` and a token signed with the wrong key.

At the time of deployment the Cloudflare API reported Access as **not enabled**
on this account (`access.api.error.not_enabled` from two separate endpoints), so
the login form is doing the work. That is worth confirming in the Zero Trust
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
curl -c jar -X POST -d 'username=test&password=test' https://glean.antipodetech.com/login
curl -b jar -X POST https://glean.antipodetech.com/scan
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
cp .dev.vars.example .dev.vars    # gitignored; or skip it and sign in as test / test
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
