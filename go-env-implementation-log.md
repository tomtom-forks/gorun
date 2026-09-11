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
