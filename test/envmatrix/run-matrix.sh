#!/bin/bash
# gorun Go-environment test matrix (companion to go-env-review.md).
#
# Runs as root inside the gorun-envmatrix container. Each case exercises one
# user/environment scenario from the review and reports:
#   - the exit status and last lines of output
#   - which cache/module directories gained files, and who owns them
#   - CHECK lines asserting the desired behaviour
#
# CHECK FAILs against the current gorun are expected - each marks a gap the
# review describes. A fixed gorun should bring the failure count to zero.
# Set STRICT=1 to exit non-zero on any failed check (for CI on a fixed gorun).
#
# Network access is needed for the module-download and toolchain cases.

HOST=$(hostname)
HELLO=/usr/local/bin/hello.go        # no external deps (build cache only)
DEPS=/usr/local/bin/myscript.go      # external deps (module downloads)
TOOL=/usr/local/bin/toolchain199.go  # embedded go.mod requires go 1.99.0
MARKER=/run/matrix-marker
FAILS=0
CASES=0

# --- helpers -----------------------------------------------------------------

# wipe all caches gorun/go could have written, so each case starts clean;
# the marker lets us find everything written during the case
reset() {
    rm -rf /tmp/gorun-* /root/.cache /root/go /var/cache/gorun \
           /home/alice/.cache /home/alice/.config /home/alice/go /home/ghost
    rm -rf /usr/local/gopath && mkdir /usr/local/gopath  # root-owned, as installed
    touch "$MARKER"
    sleep 1  # keep new mtimes strictly after the marker
}

begin() {
    CASE="$1"; CASES=$((CASES + 1))
    printf '\n==== %s ====\n' "$CASE"
    reset
}

# run CMD...: execute, capture combined output and exit code in OUT / RC
run() {
    echo "  cmd : $*"
    OUT=$("$@" 2>&1); RC=$?
    echo "  exit: $RC"
    [ -n "$OUT" ] && tail -4 <<<"$OUT" | sed 's/^/  out | /'
}

# list files created since the marker in the locations we care about
wrote() {
    echo "  new files (owner, depth<=3):"
    find /tmp/gorun-* /var/cache/gorun /root/.cache /root/go \
         /home/*/.cache /home/*/go /usr/local/gopath \
         -maxdepth 3 -newer "$MARKER" -printf '    %u %p\n' 2>/dev/null | sort | head -15
}

check() {
    local desc="$1"; shift
    if "$@" >/dev/null 2>&1; then
        echo "  CHECK OK  : $desc"
    else
        echo "  CHECK FAIL: $desc"
        FAILS=$((FAILS + 1))
    fi
}

nothing_new_under() {
    [ -z "$(find "$@" -newer "$MARKER" 2>/dev/null | head -1)" ]
}

out_contains() { grep -qi -- "$1" <<<"$OUT"; }

# target design (go-env-review.md): caches live in a gorun-managed per-uid dir,
# <cache_base>/<uid>/{gocache,gomod} - /var/cache/gorun via /etc/gorun.conf,
# or the built-in /tmp fallback
gorun_managed_cache() {
    find /var/cache/gorun /tmp/gorun-"$HOST"-* -maxdepth 2 -type d -name "$1" \
         2>/dev/null | grep -q .
}

# every directory component above the given file must be root-owned
# (stops at the known root-owned parents /tmp, /var/cache)
path_owned_by_root() {
    local p
    [ -n "$1" ] || return 1
    p=$(dirname "$1")
    while [ "$p" != "/" ] && [ "$p" != "/tmp" ] && [ "$p" != "/var/cache" ]; do
        [ "$(stat -c %U "$p" 2>/dev/null)" = root ] || return 1
        p=$(dirname "$p")
    done
}

# setpriv changes uid/gid but does NOT touch the environment - the faithful
# reproduction of a daemon calling setuid() without cleaning its env
as_alice()  { setpriv --reuid alice  --regid alice   --init-groups  "$@"; }
as_bob()    { setpriv --reuid bob    --regid bob     --init-groups  "$@"; }
as_ghost()  { setpriv --reuid ghost  --regid ghost   --init-groups  "$@"; }
as_nobody() { setpriv --reuid nobody --regid nogroup --clear-groups "$@"; }

# --- review scenarios 1+2: interactive users on their own account -------------

begin "01 root, login shell (profile.d sourced) [scenario 2]"
run bash -lc "gorun $HELLO"
wrote
check "script ran" [ "$RC" -eq 0 ]
check "no writes under /home" nothing_new_under /home

begin "02 alice, login shell, own home [scenario 1]"
run su - alice -c "gorun $HELLO"
wrote
check "script ran" [ "$RC" -eq 0 ]
check "no writes to root's caches" nothing_new_under /root /usr/local/gopath

begin "03 alice, login shell, script with module downloads [scenario 1]"
run su - alice -c "gorun $DEPS --help"
wrote
check "compiled and ran (--help exits 0)" [ "$RC" -eq 0 ]
check "module cache in gorun-managed location (target behaviour after fix)" \
      gorun_managed_cache gomod
check "no writes to /usr/local/gopath" nothing_new_under /usr/local/gopath

# --- scenario 3: root env leaking into a less privileged user ------------------

begin "04 INCIDENT: full root env leaked to alice (daemon-style setuid) [scenario 3]"
run as_alice env GOPATH=/usr/local/gopath HOME=/root gorun "$DEPS" --help
wrote
check "compiled and ran despite leaked env (--help exits 0)" [ "$RC" -eq 0 ]
check "no writes to root's caches" nothing_new_under /root
check "no writes to /usr/local/gopath" nothing_new_under /usr/local/gopath

