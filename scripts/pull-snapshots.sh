#!/usr/bin/env bash
#
# pull-snapshots.sh — mirror the production snapshot volume onto this host.
#
# The snapshots live in a Docker named volume on the deploy host, which is
# macOS: a named volume has no host-side path there (it is inside Docker
# Desktop's VM), so rsync has nothing to read and the bytes have to come out
# through a throwaway read-only container. See ADR-0024.
#
# A snapshot directory is immutable once complete — snapshot.json is written
# last and nothing else writes it — so the sync rule is a set difference and
# not a delta: pull the complete source instants that are not here, skip the
# ones that are.
#
# The mirror does NOT mirror deletions. It prunes on its own count
# (SNAPSHOT_MIRROR_KEEP, never below one) and never deletes a local snapshot
# because the source stopped having it. That is the single reason this is not
# `rsync --delete`: propagating a source deletion would faithfully reproduce
# `docker volume prune` on a stopped deploy into the copy that exists to
# survive it. Do not "simplify" it later.
#
# The landed directory is named exactly after its instant because load.sql
# holds absolute /snapshots/<instant>/... paths — that is what makes the mirror
# restorable by mounting it at /snapshots.
#
# Usage:
#   scripts/pull-snapshots.sh              # one tick: pull what is missing, prune
#   scripts/pull-snapshots.sh --status     # is the mirror current?
#   scripts/pull-snapshots.sh --dry-run    # what a tick would do; writes nothing
#   scripts/pull-snapshots.sh --status --max-lag 24h   # exit 5 if staler than that
#
# Nothing schedules this pull (ADR-0024), so the mirror's lag is the time since
# the last run and `--status` read by a person is the only signal there is.
# `--max-lag` turns that reading into an exit code, so whatever eventually runs
# on a schedule — a unit, a CI job, a person's shell — gets a verdict instead of
# a duration to interpret.
#
# Env:
#   SNAPSHOT_SSH_HOST       default robmini
#   SNAPSHOT_VOLUME         default cotel-snapshots
#   SNAPSHOT_MIRROR_DIR     default $HOME/cotel-snapshots
#   SNAPSHOT_MIRROR_KEEP    default 56 (newest complete local snapshots; min 1)
#   SNAPSHOT_HELPER_IMAGE   default debian:bookworm-slim
#   SNAPSHOT_SSH_TIMEOUT    default 30 (connect timeout and keepalive interval)
#
# Exit codes:
#   0   tick completed, including "nothing to do"
#   1   source unreachable (ssh or docker failed)
#   2   remote listing empty or malformed
#   3   a pulled snapshot failed verification (sha256 or manifest); nothing landed
#   4   mirror directory missing or unwritable
#   5   --status --max-lag only: the mirror is staler than the threshold
#   64  invalid invocation or configuration (not a tick outcome)
#
# Needs only ssh, tar, sha256sum, jq and the standard text tools (awk, sed,
# grep, find, comm, du, date). Nothing runs on the source except a read-only
# throwaway container; the production container is never touched.

set -euo pipefail

SSH_HOST="${SNAPSHOT_SSH_HOST:-robmini}"
VOLUME="${SNAPSHOT_VOLUME:-cotel-snapshots}"
MIRROR_DIR="${SNAPSHOT_MIRROR_DIR:-$HOME/cotel-snapshots}"
KEEP="${SNAPSHOT_MIRROR_KEEP:-56}"
HELPER_IMAGE="${SNAPSHOT_HELPER_IMAGE:-debian:bookworm-slim}"
SSH_TIMEOUT="${SNAPSHOT_SSH_TIMEOUT:-30}"

INSTANT_RE='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}Z$'

usage() {
    cat <<'EOF'
pull-snapshots.sh — mirror the production snapshot volume onto this host.

Usage:
  scripts/pull-snapshots.sh              one tick: pull what is missing, prune
  scripts/pull-snapshots.sh --status     is the mirror current?
  scripts/pull-snapshots.sh --dry-run    what a tick would do; writes nothing

  --max-lag <duration>   with --status: exit 5 unless the mirror's lag behind
                         the source is within <duration>. Accepts 90s, 30m,
                         24h, 7d, or bare seconds.

Env:
  SNAPSHOT_SSH_HOST       default robmini
  SNAPSHOT_VOLUME         default cotel-snapshots
  SNAPSHOT_MIRROR_DIR     default $HOME/cotel-snapshots
  SNAPSHOT_MIRROR_KEEP    default 56 (newest complete local snapshots; min 1)
  SNAPSHOT_HELPER_IMAGE   default debian:bookworm-slim
  SNAPSHOT_SSH_TIMEOUT    default 30

Exit codes:
  0   tick completed, including "nothing to do"
  1   source unreachable (ssh or docker failed)
  2   remote listing empty or malformed
  3   a pulled snapshot failed verification; nothing landed
  4   mirror directory missing or unwritable
  5   --status --max-lag only: the mirror is staler than the threshold
  64  invalid invocation or configuration
EOF
}

