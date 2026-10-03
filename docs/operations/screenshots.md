# Re-taking the README screenshots

The images in `docs/assets/dashboard-*.png` are produced from a throwaway instance
seeded with synthetic telemetry, never from a real one. Real telemetry carries real
user names and real spend, and a screenshot is forever.

Redo them whenever a page in the shot changes shape.

## 1. Build the image under test

```bash
docker build -t cotel:shots \
  --label org.opencontainers.image.revision="$(git rev-parse HEAD)" .
```

The label is what step 3 checks the image against; without it there is nothing to
compare and you are back to trusting your memory of when you built.

## 2. Run it on a fresh volume, with retention off

The seed reaches 90 days back; the shipped 30-day raw retention would roll most of
it into `daily_usage` mid-run and the session rows would vanish from the list.

```bash
docker run -d --name cotel-shots \
  -p 14318:4318 -p 18080:8080 \
  -e COTEL_RETENTION_RAW_DAYS=3650 \
  -e COTEL_RETENTION_AGGREGATE_DAYS=3650 \
  -v cotel-shots-data:/data \
  cotel:shots
```

## 3. Check the image is not stale

Do this *before* the seed. The dashboard assets are `go:embed`-ed into the binary,
so an image built before a design token moved serves the old CSS and the shot
carries the old colours silently — the container is healthy, the seed is fine, the
crop anchor matches, and nothing surfaces it until someone diffs a PNG. That is
how the Overview hero in PR #109 landed on the pre-repaint palette and had to be
re-shot in #111.

The cheap guard first — the image should have been built from the commit you intend
to publish:

```bash
docker inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' \
  cotel:shots
git rev-parse origin/main
```

Then the one that actually proves it, by diffing the palette the container serves
against the token source at that commit. Compare declarations rather than
hardcoded hex values — the tokens are expected to change, so a literal is wrong by
the next repaint:

```bash
tokens() { grep -oE -- '--color-[a-z0-9-]+: *#[0-9a-fA-F]{3,8}' | tr -d ' ' | sort -u; }
CSS=$(curl -s http://localhost:18080/ | grep -oE '/assets/[^"]+\.css' | head -1)

diff <(curl -s "http://localhost:18080$CSS" | tokens) \
     <(git show origin/main:frontend/src/styles/tokens.css | tokens) \
  && echo "palette matches origin/main"
```

Vite minifies away the whitespace but keeps the custom-property declarations
verbatim, so both sides normalise to the same ~46 lines. Both the light `:root`
block and the `prefers-color-scheme: dark` override are in there; the shots are
dark, and the dark values are the ones that drifted last time. Any output at all
means the image predates a token change — rebuild from step 1 before spending an
hour on the seed.

## 4. Seed it

```bash
python3 -u scripts/seed-demo.py --dash-url http://localhost:18080 \
                                --ingest-url http://localhost:14318
```

Budget about an hour, not a couple of minutes. A 2026-10-03 run on the Pi took
46 minutes to post 458 sessions / 22 663 spans and a further 10 minutes for the
queue to drain — 56 minutes from launch to a usable instance. Start it as a
background job and come back to it. Keep the `-u`: without it the every-10-days
progress line sits in Python's buffer and a redirected log looks dead for the
whole run.

Ingest is queued behind the HTTP response, so the seeder exiting does not mean
the data is there: wait for `span_count` in
`curl -s localhost:18080/api/v1/health` to stop climbing, and expect it to land
exactly on the span total the seeder printed.

The RNG seed is fixed, so a re-run on the same day against a fresh volume
reproduces the same numbers. Across days it does not: the per-day volume is
weighted by weekday, and the 90-day window lands on a different weekday
alignment each time, so session and span totals drift by a few percent and the
30-day stat cards move with them. The curve shape stays the same.

## 5. Shoot

```bash
BASE=http://localhost:18080 node scripts/shoot-screenshots.mjs
```

1440×1400 viewport at 2× DPR, dark scheme, each page cropped at the bottom edge of
a named element. The files land straight in `docs/assets/`.

The script always shoots all five, so it overwrites shots whose page did not
change. These are git-lfs objects and a rewrite is a new object even when the
image looks identical, so `git checkout -- docs/assets/<name>.png` the ones
outside the change and commit only the pages that actually moved.

`playwright-core` has to be resolvable from the script — `npx playwright-core@latest
--help` once is enough to populate the npx cache, then symlink it into a
`node_modules/` directory. It has to be a symlink: the script is an ES
module and ESM resolution ignores `NODE_PATH`. Put that `node_modules/` in the
repo's *parent* rather than at the repo root: ESM resolution walks up the
directory chain either way, and the repo root is not ignored there, so a root
`node_modules/` is one `git add -A` away from being committed. Chromium comes from `CHROMIUM`
(default `/usr/bin/chromium`); this box has no Playwright-managed browser.

## 6. Tear down

```bash
docker rm -f cotel-shots && docker volume rm cotel-shots-data
```

## What not to fake

The seeder sends only the attributes Claude Code actually sends. In particular it
does not attach `command` to `Bash` spans, so the Tools page's Bash breakdown shows
its "no command detail in this data" state — which is what a real install sees.
Seeding it would make the README advertise a view nobody gets.