begin "05 INCIDENT variant: su alice -c without '-' (GOPATH leaks, su resets HOME) [scenario 3]"
# non-login su preserves the env except HOME/SHELL/USER/LOGNAME/PATH; PATH is reset
# from login.defs, so re-add the go toolchain inside - the leak under test is GOPATH
run bash -c ". /etc/profile.d/go.sh && su alice -c 'PATH=\$PATH:/usr/local/go/bin gorun $DEPS --help'"
wrote
check "compiled and ran (--help exits 0)" [ "$RC" -eq 0 ]
check "no writes to /usr/local/gopath" nothing_new_under /usr/local/gopath

# --- scenarios 4+5: nobody and homeless users ----------------------------------

begin "06 nobody, HOME=/nonexistent [scenario 4]"
run as_nobody env HOME=/nonexistent gorun "$HELLO"
wrote
check "script ran" [ "$RC" -eq 0 ]

begin "07 nobody, HOME=/ [scenario 4]"
run as_nobody env HOME=/ gorun "$HELLO"
check "script ran" [ "$RC" -eq 0 ]

begin "08 nobody, HOME unset [scenario 5]"
run as_nobody env -u HOME gorun "$HELLO"
check "script ran" [ "$RC" -eq 0 ]

begin "09 ghost: passwd home /home/ghost does not exist [scenario 5]"
run as_ghost env HOME=/home/ghost gorun "$HELLO"
check "script ran" [ "$RC" -eq 0 ]

begin "10 bob: read-only home directory [scenario 5]"
run as_bob env HOME=/home/bob gorun "$HELLO"
check "script ran" [ "$RC" -eq 0 ]
check "nothing written into read-only home" nothing_new_under /home/bob

begin "11 nobody: module cache thrown away between compiles [scenario 4]"
t0=$SECONDS
run as_nobody env HOME=/nonexistent gorun "$DEPS" --help
echo "  first compile+run: $((SECONDS - t0))s"
check "compiled and ran (--help exits 0)" [ "$RC" -eq 0 ]
t0=$SECONDS
run as_nobody env HOME=/nonexistent gorun "$DEPS" --help
echo "  cached-binary run: $((SECONDS - t0))s"
touch "$DEPS"
t0=$SECONDS
run as_nobody env HOME=/nonexistent gorun "$DEPS" --help
echo "  recompile after touch: $((SECONDS - t0))s (current gorun re-downloads all modules here)"
check "persistent gorun-managed module cache (target behaviour after fix)" \
      gorun_managed_cache gomod

# --- scenario 6: systemd/cron-style minimal environment ------------------------

begin "12 minimal env (systemd/cron): stock PATH, no profile.d [scenario 6]"
run env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/root /usr/local/bin/gorun "$HELLO"
check "gorun locates the go toolchain and runs" [ "$RC" -eq 0 ]

# --- scenario 7: leaked cache-related variables ---------------------------------

begin "13 alice with leaked XDG_CACHE_HOME=/root/.cache [scenario 7]"
run as_alice env HOME=/home/alice XDG_CACHE_HOME=/root/.cache gorun "$HELLO"
wrote
check "script ran" [ "$RC" -eq 0 ]
check "no writes to /root" nothing_new_under /root

begin "14 alice with leaked GOCACHE=/root/.cache/go-build [scenario 7]"
run as_alice env HOME=/home/alice GOCACHE=/root/.cache/go-build gorun "$HELLO"
check "script ran" [ "$RC" -eq 0 ]
check "no writes to /root" nothing_new_under /root

# --- scenario 8: toolchain auto-download ----------------------------------------

begin "15 embedded go.mod requires go 1.99.0 (GOTOOLCHAIN) [scenario 8]"
# no explicit GOTOOLCHAIN: exercises the default (target: GOTOOLCHAIN=local built in)
run su - alice -c "gorun $TOOL"
check "build failed (no go 1.99 exists)" [ "$RC" -ne 0 ]
check "failed locally without a download attempt (target: GOTOOLCHAIN=local default)" \
      out_contains "GOTOOLCHAIN=local"
run su - alice -c "GOTOOLCHAIN=local gorun $TOOL"
check "explicit GOTOOLCHAIN=local fails fast with a clear version error" \
      out_contains "requires go >= 1.99"

# --- scenario 9: /tmp trust ------------------------------------------------------

begin "16 /tmp squatting: alice pre-creates root's gorun directory [scenario 9]"
su alice -c "mkdir -p /tmp/gorun-$HOST-0 && chmod 777 /tmp/gorun-$HOST-0"
run gorun "$HELLO"
BIN=$(find /tmp/gorun-* /var/cache/gorun -name '*.bin' -newer "$MARKER" 2>/dev/null | head -1)
echo "  binary: ${BIN:-not found}"
check "script ran" [ "$RC" -eq 0 ]
check "root's binary lives under root-owned directories only (not the squatter's)" \
      path_owned_by_root "$BIN"

# --- scenario 7 continued: user go env config file --------------------------------

begin "17 alice with ~/.config/go/env setting GOPROXY=off [scenario 7]"
su - alice -c "mkdir -p ~/.config/go && echo GOPROXY=off > ~/.config/go/env"
run su - alice -c "gorun $DEPS --help"
check "user go/env file ignored (target: GOENV=off default): compiled and ran" \
      [ "$RC" -eq 0 ]

# --- summary ---------------------------------------------------------------------

printf '\n==== SUMMARY: %d cases, %d failed checks ====\n' "$CASES" "$FAILS"
echo "Failed checks against the CURRENT gorun are expected: each marks a gap"
echo "described in go-env-review.md. A fixed gorun should reach 0 FAILs."
if [ "${STRICT:-0}" = "1" ] && [ "$FAILS" -gt 0 ]; then
    exit 1
fi
exit 0