say() { echo "pull-snapshots: $*"; }

die() { # $1 message, $2 exit code
    echo "pull-snapshots: FAILED — $1"
    exit "$2"
}

parse_duration() { # $1 like 90s, 30m, 24h, 7d or bare seconds -> seconds on stdout
    local v="$1" n u
    n="${v%[smhd]}"
    u="${v#"$n"}"
    case "$n" in ''|*[!0-9]*) return 1 ;; esac
    case "$u" in
        ''|s) echo "$n" ;;
        m)    echo "$((n * 60))" ;;
        h)    echo "$((n * 3600))" ;;
        d)    echo "$((n * 86400))" ;;
        *)    return 1 ;;
    esac
}

MODE=""
MAX_LAG=""
want_mode() { # $1 mode name
    if [ -n "$MODE" ]; then
        die "one mode at a time; got --${MODE} and --${1}" 64
    fi
    MODE="$1"
}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --status)      want_mode status ;;
        --dry-run)     want_mode dry-run ;;
        --max-lag)
            shift
            MAX_LAG="${1:-}"
            [ -n "$MAX_LAG" ] || die "--max-lag needs a duration, e.g. --max-lag 24h" 64
            ;;
        --max-lag=*)   MAX_LAG="${1#*=}" ;;
        -h|--help)     usage; exit 0 ;;
        *)             die "unknown argument '${1}'; one of --status [--max-lag <duration>], --dry-run, --help" 64 ;;
    esac
    shift
done
MODE="${MODE:-tick}"

if [ -n "$MAX_LAG" ]; then
    if [ "$MODE" != "status" ]; then
        die "--max-lag is a verdict on --status; it does not apply to a ${MODE}" 64
    fi
    if ! MAX_LAG_SECONDS="$(parse_duration "$MAX_LAG")"; then
        die "--max-lag wants a duration like 90s, 30m, 24h or 7d, got '${MAX_LAG}'" 64
    fi
fi

if ! [ "$SSH_TIMEOUT" -gt 0 ] 2>/dev/null; then
    die "SNAPSHOT_SSH_TIMEOUT must be a positive integer, got '${SSH_TIMEOUT}'" 64
fi
if ! [ "$KEEP" -ge 0 ] 2>/dev/null; then
    die "SNAPSHOT_MIRROR_KEEP must be a non-negative integer, got '${KEEP}'" 64
fi
[ "$KEEP" -lt 1 ] && KEEP=1
if [ -z "$MIRROR_DIR" ] || [ "$MIRROR_DIR" = "/" ]; then
    die "SNAPSHOT_MIRROR_DIR must be a real directory path, got '${MIRROR_DIR}'" 64
fi
MIRROR_DIR="${MIRROR_DIR%/}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- the source -------------------------------------------------------------

ssh_remote() { # $1 remote command string
    # -n is load-bearing, not hygiene: without it ssh reads the caller's stdin.
    # The tick's loop reads its work list on fd 3 for the same reason, so either
    # guard alone is enough — both are kept because the failure they prevent is
    # a tick that pulls one snapshot, swallows the rest of the list and exits 0.
    ssh -n \
        -o BatchMode=yes \
        -o ConnectTimeout="$SSH_TIMEOUT" \
        -o ServerAliveInterval="$SSH_TIMEOUT" \
        -o ServerAliveCountMax=3 \
        "$SSH_HOST" "$1"
}

in_helper() { # $1 command for the helper container
    printf 'docker run --rm -v %s:/snapshots:ro %s %s' "$VOLUME" "$HELPER_IMAGE" "$1"
}

# The trailing `exit 0` matters: the loop's status is that of the last `[ -f ]`
# test, so a newest directory without a manifest would make a perfectly good
# listing look like a failed ssh.
remote_list_raw() {
    ssh_remote "$(in_helper "sh -c 'for d in /snapshots/*/; do [ -f \"\$d/snapshot.json\" ] && basename \"\$d\"; done; exit 0'")"
}

