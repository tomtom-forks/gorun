# Implementation log: gorun Go environment changes

Implements the seven changes from `go-env-review.md` ("Changes" section), one commit per
change, with the `test/envmatrix/` container matrix run after each. The matrix CHECK
lines assert the target design, so FAILs count the gaps still open; the goal is 17 cases,
0 failed checks by change 7.

Base: `origin/master` at `e0c4727` (the revision salt deploys): default build base
`/var/tmp`, `cleanDays` default 14, `-noRun` flag, PID-directory build locking
(`waitForActiveBuilds`), stale build-directory cleanup (`cleanSecsBuildDirs`, 1h),
`GOTMPDIR` under the per-script dir, and binary/`.lastRun` touching against
systemd-tmpfiles ageing of `/var/tmp`.

**Baseline** (before change 1): 17 cases, **10 failed checks** — cases 03, 04, 05, 11,
12, 13, 14, 15, 16, 17.

## Change 1 — /etc/gorun.conf support (2026-09-11)

Added `config.go` (`configPath`/`Config`/`loadConfig`/`isEnvKey`) and wired it into
`main()`; related functionality gets its own file from here on, so the Containerfile now
copies `*.go` into the image build.

- `Config` is the layered result: `loadConfig` starts from the built-in defaults
  (`target_dir_base=/var/tmp`, `clean_days=14` on this base) and overlays whatever the
  file sets, so `main()` registers `-targetDirBase`/`-cleanDays` with `cfg` values as
  their defaults and parsing argv last completes the precedence chain — no default
  variables or "was it set" flags in `main()`.
- Flat `key=value` parsing (stdlib only; decision 1). Keys: `go_bin`, `cache_base`,
  `target_dir_base`, `clean_days`; any all-uppercase key is a build-env default. Blank
  lines and `#` comments allowed.
- Hard error if the file exists but is not root-owned, is group/world writable, has an
  unknown key, or fails to parse (decision 4). A missing file yields built-in defaults.
- `GORUN_ARGS` is honoured only when no config file exists; with one present it is
  ignored entirely, with a one-line stderr warning if set (decision 5, revised
  2026-09-14 — simpler than per-key stripping, and nobody here uses `GORUN_ARGS`).
- `Script` carries `cfg`; `go_bin`, `cache_base` and the env defaults are parsed here and
  consumed by changes 2-5.

Verification (one-off container tests): valid config honoured for `target_dir_base` and
`clean_days`; `GORUN_ARGS` ignored with a warning when the config exists, honoured
without it; argv `-targetDirBase` beats config; group-writable, non-root-owned, and
unknown-key config files each refused with a clear error.

Matrix: **17 cases, 10 failed checks** — identical to baseline, as expected: the
container installs no `/etc/gorun.conf` until change 6, and without one behaviour is
unchanged.

## Change 2 — deterministic build environment (2026-09-11)

Added `buildenv.go` with `goBuildEnv()`: `os.Environ()` → config env defaults → forced
`GOCACHE=<cacheRoot>/gocache`, `GOMODCACHE=<cacheRoot>/gomod` and the pre-existing
`GOTMPDIR=<tmpDir>` (moved here from `compile()`, so build temporaries still land beside
the binary for `clean()` to age) → embedded `go.env` last (last-entry-wins gives go.env >
config > inherited). In `gorun.go`: `initVars()` resolves `cacheRoot`
(`<cache_base>/<euid>` when configured, else `perUserTmpDir` under `/var/tmp` — decision
3); `compile()`'s HOME/GOCACHE heuristic (including the EACCES/ENOENT trap) deleted, not
fixed; the now-unused `getEnvVar()` removed; the `waitForActiveBuilds`/`targetOutOfDate`
re-check that precedes the build is untouched. HOME is never modified. The review's minor
observation (empty-string entries from splitting the `go.env` section) fixed in passing,
using `strings.SplitSeq` (Go 1.24). `perUserTmpDir` is now named by `os.Geteuid()` like
`cacheRoot` and the ownership checks (was `os.Getuid()`; identical in every real
invocation, where uid == euid).

Matrix: **17 cases, 4 failed checks** (12, 15, 16, 17). Newly passing: 03 (module cache
in gorun-managed location), 04 + 05 (the reported incident: leaked GOPATH/HOME no longer
break or redirect builds), 11 (persistent per-user cache: recompile after touch no longer
re-downloads modules), 13 + 14 (leaked XDG_CACHE_HOME/GOCACHE ignored). Remaining fails
belong to change 5 (12), change 4 (15, 17) and changes 3+6 (16).

