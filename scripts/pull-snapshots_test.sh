#!/usr/bin/env bash
#
# pull-snapshots_test.sh — drive pull-snapshots.sh against a fake source.
#
# A stub `ssh` on PATH serves a directory tree as if it were the snapshot
# volume, so nothing here needs network, docker or the deploy host. The stub
# can also stream bytes from a tampered copy while hashing the pristine one,
# which is how the verification path is exercised.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PULL="$ROOT/scripts/pull-snapshots.sh"
PASS=0
FAIL=0

fail() {
    echo "FAIL: $*"
    FAIL=$((FAIL + 1))
}

pass() {
    echo "PASS: $*"
    PASS=$((PASS + 1))
}

WORK="$(mktemp -d)"
BIN="$WORK/bin"
mkdir -p "$BIN"
trap 'rm -rf "$WORK"' EXIT

cat >"$BIN/ssh" <<'STUB'
#!/usr/bin/env bash
# Fake ssh: the remote command is the last argument. FAKE_REMOTE is the volume,
# FAKE_TAR_SRC (default FAKE_REMOTE) is what the tar stream reads from.
set -uo pipefail
cmd="${!#}"

# Real ssh consumes its caller's stdin unless -n is given, which is how a
# multi-instant tick can lose the rest of its work list. Emulate that: a
# bounded read so an idle stdin cannot hang the suite.
nflag=0
for a in "$@"; do
    if [ "$a" = "-n" ]; then nflag=1; fi
done
if [ "$nflag" = "0" ]; then read -t 0.2 -r _swallowed || true; fi
if [ "${FAKE_SSH_FAIL:-0}" = "1" ]; then
    echo "ssh: connect to host ${FAKE_SSH_HOST:-fake} port 22: Connection refused" >&2
    exit 255