# Complete source instants, oldest first. Names that are not snapshot instants
# belong to whatever else shares the volume and are ignored, not rejected.
remote_instants() { # $1 output file
    local raw="$TMP/remote-raw"
    if ! remote_list_raw >"$raw" 2>"$TMP/remote-err"; then
        local err
        err="$(tr '\n' ' ' <"$TMP/remote-err" | sed 's/[[:space:]]*$//' | cut -c1-200)"
        die "source unreachable — ${SSH_HOST}:${VOLUME} listing failed${err:+ (${err})}" 1
    fi
    tr -d '\r' <"$raw" | grep -E "$INSTANT_RE" | LC_ALL=C sort >"$1" || true
    if [ ! -s "$1" ]; then
        local snippet
        snippet="$(tr '\n' ' ' <"$raw" | sed 's/[[:space:]]\{1,\}/ /g' | cut -c1-200)"
        die "remote listing empty or malformed on ${SSH_HOST}:${VOLUME}${snippet:+ — got: ${snippet}}" 2
    fi
}

# --- the mirror -------------------------------------------------------------

ensure_mirror() {
    if [ ! -d "$MIRROR_DIR" ]; then
        local parent
        parent="$(dirname "$MIRROR_DIR")"
        [ -d "$parent" ] || die "mirror directory ${MIRROR_DIR} is missing and its parent ${parent} does not exist" 4
        mkdir -m 700 "$MIRROR_DIR" 2>/dev/null || die "mirror directory ${MIRROR_DIR} is missing and cannot be created" 4
        say "created mirror directory ${MIRROR_DIR} (mode 700)"
    fi
    [ -w "$MIRROR_DIR" ] && [ -x "$MIRROR_DIR" ] || die "mirror directory ${MIRROR_DIR} is not writable" 4
}

local_instants() { # every instant-named directory, oldest first
    local d i
    for d in "$MIRROR_DIR"/*/; do
        [ -d "$d" ] || continue
        i="${d%/}"
        i="${i##*/}"
        [[ "$i" =~ $INSTANT_RE ]] || continue
        echo "$i"
    done | LC_ALL=C sort
}

local_complete() { # instant-named directories that hold a manifest, oldest first
    local i
    while read -r i; do
        if [ -n "$i" ] && [ -f "$MIRROR_DIR/$i/snapshot.json" ]; then
            echo "$i"
        fi
    done < <(local_instants)
}

# A partial from a killed run, and an instant-named directory with no manifest,
# are both unreadable as "present" and both have to go before a pull lands.
clean_leftovers() {
    local d i
    for d in "$MIRROR_DIR"/.partial-*; do
        if [ -e "$d" ]; then
            say "removing leftover partial ${d##*/}"
            rm -rf "$d"
        fi
    done
    while read -r i; do
        if [ -n "$i" ] && [ ! -f "$MIRROR_DIR/$i/snapshot.json" ]; then
            say "removing incomplete local snapshot ${i} (no snapshot.json)"
            rm -rf "${MIRROR_DIR:?}/$i"
        fi
    done < <(local_instants)
}

sha_tree() { # $1 directory — sha256 of every file, path-sorted, paths relative
    ( cd "$1" && find . -type f | LC_ALL=C sort | xargs -r sha256sum )
}

compare_sha() { # $1 source sums, $2 local sums — prints differences, 1 if any
    awk '
        function path(line) { return substr(line, index(line, "  ") + 2) }
        FNR == NR { remote[path($0)] = $1; next }
        {
            p = path($0)
            seen[p] = 1
            if (!(p in remote)) { printf "%s: not present at the source, local=%s\n", p, $1; bad = 1; next }
            if (remote[p] != $1) { printf "%s: source=%s local=%s\n", p, remote[p], $1; bad = 1 }
        }
        END {
            for (p in remote) if (!(p in seen)) { printf "%s: missing from the pulled copy, source=%s\n", p, remote[p]; bad = 1 }
            exit bad ? 1 : 0
        }
    ' "$1" "$2"
}