## Change 3 — create and guard the per-uid cache dirs (2026-09-11)

Added `safedir.go` with `ensureOwnedDir()`: MkdirAll 0700, then refuse unless the
directory is owned by the effective uid with no group/other access (decision 4: fail
closed, the condition is root-fixable). `compile()` guards `cacheRoot` before doing
anything. `clean()` skips the `gocache`/`gomod` dirs *before both of its passes* — on this
base that matters: the stale-build-dir sweep added in `a49b610` deletes numeric-named
subdirectories older than an hour as abandoned PID dirs, and the Go build cache's shard
directories (`00`..`ff`) are numeric, so in the no-config fallback layout they would have
been pruned. The post-build chmod walk is removed — the read-only module cache no longer
lives under the deleted per-run directory.

Matrix: **17 cases, 5 failed checks** (12, 15, 16×2, 17) — the expected interim
regression: case 16's squatted `/var/tmp/gorun-<host>-0` is now *refused* instead of
silently used, so its "script ran" check fails alongside the ownership check.
Fail-closed is the decided behaviour; the case passes once change 6 moves binaries under
`/var/cache/gorun`, and change 7 redesigns its checks to accept refusal as the safe
outcome. All other results unchanged from change 2.

## Change 4 — GOTOOLCHAIN=local and GOENV=off built-in defaults (2026-09-11)

`goBuildEnv()` now appends `GOTOOLCHAIN=local` and `GOENV=off` immediately after
`os.Environ()`, so they override inherited values but are themselves overridable by the
config file's env keys and by the embedded `go.env` section (decision 2's forced set is
now complete: GOCACHE, GOMODCACHE, GOTOOLCHAIN, GOENV, with GOTMPDIR alongside).

Matrix: **17 cases, 3 failed checks** (12, 16×2). Newly passing: 15 (a go.mod requiring
go 1.99.0 now fails fast with "requires go >= 1.99.0 (running go 1.24.2;
GOTOOLCHAIN=local)" instead of attempting a toolchain download) and 17 (a user's
`~/.config/go/env` setting GOPROXY=off no longer affects builds). Remaining: 12
(change 5), 16 (changes 6+7).

## Change 5 — goBinaryPath via go_bin, PATH, well-known fallback (2026-09-11)

Moved `goBinaryPath`/`goVer`/`compiledVersion`/`installedGoVersion` into a new `gobin.go`
as `Script` methods. Lookup order is now the configured `go_bin` (a hard error if it is
set but missing), then the PATH, then `/usr/local/go/bin/go`; the deprecated
`runtime.GOROOT()` lookup is gone (it was empty in `-trimpath` builds anyway, and GOROOT
was an env-leak channel), and the `runtime` import with it — `errors` stays, as `main()`
on this base wraps `runScript` failures with it. `goVer` now runs with `goBuildEnv()`
instead of `os.Environ()`, so version checks and builds always agree, and parses the
output with `strings.Fields` instead of trimming and splitting on single spaces.

Matrix: **17 cases, 2 failed checks** (16×2). Newly passing: 12 (systemd/cron-style
minimal PATH finds the toolchain via the well-known fallback). Only case 16's intentional
fail-closed refusal remains, resolved by changes 6+7.

## Change 6 — example config: go.sh, gorun.conf, /var/cache/gorun (2026-09-11)

- `example/linux/etc/profile.d/go.sh` no longer exports GOPATH (the incident's root
  cause) and no longer sets HOME=/root; it only adds `/usr/local/go/bin`,
  `/usr/local/gopath/bin` and (guarded) `$HOME/go/bin` to the PATH.
- New `example/linux/etc/gorun.conf`: `go_bin=/usr/local/go/bin/go`,
  `cache_base=/var/cache/gorun`, `target_dir_base=/var/cache/gorun` (decision 6) — the
  latter also takes binaries out of systemd-tmpfiles' ageing of `/var/tmp`, which
  `e0c4727`'s per-run touching works around. The tmpfiles.d line for the 1777 root-owned
  base is documented inline; GOTOOLCHAIN/GOENV shown commented since they are built-in
  defaults.
- Containerfile installs the example conf (chmod 644 — the repo copy is group-writable
  and would be refused) and creates `/var/cache/gorun` mode 1777; `reset()` in the matrix
  recreates that base per case, as tmpfiles.d would at boot.
- Main README: default location and toolchain-location bullets updated, the nobody/HOME
  gotcha rewritten (caches are persistent, HOME never modified), new "Site configuration
  (/etc/gorun.conf)" section; envmatrix README note about installing the conf resolved.

