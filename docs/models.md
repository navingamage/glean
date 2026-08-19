# Models

glean reads class definitions through [OpenRouter](https://openrouter.ai), which
speaks the OpenAI chat completions schema and fronts most of the providers worth
using. The client is about two hundred lines in
[`internal/extract/openrouter.go`](../internal/extract/openrouter.go); the API
surface glean needs is one POST, and carrying a vendor SDK for that would be the
only dependency in the program.

## Choosing one

```bash
glean models              # usable models, cheapest first
glean models -match flash
glean models -all         # including ones glean cannot use
glean models -pages 80    # price a bigger scan
```

Prices come from OpenRouter's live catalogue rather than from anything written
down here, because a discount that was 75% off last month is not a fact worth
hardcoding. The columns are priced against a real workload: 6,000 input tokens
and 900 output tokens, which is what a Canadian settlement notice actually costs
to read once the markup is stripped.

The headline result is that it does not matter much. A full first scan of the
whole registry lands between two and twenty cents depending on the model, and a
steady-state daily run costs a fraction of that, because only pages whose content
hash changed since yesterday are extracted at all.

So choose on accuracy. The failure that costs money here is a model that reads
"purchased bread at Loblaws, No Frills or Zehrs" as three separate requirements
instead of three alternatives, because that produces a settlement you are quietly
told you do not qualify for. Saving a cent per scan does not pay for one of
those.

A `:batch` suffix is half price because the provider may queue the request rather
than answer it. A scan waiting on it feels that as a stall. Worth trying before
committing a schedule to it.

## Requirements

`glean models` hides anything that fails either test:

**Strict JSON schema.** The extractor sends `response_format` with
`type: json_schema` and `strict: true`. Without it there is no contract with the
model and the record comes back as prose to be guessed at. Models advertising
only `response_format` (plain JSON mode) are excluded, and shown by `-all` with a
note.

**At least 32k of context.** A notice page is sent up to 60,000 characters.
Truncating it removes the class definition, which is the only thing being
extracted.

## Configuration

| | |
| --- | --- |
| `OPENROUTER_API_KEY` | Required for class definitions. Without it glean still finds settlements, stages, deadlines and claim portals; it just cannot say whether you are in the class. |
| `GLEAN_MODEL` | Default model, overridden by `-model`. |
| `OPENROUTER_BASE_URL` | Endpoint override, for a proxy or a test double. |

The key belongs in the macOS Keychain rather than in a dotfile:

```bash
security add-generic-password -U -a "$USER" -s antipode-openrouter-api-key -w
source ~/.config/antipode/load-secrets.sh
```

Omitting the value after `-w` makes the tool prompt for it, so the key never
lands in shell history.

## Cost reporting

Every scan that used a model prints what it spent, taken from the `usage.cost`
OpenRouter returns rather than estimated:

```
91 candidates, 91 fetched, 38 unchanged, 42 not relevant, 11 extracted, ...
11 model call(s), 64,200 in / 9,900 out tokens, $0.0212 (google/gemini-3.7-flash)
```

This is not decoration. A scan that quietly costs four dollars a day is a scan
someone turns off within a month. One that prints two cents is one they leave
running, which is the only way the program is worth anything.

## Retries

A scan touches a few dozen pages in a burst, so rate limiting is expected rather
than exceptional: 429 and 5xx are backed off and retried four times, and so is an
error reported inside a 200 body, which is how OpenRouter surfaces some upstream
provider failures. Anything else in the 4xx range is a bug in the request, and
retrying it only spends money more slowly, so it is reported immediately.

A model failure never drops a settlement. The scan falls back to the heuristic
extractor for that page, so an outage costs the class definition rather than the
deadline.