fi
src="${FAKE_TAR_SRC:-$FAKE_REMOTE}"
case "$cmd" in
    *"tar -cf -"*)
        instant="$(printf '%s' "$cmd" | sed -n "s|.*-C /snapshots '\([^']*\)'.*|\1|p")"
        exec tar -cf - -C "$src" "$instant"
        ;;
    *sha256sum*)
        instant="$(printf '%s' "$cmd" | sed -n "s|.*cd /snapshots/\([^ ]*\) &&.*|\1|p")"
        cd "$FAKE_REMOTE/$instant" || exit 1
        find . -type f | LC_ALL=C sort | xargs -r sha256sum
        ;;
    *snapshot.json*)
        if [ "${FAKE_LIST_GARBAGE:-0}" = "1" ]; then
            echo "Unable to find image 'debian:bookworm-slim' locally"
            exit 0
        fi
        for d in "$FAKE_REMOTE"/*/; do
            if [ -f "$d/snapshot.json" ]; then basename "$d"; fi
        done
        exit 0
        ;;
    *)
        echo "fake ssh: unexpected remote command: $cmd" >&2
        exit 97
        ;;
esac
STUB
chmod +x "$BIN/ssh"
export PATH="$BIN:$PATH"

instant_rfc() { # 2026-10-06T12-00-00Z -> 2026-10-06T12:00:00Z
    local d="${1%%T*}" t="${1#*T}"
    t="${t%Z}"
    printf '%sT%sZ' "$d" "${t//-/:}"
}

make_snapshot() { # $1 root, $2 instant, $3 optional "incomplete"
    local d="$1/$2"
    mkdir -p "$d"
    printf 'spans parquet bytes for %s\n' "$2" >"$d/spans.parquet"
    printf 'users parquet bytes for %s\n' "$2" >"$d/users.parquet"
    printf 'CREATE TABLE spans(x INTEGER);\n' >"$d/schema.sql"
    printf "COPY spans FROM '/snapshots/%s/spans.parquet' (FORMAT PARQUET);\n" "$2" >"$d/load.sql"
    if [ "${3:-}" = "incomplete" ]; then
        return 0
    fi
    printf '{"instant":"%s","duration_ms":120,"format":"parquet-zstd","schema_version":10,"tables":{"spans":1,"users":1},"directory":"/snapshots/%s"}\n' \
        "$(instant_rfc "$2")" "$2" >"$d/snapshot.json"
}

# A case gets its own source tree and its own mirror.
new_case() { # $1 name
    CASE="$WORK/$1"
    FAKE_REMOTE="$CASE/remote"
    MIRROR="$CASE/mirror"
    mkdir -p "$FAKE_REMOTE" "$MIRROR"
    export FAKE_REMOTE
    unset FAKE_TAR_SRC FAKE_SSH_FAIL FAKE_LIST_GARBAGE || true
}

run_pull() { # remaining args passed to the script
    SNAPSHOT_SSH_HOST=fake \
    SNAPSHOT_VOLUME=fake-snapshots \
    SNAPSHOT_MIRROR_DIR="$MIRROR" \
    SNAPSHOT_MIRROR_KEEP="${KEEP:-56}" \
        "$PULL" "$@" 2>&1
}

# expect NAME WANT_RC [args...]
expect() {
    local name="$1" want_rc="$2"
    shift 2
    set +e
    OUT="$(run_pull "$@")"
    RC=$?
    set -e
    if [ "$RC" -ne "$want_rc" ]; then
        fail "$name — exit $RC, want $want_rc; output: $OUT"
        return 1
    fi
    pass "$name (exit $RC)"
    return 0
}

mirror_fingerprint() { # every path under the mirror plus the hash of every file
    ( cd "$MIRROR" && find . | LC_ALL=C sort && find . -type f | LC_ALL=C sort | xargs -r sha256sum )
}

assert_landed() { # $1 instant — present, complete, byte-identical to the source
    local i="$1"
    if [ ! -f "$MIRROR/$i/snapshot.json" ]; then
        fail "landed $i — no snapshot.json in the mirror"
        return
    fi
    local a b
    a="$( cd "$FAKE_REMOTE/$i" && find . -type f | LC_ALL=C sort | xargs -r sha256sum )"
    b="$( cd "$MIRROR/$i" && find . -type f | LC_ALL=C sort | xargs -r sha256sum )"
    if [ "$a" = "$b" ]; then
        pass "landed $i — $(printf '%s' "$b" | grep -c .) files sha256-identical to the source"
    else
        fail "landed $i — content differs from the source"
    fi
}

assert_absent() { # $1 path relative to the mirror
    if [ -e "$MIRROR/$1" ]; then fail "absent $1 — still in the mirror"; else pass "absent $1"; fi
}

assert_present() { # $1 path relative to the mirror
    if [ -e "$MIRROR/$1" ]; then pass "present $1"; else fail "present $1 — gone from the mirror"; fi
}

assert_no_partials() {
    local leftovers
    leftovers="$(find "$MIRROR" -maxdepth 1 -name '.partial-*' | wc -l | tr -d ' ')"
    if [ "$leftovers" = "0" ]; then pass "no leftover partials"; else fail "no leftover partials — found $leftovers"; fi
}

assert_contains() { # $1 label, $2 needle (searched in $OUT)
    case "$OUT" in
        *"$2"*) pass "$1" ;;
        *) fail "$1 — output did not contain '$2': $OUT" ;;
    esac
}

echo "== nothing new: a tick over a current mirror writes nothing"
new_case nothing-new
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$FAKE_REMOTE" 2026-10-10T06-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
make_snapshot "$MIRROR" 2026-10-10T06-00-00Z
BEFORE="$(mirror_fingerprint)"
expect "nothing new is exit 0" 0
assert_contains "nothing new says it pulled nothing" "pulled 0, already here 2"
if [ "$BEFORE" = "$(mirror_fingerprint)" ]; then
    pass "nothing new wrote nothing"
else
    fail "nothing new wrote nothing — the mirror changed"
fi

echo
echo "== one new instant: pulled, verified, landed under its own name"
new_case one-new
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$FAKE_REMOTE" 2026-10-10T06-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
expect "one new instant is exit 0" 0
assert_contains "one new instant is reported" "pulled 2026-10-10T06-00-00Z"
assert_landed 2026-10-10T06-00-00Z
assert_no_partials
if grep -q "/snapshots/2026-10-10T06-00-00Z/spans.parquet" "$MIRROR/2026-10-10T06-00-00Z/load.sql"; then
    pass "load.sql keeps its absolute /snapshots path, so the landed name must be the instant"
else
    fail "load.sql no longer names the absolute snapshot path"
fi

echo
echo "== several new instants in one tick: every one of them lands"
new_case many-new
for h in 00 06 12 18; do make_snapshot "$FAKE_REMOTE" "2026-10-10T${h}-00-00Z"; done
expect "four new instants in one tick is exit 0" 0
assert_contains "the tick counts all four" "pulled 4, already here 0"
for h in 00 06 12 18; do assert_landed "2026-10-10T${h}-00-00Z"; done
assert_no_partials

echo
echo "== a source directory without snapshot.json is never pulled"
new_case no-manifest
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$FAKE_REMOTE" 2026-10-10T06-00-00Z incomplete
expect "half-written source snapshot is skipped, exit 0" 0
assert_landed 2026-10-10T00-00-00Z
assert_absent 2026-10-10T06-00-00Z
assert_no_partials

echo
echo "== sha mismatch: exit 3, partial removed, nothing landed"
new_case sha-mismatch
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
# The stream comes from a tampered copy; the hashes come from the real volume.
FAKE_TAR_SRC="$CASE/tampered"
mkdir -p "$FAKE_TAR_SRC"
cp -r "$FAKE_REMOTE/2026-10-10T00-00-00Z" "$FAKE_TAR_SRC/"
printf 'corrupted on the wire\n' >"$FAKE_TAR_SRC/2026-10-10T00-00-00Z/spans.parquet"
export FAKE_TAR_SRC
expect "corrupted transfer is exit 3" 3
assert_contains "the mismatching file is named" "./spans.parquet: source="
assert_contains "verification failure is explicit" "sha256 verification failed"
assert_absent 2026-10-10T00-00-00Z
assert_no_partials
unset FAKE_TAR_SRC

echo
echo "== ssh failure: exit 1, mirror untouched"
new_case ssh-down
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$MIRROR" 2026-10-09T18-00-00Z
BEFORE="$(mirror_fingerprint)"
export FAKE_SSH_FAIL=1
expect "unreachable source is exit 1" 1
assert_contains "unreachable is named as such" "source unreachable"
unset FAKE_SSH_FAIL
if [ "$BEFORE" = "$(mirror_fingerprint)" ]; then
    pass "unreachable source left the mirror untouched"
else
    fail "unreachable source left the mirror untouched — it changed"
fi

echo
echo "== a listing that is not snapshots at all: exit 2"
new_case garbage-list
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
export FAKE_LIST_GARBAGE=1
expect "malformed remote listing is exit 2" 2
assert_contains "malformed listing quotes what it got" "remote listing empty or malformed"
unset FAKE_LIST_GARBAGE

echo
echo "== an empty source volume: exit 2"
new_case empty-list
expect "empty remote listing is exit 2" 2

echo
echo "== the source list shrinks: local copies stay (the mirror does not mirror deletions)"
new_case source-shrinks
make_snapshot "$FAKE_REMOTE" 2026-10-10T18-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
make_snapshot "$MIRROR" 2026-10-10T06-00-00Z
make_snapshot "$MIRROR" 2026-10-10T12-00-00Z
expect "a source that lost snapshots is still exit 0" 0
assert_present 2026-10-10T00-00-00Z
assert_present 2026-10-10T06-00-00Z
assert_present 2026-10-10T12-00-00Z
assert_landed 2026-10-10T18-00-00Z

echo
echo "== pruning keeps the newest KEEP and never drops the last one"
new_case prune
make_snapshot "$FAKE_REMOTE" 2026-10-10T18-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
make_snapshot "$MIRROR" 2026-10-10T06-00-00Z
make_snapshot "$MIRROR" 2026-10-10T12-00-00Z
KEEP=2 expect "prune to 2 is exit 0" 0
assert_absent 2026-10-10T00-00-00Z
assert_absent 2026-10-10T06-00-00Z
assert_present 2026-10-10T12-00-00Z
assert_present 2026-10-10T18-00-00Z

new_case prune-floor
make_snapshot "$FAKE_REMOTE" 2026-10-10T06-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
KEEP=0 expect "keep 0 is clamped to 1, not to zero snapshots" 0
assert_absent 2026-10-10T00-00-00Z
assert_present 2026-10-10T06-00-00Z
if [ "$(find "$MIRROR" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')" = "1" ]; then
    pass "keep 0 left exactly one snapshot standing"
else
    fail "keep 0 did not leave exactly one snapshot: $(find "$MIRROR" -mindepth 1 -maxdepth 1 -type d)"
fi

echo
echo "== an interrupted previous run: its partial is cleaned up and never read as present"
new_case interrupted
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$FAKE_REMOTE" 2026-10-10T06-00-00Z
# A partial from a killed run, and an instant-named directory that never got
# its manifest: both have to be removed and both instants re-pulled.
mkdir -p "$MIRROR/.partial-2026-10-10T06-00-00Z"
printf 'half a file\n' >"$MIRROR/.partial-2026-10-10T06-00-00Z/spans.parquet"
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z incomplete
expect "a tick after an interruption is exit 0" 0
assert_contains "the leftover partial is reported" "removing leftover partial .partial-2026-10-10T06-00-00Z"
assert_contains "the manifest-less local copy is reported" "removing incomplete local snapshot 2026-10-10T00-00-00Z"
assert_no_partials
assert_landed 2026-10-10T00-00-00Z
assert_landed 2026-10-10T06-00-00Z

echo
echo "== an unwritable mirror: exit 4"
new_case unwritable
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
chmod 500 "$MIRROR"
expect "unwritable mirror is exit 4" 4
assert_contains "unwritable mirror says so" "is not writable"
chmod 700 "$MIRROR"

echo
echo "== a mirror whose parent does not exist: exit 4, nothing created"
new_case missing-parent
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
MIRROR="$CASE/no/such/parent/mirror"
expect "mirror under a missing parent is exit 4" 4
assert_contains "the missing parent is named" "does not exist"
if [ -e "$CASE/no" ]; then fail "nothing created — $CASE/no exists"; else pass "nothing created under the missing parent"; fi

echo
echo "== a missing mirror is created on the first tick"
new_case first-tick
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
rmdir "$MIRROR"
expect "first tick into a missing mirror is exit 0" 0
assert_contains "mirror creation is reported" "created mirror directory"
assert_landed 2026-10-10T00-00-00Z
if [ "$(stat -c '%a' "$MIRROR")" = "700" ]; then
    pass "the mirror is created mode 700 (it carries the same plaintext tokens as the data volume)"
else
    fail "the mirror is not mode 700: $(stat -c '%a' "$MIRROR")"
fi

echo
echo "== --dry-run touches nothing"
new_case dry-run
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$FAKE_REMOTE" 2026-10-10T06-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
BEFORE="$(mirror_fingerprint)"
KEEP=1 expect "--dry-run is exit 0" 0 --dry-run
assert_contains "--dry-run names what it would pull" "would pull   2026-10-10T06-00-00Z"
assert_contains "--dry-run names what it would prune" "would prune  2026-10-10T00-00-00Z"
assert_contains "--dry-run restates the deletion rule" "does not mirror deletions"
if [ "$BEFORE" = "$(mirror_fingerprint)" ]; then
    pass "--dry-run wrote nothing"
else
    fail "--dry-run wrote nothing — the mirror changed"
fi

echo
echo "== --dry-run on a current mirror has nothing to do"
new_case dry-run-current
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
expect "--dry-run on a current mirror is exit 0" 0 --dry-run
assert_contains "--dry-run says there is nothing to pull" "would pull   nothing"
assert_contains "--dry-run says there is nothing to prune" "would prune  nothing"

echo
echo "== --status prints the block a human reads"
new_case status
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$FAKE_REMOTE" 2026-10-10T06-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
expect "--status is exit 0" 0 --status
assert_contains "--status names the newest local instant" "local newest 2026-10-10T00-00-00Z (age "
assert_contains "--status counts the mirror and its bytes" "complete snapshots,"
assert_contains "--status names the newest source instant" "newest 2026-10-10T06-00-00Z (age "
assert_contains "--status prints the lag" "lag          6h0m behind the source"
assert_contains "--status says how much is outstanding" "to pull      1 source snapshot"

new_case status-current
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
make_snapshot "$MIRROR" 2026-10-10T00-00-00Z
expect "--status on a current mirror is exit 0" 0 --status
assert_contains "--status calls a current mirror current" "0s — the mirror holds the newest source snapshot"

new_case status-no-mirror
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
rmdir "$MIRROR"
expect "--status without a mirror is exit 4" 4 --status
assert_contains "--status without a mirror says so" "ABSENT"

echo
echo "== invocation errors"
new_case usage
make_snapshot "$FAKE_REMOTE" 2026-10-10T00-00-00Z
expect "an unknown flag is exit 64" 64 --mirror-deletions
expect "--help is exit 0" 0 --help
assert_contains "--help lists the exit codes" "4   mirror directory missing or unwritable"
KEEP=not-a-number expect "a non-numeric keep is exit 64" 64

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]
