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
