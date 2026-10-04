# ADR 0018 — DuckDB driver: `duckdb/duckdb-go/v2`, with the storage format pinned

**Date:** 2026-10-04
**Status:** Accepted
**Deciders:** Daedalus (CTO)

---

## Context

cotel reached `github.com/marcboeker/go-duckdb v1.8.3`, which carries DuckDB
engine **1.1.3**. The current engine is 1.5.6. Four minors behind is not by
itself a reason to move — [ADR-0001](./0001-storage) picked DuckDB for being
boring, and an embedded storage engine is the last place to chase versions.

Two things made it a decision rather than a chore:

1. **The module is deprecated by its own author.** The first line of
   `marcboeker/go-duckdb`'s `go.mod` (v2.4.3) reads
   `// Deprecated: This module has moved to github.com/duckdb/duckdb-go`, and its
   last tag is from 2025-10-15. The DuckDB project took the driver over. We were
   not four minors behind a maintained dependency; we were pinned to an address
   that is no longer the dependency.
2. **The file format was believed to be a one-way door.** DuckDB's on-disk
   format is versioned, and a newer engine writing a file an older engine cannot
   read would mean the upgrade could not be rolled back — with production's only
   copy of the data in the file. That belief is what had to be measured before
   anything else, and it turned out to be false in the way that matters.

The upgrade is explicitly **not** a repair for the ART-metadata corruption that
took production down. Upstream [duckdb#25360](https://github.com/duckdb/duckdb/issues/25360)
reports the same class of fault still reproducing on 1.5.5; what the newer engine
changes is that the failure surfaces as a Go error instead of an `abort()` inside
cgo.

---

## Options considered

| Option | Maintained | Engine | Verdict |
|---|---|---|---|
| Stay on `marcboeker/go-duckdb v1.8.3` | No — deprecated, last tag 2025-10 | 1.1.3 | Rejected: the dependency has no upstream to receive a bug report |
| `marcboeker/go-duckdb/v2` (up to v2.4.3) | No — same deprecation notice | 1.1.3-era | Rejected: a module move with none of the benefit |
| **`github.com/duckdb/duckdb-go/v2 v2.10506.0`** | Yes — the DuckDB project | **1.5.6** | **Chosen** |
| Replace DuckDB | — | — | Out of scope; ADR-0001 stands |

The target's tag numbering encodes the engine: `2.1` + `05` + `06` → DuckDB
1.5.6. Verified from the artifact rather than the README — `duckdb.h` in
`lib/linux-arm64@v0.10506.0` declares API version 1.5.6 with source id
`069cc9f9b5`, the same source id the official 1.5.6 CLI prints.

---

## The storage format is not a one-way door — measured

DuckDB writes a format number into the file header (`DUCK` magic at byte 8, the
number as a little-endian `uint64` at byte 12). The engine's DSN parameter
`storage_compatibility_version` names the *oldest release that must be able to
read the result*, and maps to that number:

| `storage_compatibility_version` | `storage_version` in the header | DuckDB 1.1.3 opens it? |
|---|---|---|
| **`v0.10.2` — today's default on both 1.1.3 and 1.5.6** | **64** | **yes** |
| `v1.1.3` | 64 | yes |
| `v1.2.0` | 65 | no |
| `v1.3.0` | 66 | no |
| `v1.4.0` | 67 | no |
| `v1.5.0` | 68 | no |
| `v1.5.6` | 68 | no |

A refusal reads:

```
IO Error: Trying to read a database file with version number 68, but we can only
read version 64.
```

The decisive measurement is what happens to an **existing** file. On a 21 MB /
200 000-row database created by 1.1.3 with cotel's real `schema.sql` (so: a
primary ART index on `span_id` plus four secondary ART indexes):

| What was done | Result |
|---|---|
| 1.5.6 opens it read-only and queries | 200 000 rows; file `sha256` unchanged |
| 1.5.6 opens read-write, inserts, `CHECKPOINT` | header still **64** |
| 1.1.3 reopens that file | reads it, 201 000 rows |
| 1.1.3 writes into a file 1.5.6 created | ok |
| 1.1.3 leaves an unreplayed WAL, 1.5.6 replays it (the production case) | ok |
| 1.5.6 opens an existing 64 file and *explicitly asks* for `v1.5.6` | **no-op** — file stays 64, 1.1.3 still reads it |
| 1.5.6 creates a **new** file with compat ≥ `v1.2.0` | only here does the door close |

So conversion of an existing file does not happen through ordinary
open-and-write, not even on explicit request. The door is one-way only for a
*newly created* file, and only on explicit opt-in. The rollback was confirmed
with the artifact a rollback would actually use: after 1.5.6 wrote to the file,
the then-current production image `ghcr.io/flopsstuff/cotel:latest` (DuckDB
1.1.3) opened it and served `/healthz` with the full span count.

---

## Decision

Move to `github.com/duckdb/duckdb-go/v2 v2.10506.0`, and **pin the storage
format rather than inherit it**.

`storage.Open` passes `storage_compatibility_version=v0.10.2` in the DSN, and
`TestNewDatabaseIsWrittenAtPinnedStorageVersion` reads the number out of the file
header and holds it at 64. The pin is a no-op against today's engine — that is
the point. A default is behaviour, not a contract; unpinned, a future DuckDB
minor could raise it and make every newly created file unopenable by the previous
image, with no diff of ours to point at. The test reads the header rather than
asking the engine, because the question is what a *different, older* engine would
find there.

Raising the pin is a one-way door for new files and needs its own ADR.

---

## Consequences

- **Go 1.24.0 is the floor.** The driver requires it, so `go.mod`, the builder
  stage in `Dockerfile` and the CI `go-version` all move together. Building cotel
  on Go 1.23 no longer works.
- **The image grows 213 MB → 294 MB.** The static engine library is bigger. This
  is the whole cost of the upgrade.
- **ICU is linked statically.** Engine 1.1.3 fetched the ICU extension over the
  network at startup; 1.5.6 bundles it, removing a network dependency from the
  cold-start path.
- **A `JSON` column no longer scans into `*string`.** v2 hands back a decoded
  `map[string]interface{}`. `ExportSpans` and the session-detail query cast in
  SQL (`attributes::VARCHAR`) instead, which does not depend on the driver
  version at all. Any new query selecting `attributes` or `resource_attrs` into a
  Go string must carry the same cast.
- **A rollback to the pre-upgrade image stays possible** for as long as the pin
  holds and no file is created at 65 or above. Note the asymmetry with
  [ADR-0010](./0010-schema-version-guard): the *storage format* is now
  rollback-safe, but a schema migration still is not — an older binary opens a
  newer-schema database and then errors on the columns it expects.
- **The corruption is not addressed.** Treating this upgrade as the fix would be
  wrong; see the upstream issue above.
- `go mod tidy` carried transitive bumps along (`arrow-go` 18.0.0 → 18.5.1,
  `grpc` 1.67.1 → 1.78.0, `protobuf` 1.36.3 → 1.36.11, `mitchellh/mapstructure`
  → `go-viper/mapstructure/v2`). They are a consequence of the driver's own
  requirements, not part of this decision.
