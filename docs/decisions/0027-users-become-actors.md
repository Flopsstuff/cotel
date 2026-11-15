# ADR 0027 — Users become actors, down to the schema

**Date:** 2026-11-15
**Status:** Accepted
**Deciders:** Flop (board)

---

## Context

cotel attributes every span to whoever owns the bearer token it arrived with.
Since ADR-0004 that owner has been called a **user**: the `users` table, the
`spans.user_id` and `daily_usage.user_id` columns, `/api/v1/users`, the Users
page.

What production actually holds is not users. On 2026-11-15 the list was one
person (`flop`), a host (`rpi5`) and fifteen Paperclip agents (Daedalus, Clio,
Wayland, ...), each with its own token since ADR-0008. "User" describes the
smallest group there and misleads about the rest: an agent is not someone who
signs in, and "Add user" reads as an access-control action when it only mints an
ingest identity.

## Decision

The entity is an **actor**: whoever sends telemetry, whether a person, an AI
agent or a host. The rename goes all the way down, so the code, the API, the
database and the UI use one word:

| Layer | Before | After |
|---|---|---|
| Table | `users` | `actors` |
| Attribution column | `spans.user_id`, `daily_usage.user_id` | `actor_id` |
| Index | `idx_spans_user_id` | `idx_spans_actor_id` |
| API | `/api/v1/users`, `?user_id=`, `users_count`, `mode=user_only` | `/api/v1/actors`, `?actor_id=`, `actors_count`, `mode=actor_only` |
| Export format | v1, column `user_id` | v2, column `actor_id` at the same offset |
| UI | Users page, `/users` | Actors page, `/actors` |

The column still holds the actor's **name**, as `user_id` did; only the name of
the column changes.

### How the schema moves (v10 → v11)

A rename cannot be written idempotently in SQL, and `schema.sql` is re-applied
whenever its hash changes (ADR-0010). Left in that file, the rename would fail on
every re-apply after the first; worse, `CREATE TABLE IF NOT EXISTS actors` and
`ADD COLUMN IF NOT EXISTS actor_id` would quietly create an empty table and an
empty column next to the populated originals. So `migrateUsersToActors` in
`internal/storage/duckdb.go` runs **before** `schema.sql`:

- it reads the catalog and renames only what still carries the old name, so a
  fresh database, a migrated one and a re-apply are all no-ops;
- it drops the secondary indexes on `spans` first, because DuckDB refuses to
  ALTER a table an index depends on, and `schema.sql` recreates them right
  after, as it already does for the v9 → v10 change (ADR-0013);
- all steps commit in one transaction;
- if both `users` and `actors` exist it refuses to start rather than guess which
  one holds the real tokens.

It is tested against a database built from a frozen copy of the v10
`schema.sql` (`internal/storage/testdata/schema_v10.sql`).

### Compatibility kept

- `/api/v1/users[/...]`, `?user_id=` and `mode=user_only|user_and_history` are
  accepted as aliases. Response bodies use the new field names only.
- `/users` and `/users/:id` in the dashboard redirect to `/actors`.
- The importer reads both v1 (`user_id`) and v2 (`actor_id`) export files.
- A snapshot taken before v11 restores as it was written (`--db-import` checks
  its own table list) and is migrated by the next open.

## Consequences

- **Breaking for API consumers that read fields:** `users_count` and `user_id`
  in responses are now `actors_count` and `actor_id`. Requests keep working.
- **Export files are format v2.** A v2 file cannot be imported by a cotel older
  than this change.
- **No rollback across v11 without a restore.** An older binary opening a
  migrated file finds no `users` table and creates an empty one, so every token
  stops authenticating. Rolling back means restoring a pre-v11 snapshot.
- The ADRs and incident write-ups written before this change keep the old names;
  they describe the schema as it was.
