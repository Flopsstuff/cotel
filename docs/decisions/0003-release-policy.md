# ADR-0003: Release Policy and Versioning

## Context

The original CI pipeline published a Docker image to GHCR on every push to `main`. This produces noise in the registry, makes it impossible to distinguish "tested release" from "work-in-progress commit", and provides no changelog or GitHub Release artifact for users to anchor on.

## Decision

Adopt **manual, tag-driven releases** with semantic versioning.

### Versioning

- SemVer `MAJOR.MINOR.PATCH`, starting at `0.1.0`.
- Git tags are prefixed: `v0.1.0`, `v0.2.0`, etc.
- CHANGELOG headings omit the `v` prefix: `[0.1.0]`, `[0.2.0]`.
- While the project is pre-1.0, minor bumps carry new features; patch bumps carry bug fixes only; major remains `0`.

### Changelog

`CHANGELOG.md` follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/):

- Maintain an `## [Unreleased]` section at the top.
- Add entries there as PRs are merged (Added / Fixed / Changed / Removed).
- The release workflow stamps `[Unreleased]` → `[version] - date` and inserts a fresh `[Unreleased]` heading automatically.

### Release trigger

Releases are created **only** by manually triggering `.github/workflows/release.yml` via the GitHub Actions "Run workflow" button on `main`. The workflow:

1. Computes the next version from the latest `v*` tag (or starts at `v0.1.0` if none exist).
2. Accepts a `bump` input (`minor` default, `patch`, `major`).
3. Updates `CHANGELOG.md` and commits it.
4. Creates an annotated git tag and pushes both the commit and the tag.
5. Builds and pushes the Docker image to GHCR with `vMAJOR.MINOR.PATCH`, `vMAJOR.MINOR`, and `latest` tags.
6. Creates a GitHub Release with the extracted changelog section as release notes.

### CI changes

`ci.yml` now runs only on pushes to `main` and pull requests. It no longer publishes Docker images — that responsibility belongs entirely to `release.yml`. The smoke test continues to validate Docker builds on every CI run.

## Options considered

| Option | Rejected because |
|---|---|
| Publish every `main` push (previous behaviour) | Registry pollution; no way to signal "this is a release". |
| Auto-release on every tag push | Requires callers to push tags manually; no CHANGELOG automation. |
| Fully automated release (semantic-release) | Requires commit message convention enforcement; too much ceremony for a solo project. |

## Consequences

- No Docker image is published until someone presses the button — deliberate gate.
- GHCR always has a `latest` tag pointing to the newest release, and versioned tags for pinning.
- CHANGELOG must be kept up-to-date by the developer; the release workflow will produce an empty section if it isn't.
- Branch protection on `main` would block the release workflow's push-back commit. If protection is added later, the workflow token must be granted bypass or a deploy key used instead.

## Decision lenses applied

- **Boring tech**: `workflow_dispatch` + `gh release create` — no third-party release bots.
- **Reversibility**: manual button is easily bypassed or extended; the tag format is conventional and portable.
- **Solo team discipline**: one workflow file handles version calculation, changelog, tagging, Docker, and GH Release — nothing to keep in sync across multiple systems.
