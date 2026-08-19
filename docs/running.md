# Running it

glean is a Go binary and a locally served web page. There is no App Store in the
loop, no code signing, no provisioning profile and no Apple Developer
enrolment. Nothing here is waiting on a DUNS number.

```bash
make build
./bin/glean scan      # look for new settlements
./bin/glean ask       # answer the questions that unlock the most money
./bin/glean serve     # dashboard at http://127.0.0.1:8477
```

That is the whole installation.

## On a schedule

The premise of the program is that nobody goes looking for this money, so
leaving it to a person to remember to run a scan gives the whole thing back.
Two ways to make it automatic.

**Foreground**, for a machine that is already running something:

```bash
./bin/glean watch -every 12h
```

**As a launchd agent**, which survives reboots and is the one to use:

```bash
sed -i '' "s|REPLACE_WITH_PATH_TO|$PWD/..|" Scripts/com.antipodetech.glean.plist
cp Scripts/com.antipodetech.glean.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.antipodetech.glean.plist
```

It runs `Scripts/glean-scan` at 08:30 daily and logs to `/tmp/glean-scan.log`.
Remove it with `launchctl unload`.

launchd does not read a login shell's profile, so the wrapper sources
`~/.config/antipode/load-secrets.sh` to get the OpenRouter key out of the
Keychain. The first scheduled run may raise a Keychain prompt; choose Always
Allow and it will not ask again. The key is never written into the plist, the
script, or any environment that outlives the scan.

## Being told about it

A scan finds things at 08:30; the point is to hear about them. Copy
`notify.example.json` to `notify.json` in the state directory
(`~/.local/share/glean` unless `GLEAN_HOME` says otherwise) and fill in whatever
channels you want. All of them are optional and a missing file means the digest
only prints to the terminal.

| channel | reaches you |
| --- | --- |
| `desktop` | a macOS notification, if you are at the machine |
| `webhook` | a plain-text POST, which is how this gets to a phone |
| `email` | SMTP, for the record rather than the interruption |

Reminders fire when a deadline crosses thirty, fourteen, seven, three or one day
out. Thirty is the one that matters: a claim needing a receipt from 2009 is not
something you can act on the afternoon it closes.

### Getting it onto a phone

The webhook is the answer, and it needs nothing from Apple. Point it at a push
service that accepts a plain POST, subscribe on the phone, and a settlement
closing in a week arrives as a notification.

Installing someone else's push app from the App Store has nothing to do with
publishing your own, so this works today regardless of enrolment status.

### Reaching the dashboard from a phone

`serve` binds to `127.0.0.1` on purpose. The profile behind it is a list of
every bank, shop, insurer and employer you have ever answered a question about,
which is not something to put on a network without deciding to.

Deciding to looks like this, on a network you trust:

```bash
./bin/glean serve -addr 0.0.0.0:8477
```

Then reach it at `http://<your-mac's-LAN-ip>:8477` from the phone. There is no
authentication in front of it, so anything else on that network can read it too.
For anything less private than a home network, forward the port over SSH or a
tailnet instead of binding it wide.

## Where the state lives

Everything is under `~/.local/share/glean`, or `$GLEAN_HOME`:

| | |
| --- | --- |
| `events.jsonl` | the append-only log; delete it and the store rebuilds from a scan |
| `profile.json` | your answers, which never leave the machine |
| `notify.json` | delivery channels, if you made one |
| `hashes.json` | what each page said last time, so an unchanged page costs no model call; safe to delete, at the price of one expensive scan |
| `cache/` | conditional-GET state, safe to delete |

The profile is not sent to the extraction model. The only thing that goes over
the network is the public settlement page.
