# Re-taking the README screenshots

The images in `docs/assets/dashboard-*.png` are produced from a throwaway instance
seeded with synthetic telemetry, never from a real one. Real telemetry carries real
user names and real spend, and a screenshot is forever.

Redo them whenever a page in the shot changes shape.

## 1. Build the image under test

```bash
docker build -t cotel:shots .
```

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

## 3. Seed it

```bash
python3 scripts/seed-demo.py --dash-url http://localhost:18080 \
                             --ingest-url http://localhost:14318
```

Takes a couple of minutes and ingests ~25 000 spans across ~485 sessions. The RNG
seed is fixed, so a re-run against a fresh volume reproduces the same numbers.
Ingest is queued behind the HTTP response — wait for `span_count` in
`curl -s localhost:18080/api/v1/health` to stop climbing before shooting.

## 4. Shoot

```bash
BASE=http://localhost:18080 node scripts/shoot-screenshots.mjs
```

1440×1400 viewport at 2× DPR, dark scheme, each page cropped at the bottom edge of
a named element. The files land straight in `docs/assets/`.

`playwright-core` has to be resolvable from the script — `npx playwright-core@latest
--help` once is enough to populate the npx cache, then symlink it into a
`node_modules/` beside the script. It has to be a symlink: the script is an ES
module and ESM resolution ignores `NODE_PATH`. Chromium comes from `CHROMIUM`
(default `/usr/bin/chromium`); this box has no Playwright-managed browser.

## 5. Tear down

```bash
docker rm -f cotel-shots && docker volume rm cotel-shots-data
```

## What not to fake

The seeder sends only the attributes Claude Code actually sends. In particular it
does not attach `command` to `Bash` spans, so the Tools page's Bash breakdown shows
its "no command detail in this data" state — which is what a real install sees.
Seeding it would make the README advertise a view nobody gets.