pull_one() { # $1 instant
    local i="$1" partial="$MIRROR_DIR/.partial-$1" rcs out manifest_instant
    rm -rf "$partial"
    mkdir -m 700 "$partial"

    set +e
    ssh_remote "$(in_helper "tar -cf - -C /snapshots '$i'")" \
        | tar -xf - -C "$partial" --strip-components=1
    rcs="${PIPESTATUS[*]}"
    set -e
    case "$rcs" in
        "0 0") ;;
        0\ *)  rm -rf "$partial"; die "extracting ${i} failed (tar exit ${rcs#* }); nothing landed" 3 ;;
        *)     rm -rf "$partial"; die "source unreachable while streaming ${i} (ssh exit ${rcs%% *})" 1 ;;
    esac

    if ! ssh_remote "$(in_helper "sh -c 'cd /snapshots/$i && find . -type f | LC_ALL=C sort | xargs -r sha256sum'")" \
            >"$TMP/source-sums" 2>/dev/null; then
        rm -rf "$partial"
        die "source unreachable while hashing ${i}" 1
    fi
    if [ ! -s "$TMP/source-sums" ]; then
        rm -rf "$partial"
        die "source reported no files for ${i}; nothing landed" 3
    fi
    sha_tree "$partial" >"$TMP/local-sums"
    if ! out="$(compare_sha "$TMP/source-sums" "$TMP/local-sums")"; then
        rm -rf "$partial"
        while read -r line; do
            if [ -n "$line" ]; then say "verify ${i}: ${line}"; fi
        done <<<"$out"
        die "sha256 verification failed for ${i}; partial removed, nothing landed" 3
    fi

    if ! manifest_instant="$(jq -er '.instant' "$partial/snapshot.json" 2>/dev/null)"; then
        rm -rf "$partial"
        die "snapshot.json for ${i} is absent, unparseable or has no instant; nothing landed" 3
    fi
    if [ "${manifest_instant//:/-}" != "$i" ]; then
        rm -rf "$partial"
        die "snapshot.json claims instant ${manifest_instant} but was pulled as ${i}; nothing landed" 3
    fi

    if [ -e "$MIRROR_DIR/$i" ]; then
        rm -rf "$partial"
        die "${MIRROR_DIR}/${i} appeared while it was being pulled; nothing landed" 3
    fi
    mv "$partial" "$MIRROR_DIR/$i"
    say "pulled ${i} ($(wc -l <"$TMP/source-sums" | tr -d ' ') files verified)"
}

prune_local() {
    local complete=() i n excess k
    while read -r i; do
        if [ -n "$i" ]; then complete+=("$i"); fi
    done < <(local_complete)
    n="${#complete[@]}"
    if [ "$n" -le "$KEEP" ]; then return 0; fi
    excess=$((n - KEEP))
    for ((k = 0; k < excess; k++)); do
        say "pruning ${complete[k]} (keeping the ${KEEP} newest)"
        rm -rf "${MIRROR_DIR:?}/${complete[k]}"
    done
}

# --- reporting --------------------------------------------------------------

instant_epoch() { # $1 instant directory name
    local d="${1%%T*}" t="${1#*T}"
    t="${t%Z}"
    date -u -d "${d}T${t//-/:}Z" +%s 2>/dev/null || echo 0
}

fmt_age() { # $1 seconds
    local s="$1"
    if [ "$s" -lt 0 ]; then s=0; fi
    if [ "$s" -lt 60 ]; then
        printf '%ds' "$s"
    elif [ "$s" -lt 3600 ]; then
        printf '%dm%ds' "$((s / 60))" "$((s % 60))"
    elif [ "$s" -lt 86400 ]; then
        printf '%dh%dm' "$((s / 3600))" "$(((s % 3600) / 60))"
    else
        printf '%dd%dh' "$((s / 86400))" "$(((s % 86400) / 3600))"
    fi
}

newest_of() { # $1 file of instants, oldest first
    tail -n 1 "$1"
}

count_of() { grep -c . <"$1" || true; }

# --- modes ------------------------------------------------------------------

