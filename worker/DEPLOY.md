# Deploying glean

glean in production is a Cloudflare Worker with a D1 database. It scans on a
schedule and serves a private dashboard.

A Worker rather than a Pages project for one reason: **Pages Functions cannot be
scheduled**. The premise of glean is that it looks for this money without being
asked, and a deployment that only ran when someone visited it would give that
back. Every other Antipode property is a static Pages site; this one is not, and
that difference is deliberate.

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

Cloudflare Access (Zero Trust) is the better answer if you want it: it gates on
your email with a one-time code, needs no secret in a URL, and the free tier
covers this. It is configured in the Zero Trust dashboard rather than in code, so
it is not set up here. The shared key stands until then, and the two compose
fine if you add Access later.

## Scheduling

`[triggers] crons = ["30 12 * * *"]` in `wrangler.toml`, which is 08:30 in
Toronto. Change it there and redeploy.

Trigger a scan by hand without waiting:

```bash
wrangler dev --test-scheduled   # then: curl 'http://localhost:8788/__scheduled'
```

A Worker invocation is not unbounded, so the scan carries a wall-clock budget and
stops cleanly when it runs out. That is not a failure: everything extracted
before the cutoff is committed, and the next run resumes on the pages it did not
reach, because their hashes are still missing. A cold first scan over the whole
registry may take two or three runs to work through.

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