Matrix: **17 cases, 0 failed checks.** Case 16's binary now lives at
`/var/cache/gorun/gorun-<host>-0/...` — every path component root-owned, the squatted
`/var/tmp` directory irrelevant. Change 7 still to come: the perUserTmpDir ownership guard
in `runScript()` (defence when the target base itself is squattable) and the case 16
redesign to squat both locations and accept fail-closed refusal as the safe outcome.

## Change 7 — perUserTmpDir ownership guard (2026-09-11)

`runScript()` now calls `ensureOwnedDir(perUserTmpDir)` right after `initVars()` — before
`clean()`, the concurrent-build wait, compiling, or executing a cached binary — so a
squatter-owned directory is refused on the run path too, not only when the cache root is
created. Matrix case 16 redesigned: alice squats *both* `/var/tmp/gorun-<host>-0` and
`/var/cache/gorun/gorun-<host>-0`, and the checks accept either safe outcome — a binary
whose every parent directory is root-owned, or a fail-closed refusal naming the
ownership problem. envmatrix README finalised: "Current" column renamed "Pre-fix"
(historical, against `e0c4727`), any FAIL is now a regression.

Matrix: **17 cases, 0 failed checks** — gorun refused the squatted target directory
("owned by uid 1001, not uid 0 - refusing to use it"), no binary written.

## Follow-up — mechanical modernisation of pre-existing code (2026-09-14)

No behaviour change; brings upstream idioms in `gorun.go` up to the module's Go 1.24
level, as flagged by `gofmt -s`/`go vet ./...`/gopls `modernize` and a manual pass:
range-over-int loops, `min()` for the backoff cap, `errors.Is(err, fs.ErrNotExist)` for
`os.IsNotExist`, `filepath.WalkDir` for `filepath.Walk`, `os.ReadDir` in `clean()` (which
also closes the directory handle the old `os.Open`+`Readdir` never released),
`bytes.ReplaceAll`, `getSection` returning `nil`, `var checkDirs []string`, and no
redundant `.Local()`. `modernize ./...` now reports nothing.

Matrix: **17 cases, 0 failed checks.**

## Follow-up — error reporting fixes in pre-existing code (2026-09-14)

Behaviour changes, all in how failures are reported: errors are returned wrapped
(`%w`) instead of being printed to *stdout* and then also returned (`updateTarget`,
`runCommand`, `copyDir`, `writeFileFromComments`), so each failure is reported once, on
stderr, with its cause chain; `main()` only adds "failed to find compiled binary" when
the error really is a missing binary (`fs.ErrNotExist`) rather than to every `runScript`
failure; "no script given" is detected with `flag.NArg() == 0` instead of comparing token
and flag counts (which let `gorun -cleanDays 3` through), and an unresolvable source
path now reports on stderr and exits 1 (was stdout, exit 0); `diffEmbedded` returns an
error instead of calling `os.Exit`; `copyDir` checks the `filepath.Rel` and `os.Mkdir`
results it used to ignore; `clean()` no longer dereferences a nil `FileInfo` when
`.lastRun` fails to stat for a reason other than not existing; the debug "lower active
build" message goes to stderr like the other debug output; `extractIfMissingEmbedded`
propagates the error from `extractEmbedded` instead of discarding it (previously masked
by the print inside `writeFileFromComments` — the message appeared but gorun exited 0);
likewise `writeFileFromCommentsOrDir` no longer discards `copyDir`'s result — a missing
on-disk `go.mod`/`go.sum`/`go.work` is still fine (they are optional), but an unreadable
or uncopyable one is now reported instead of surfacing as a confusing `go build` failure.
Every other caller of the changed functions was traced to `main()`'s error handler.

Matrix: **17 cases, 0 failed checks.**

## Result

All seven changes from `go-env-review.md` implemented on `origin/master` (`e0c4727`),
one commit each, matrix-verified at every step: 10 failed checks at baseline → 0 after
change 7. The reported incident (root's GOPATH leaking into a less privileged user's
build) is structurally impossible; `nobody`/homeless users get persistent caches;
systemd/cron units work with a minimal PATH; toolchain downloads and per-user go env
files no longer affect builds; squatted directories are refused; and with binaries and
caches under `/var/cache/gorun`, systemd-tmpfiles ageing of `/var/tmp` no longer
threatens them. Still open (operational, pre-rollout — see the review's "Implementation
decisions"): audit deployed scripts' `go` directives against 1.24.2, audit for
`go env -w` settings scripts depend on, choose a cache retention mechanism, size
/var/cache, and deploy the tmpfiles.d rule + /etc/gorun.conf via configuration
management.