do_status() {
    local remote="$TMP/remote" local_list="$TMP/local" now bytes
    now="$(date -u +%s)"

    if [ ! -d "$MIRROR_DIR" ]; then
        say "status"
        echo "  mirror       ${MIRROR_DIR} — ABSENT, nothing has ever been pulled here"
        remote_instants "$remote"
        local rn re
        rn="$(newest_of "$remote")"
        re="$(instant_epoch "$rn")"
        echo "  source       ${SSH_HOST}:${VOLUME} — newest ${rn} (age $(fmt_age $((now - re)))), $(count_of "$remote") complete"
        echo "  lag          unknown — there is no mirror"
        die "mirror directory ${MIRROR_DIR} is missing" 4
    fi

    local_complete >"$local_list"
    remote_instants "$remote"
    bytes="$(du -sh "$MIRROR_DIR" 2>/dev/null | cut -f1)"

    local ln le rn re lagline lag_seconds=-1
    ln="$(newest_of "$local_list")"
    rn="$(newest_of "$remote")"
    re="$(instant_epoch "$rn")"

    say "status"
    echo "  mirror       ${MIRROR_DIR} — $(count_of "$local_list") complete snapshots, ${bytes:-?} on disk, keep ${KEEP}"
    if [ -n "$ln" ]; then
        le="$(instant_epoch "$ln")"
        echo "  local newest ${ln} (age $(fmt_age $((now - le))))"
    else
        echo "  local newest none — the mirror holds no complete snapshot"
    fi
    echo "  source       ${SSH_HOST}:${VOLUME} — newest ${rn} (age $(fmt_age $((now - re)))), $(count_of "$remote") complete"
    if [ -z "$ln" ]; then
        lagline="unknown — the mirror is empty"
    elif [ "$ln" = "$rn" ]; then
        lag_seconds=0
        lagline="0s — the mirror holds the newest source snapshot"
    else
        lag_seconds=$((re - le))
        lagline="$(fmt_age "$lag_seconds") behind the source"
    fi
    echo "  lag          ${lagline}"

    local missing
    missing="$(LC_ALL=C comm -23 "$remote" "$local_list" | grep -c . || true)"
    echo "  to pull      ${missing} source snapshot(s) not here yet"

    if [ -n "$MAX_LAG" ]; then
        echo "  threshold    ${MAX_LAG} — lag within this is a pass"
        if [ "$lag_seconds" -lt 0 ]; then
            die "the mirror holds no complete snapshot, so it is not within ${MAX_LAG} of the source" 5
        fi
        if [ "$lag_seconds" -gt "$MAX_LAG_SECONDS" ]; then
            die "the mirror is $(fmt_age "$lag_seconds") behind the source, over the ${MAX_LAG} threshold; run a tick" 5
        fi
        echo "  verdict      fresh — within ${MAX_LAG} of the source"
    fi
}

do_dry_run() {
    local remote="$TMP/remote" local_list="$TMP/local" pull="$TMP/pull" d i n
    say "dry run — nothing will be written"
    if [ ! -d "$MIRROR_DIR" ]; then
        echo "  mirror       ${MIRROR_DIR} — absent, would be created (mode 700)"
        : >"$local_list"
    else
        local_complete >"$local_list"
        echo "  mirror       ${MIRROR_DIR} — $(count_of "$local_list") complete snapshots, keep ${KEEP}"
        for d in "$MIRROR_DIR"/.partial-*; do
            if [ -e "$d" ]; then echo "  would clean  leftover partial ${d##*/}"; fi
        done
        while read -r i; do
            if [ -n "$i" ] && [ ! -f "$MIRROR_DIR/$i/snapshot.json" ]; then
                echo "  would clean  incomplete local snapshot ${i} (no snapshot.json)"
            fi
        done < <(local_instants)
    fi

    remote_instants "$remote"
    LC_ALL=C comm -23 "$remote" "$local_list" >"$pull" || true
    if [ -s "$pull" ]; then
        while read -r i; do
            [ -n "$i" ] && echo "  would pull   ${i}"
        done <"$pull"
    else
        echo "  would pull   nothing — every complete source snapshot is already here"
    fi

    n=$(( $(count_of "$local_list") + $(count_of "$pull") ))
    if [ "$n" -gt "$KEEP" ]; then
        # Oldest first, and only ever from the local set: a source snapshot is
        # pulled before it can be pruned, so the arithmetic here is the same
        # one a real tick does after pulling.
        local over=$((n - KEEP)) k=0
        while read -r i; do
            [ -n "$i" ] || continue
            [ "$k" -ge "$over" ] && break
            echo "  would prune  ${i}"
            k=$((k + 1))
        done < <(cat "$local_list" "$pull" | LC_ALL=C sort)
    else
        echo "  would prune  nothing — ${n} complete snapshots against keep ${KEEP}"
    fi
    echo "  would delete nothing because the source stopped having it (the mirror does not mirror deletions)"
}

do_tick() {
    local remote="$TMP/remote" local_list="$TMP/local" pull="$TMP/pull" i pulled=0 skipped=0
    ensure_mirror
    clean_leftovers
    remote_instants "$remote"
    local_complete >"$local_list"
    LC_ALL=C comm -23 "$remote" "$local_list" >"$pull" || true
    skipped="$(LC_ALL=C comm -12 "$remote" "$local_list" | grep -c . || true)"

    while read -r -u 3 i; do
        if [ -n "$i" ]; then
            pull_one "$i"
            pulled=$((pulled + 1))
        fi
    done 3<"$pull"

    prune_local

    local_complete >"$local_list"
    local newest
    newest="$(newest_of "$local_list")"
    say "OK — pulled ${pulled}, already here ${skipped}; mirror holds $(count_of "$local_list") complete snapshots${newest:+, newest ${newest}} in ${MIRROR_DIR}"
}

case "$MODE" in
    status)  do_status ;;
    dry-run) do_dry_run ;;
    tick)    do_tick ;;
esac
