# Review: gorun Go environment handling

**Date:** 2026-08-24
**Author:** Mark Bartlett (review assisted by Claude Code)
**Scope:** `gorun.go` and the example config in `example/linux/`, with regard to how the Go
build environment (GOPATH, GOCACHE, GOMODCACHE, HOME, etc.) is handled for scripts run by
root, normal users, `nobody`, and users without a usable home directory — measured against
current Go (≥1.21) cache/toolchain conventions.
**Trigger:** an issue where root's GOPATH leaked through when a script was run as a less
privileged user.

## TL;DR

The GOPATH leak is not a one-off bug — it is a consequence of the overall design:
`compile()` builds the `go build` environment by **trusting the entire inherited
environment** (`gorun.go:437`) and then patching exactly one thing, the HOME/GOCACHE pair
(`gorun.go:443-454`). GOPATH, GOMODCACHE, GOENV, XDG_CACHE_HOME, GOTOOLCHAIN and even a
leaked-but-wrong HOME all pass through unvalidated. The fix should be to *validate* the
cache-related variables against the effective uid rather than special-casing "HOME
missing", and the example `profile.d/go.sh` should stop exporting GOPATH at all (it has
been unnecessary since Go 1.8, and that export is what leaks).

## How the build environment is assembled today

1. `env = os.Environ()` — everything the caller had (`gorun.go:437`).
2. The embedded `go.env` section is appended, so it wins over inherited values
   (last-entry-wins, matched by `getEnvVar`).
3. If `GOCACHE` is unset: when `HOME` is empty or `/`, set `HOME=<perRunTmpDir>`;
   otherwise, if `$HOME/.cache` doesn't exist, try to `mkdir` it, and on failure fall back
   to `HOME=<perRunTmpDir>` (`gorun.go:443-454`).
4. The per-run dir — including the module cache and build cache when HOME was redirected
   there — is deleted after the build, with a chmod walk to defeat the read-only module
   cache (`gorun.go:469-476`).
5. The compiled script itself runs with the original untouched environment
   (`gorun.go:498`) — correct, since the script binary doesn't need the go env.

## Scenarios

### 1. Normal user, interactive shell, own home

**Today:** Works. GOPATH from `go.sh` is `$HOME/go`, cache goes to `~/.cache/go-build`,
module cache to `~/go/pkg/mod`. This matches modern Go defaults — which is exactly why
exporting GOPATH in `go.sh` adds nothing here.

**Should:** No behavior change needed, but `go.sh` should stop exporting GOPATH (see
recommendations).

### 2. Root, interactive shell

**Today:** `go.sh` gives root `GOPATH=/usr/local/gopath` (so `go install` binaries are
shared to everyone via PATH), cache in `/root/.cache/go-build`. Works, but see scenarios
3 and 6.

**Should:** If the shared-binaries goal is only the PATH entry, keep
`/usr/local/gopath/bin` on PATH but don't *export* GOPATH into every subsequent process —
set it only where root actually runs `go install`, or accept root's default `/root/go`
and install shared tools deliberately with `GOBIN=/usr/local/gopath/bin go install`.

### 3. Root drops privileges without cleaning the environment (the reported issue)

This is `su nobody -c script.go` without `-`, a daemon that `setuid()`s, or sudo with
permissive `env_keep`. The child inherits `GOPATH=/usr/local/gopath` and often
`HOME=/root`, `XDG_CACHE_HOME`, etc.

**Today:** Nothing checks GOPATH, so `go build` tries to use `/usr/local/gopath/pkg/mod`
as the module cache and fails with permission errors (or, if that tree is
group/world-writable, silently mixes root's and the user's downloads). Worse, the HOME
fixup is fooled too: with leaked `HOME=/root`, `os.Stat("/root/.cache")` fails with
**EACCES, not ENOENT**, so the `os.IsNotExist` check at `gorun.go:447` is false, no
fallback happens, and the build dies writing to root's cache.

**Should:** gorun should treat "is this usable by my euid?" as the test, not "is it set /
does it exist":

- If `HOME` is set, stat it and compare owner uid to `os.Geteuid()`; a HOME owned by
  someone else should be treated the same as no HOME.
- Any stat error on `$HOME/.cache` (not just `IsNotExist`) should count as "unusable";
  better, prove writability by creating a temp file.
- Apply the same validation to `GOPATH` (or its default `$HOME/go`) and `GOMODCACHE`: if
  `$GOPATH/pkg/mod` isn't writable by the euid, override `GOMODCACHE` (and `GOCACHE`) to
  gorun's per-user area instead of letting the build fail.

### 4. User `nobody` / system users

**Today:** With `HOME=/nonexistent` (Debian) the mkdir fails and the fallback works; with
`HOME=/` or unset it also works. But because HOME is pointed at the *per-run* directory,
which is deleted after the build (`gorun.go:427`), every recompile re-downloads all
modules and — on Go ≥1.21 — potentially an entire toolchain (see scenario 8). The README
documents this as a gotcha rather than fixing it.

**Should:** Give homeless users a **persistent** per-user cache under the already-existing
`perUserTmpDir` (`/tmp/gorun-<host>-<uid>/`), e.g. `GOCACHE=<perUserTmpDir>/gocache`,
`GOMODCACHE=<perUserTmpDir>/gomod`, instead of a throwaway HOME. That keeps recompiles
fast and stops the re-download-everything behavior. Two details: `clean()`
(`gorun.go:504-523`) must be taught to skip or age these cache dirs (today it only
deletes dirs containing `.lastRun`), and if a throwaway module cache is ever still
wanted, `GOFLAGS=-modcacherw` is the modern replacement for the chmod walk at
`gorun.go:469-476`.

### 5. HOME unset entirely, or HOME on read-only/root-squashed storage

**Today:** Unset HOME → fallback works. Read-only home → `mkdir $HOME/.cache` fails →
fallback works. But note the fallback overrides HOME itself, which as a side effect hides
`~/.netrc` and `~/.gitconfig` — so private-module fetches (the very GOPRIVATE use case the
embedded `go.env` exists for) break for these users.

**Should:** Prefer setting `GOCACHE`/`GOMODCACHE` explicitly over rewriting HOME.
Overriding HOME is a blunt instrument; setting the two cache variables achieves the goal
without breaking credential lookup.

### 6. Script run from systemd/cron (no profile.d)

**Today:** `profile.d/go.sh` is never sourced, so PATH is minimal. `goBinaryPath()`
(`gorun.go:404-421`) tries `runtime.GOROOT()` — empty in a `-trimpath` build unless a
GOROOT env var is set — then `exec.LookPath("go")`, which fails on a stock service PATH.
So goscripts in units/cron fail unless each unit sets PATH. Also note interactive root
and systemd root end up with *different* GOPATHs (`/usr/local/gopath` vs default
`/root/go`), so root compiles and caches everything twice depending on how the script was
invoked.

**Should:** Add well-known fallback locations to `goBinaryPath()` (at minimum
`/usr/local/go/bin/go`, which is where the example installs Go), and/or ship an example
drop-in showing `Environment=PATH=...` for units that run goscripts. Fixing `go.sh` to
not export GOPATH also removes the interactive-vs-service inconsistency for root.

### 7. Explicit GOCACHE/GOMODCACHE/GOENV/XDG_CACHE_HOME in the environment

**Today:** Fully trusted. Two gaps: (a) a leaked root-owned `GOCACHE`/`GOMODCACHE` fails
exactly like the GOPATH leak, and (b) `XDG_CACHE_HOME` silently bypasses gorun's logic
entirely — Go's `os.UserCacheDir` consults it *before* `$HOME/.cache`, so gorun may
carefully create `$HOME/.cache` while the build actually writes to
`$XDG_CACHE_HOME/go-build`. `GOENV` (or a stray `~/.config/go/env`) can also change
proxy/flags per machine, undermining the repeatable-build story.

**Should:** Include `XDG_CACHE_HOME` in the validation logic (or simply set `GOCACHE`
explicitly, which sidesteps the XDG question). Consider setting `GOENV=off` for the build
so the embedded `go.env` section is the single source of build configuration — that is
squarely in the spirit of gorun's repeatability goal.

### 8. Go ≥1.21 toolchain auto-download (GOTOOLCHAIN)

**Today:** Unhandled. If a script's embedded `go.mod` says `go 1.25` and the installed
toolchain is older, `go build` downloads a whole toolchain into the module cache. For
homeless users that means downloading a toolchain *on every compile* into a directory
that is deleted afterwards; combined with `-recompileWrongGoVer` semantics it also
muddies "which go compiled this".

**Should:** Default to `GOTOOLCHAIN=local` in the build env (overridable via the embedded
`go.env`), so builds use exactly the installed toolchain and version mismatches fail
loudly instead of downloading surprises.

### 9. Adjacent but important: /tmp directory trust

`perUserTmpDir` has a predictable name (`/tmp/gorun-<host>-<uid>`, `gorun.go:178-183`)
and `os.MkdirAll` (`gorun.go:267`) succeeds silently if another user pre-created it. The
compiled binary is then written and `exec`'d from a directory that may be attacker-owned —
a real privilege-escalation path when root runs goscripts. The same trust question
applies to any persistent cache added per scenario 4.

**Should:** After MkdirAll, verify each component is owned by the euid with mode 0700
(the ssh `~/.ssh` pattern), and refuse or relocate if not.

## Recommended changes, in priority order

1. **`example/linux/etc/profile.d/go.sh`: stop exporting GOPATH.** Keep only the PATH
   additions (`/usr/local/go/bin`, `/usr/local/gopath/bin`, and `$HOME/go/bin` guarded by
   `[ -n "$HOME" ]`). Go has defaulted GOPATH to `$HOME/go` since 1.8; the export is pure
   downside and is the direct cause of the reported leak. Also drop the `HOME=/root`
   fixup — that's another leak vector.
2. **Replace the HOME/GOCACHE special case in `compile()` with euid-based validation** of
   HOME, GOCACHE, GOMODCACHE, GOPATH and XDG_CACHE_HOME: anything not owned/writable by
   the effective uid gets overridden with gorun-managed locations. Treat *any* stat
   failure as unusable, not just `IsNotExist` (`gorun.go:447`).
3. **Set `GOCACHE`/`GOMODCACHE` explicitly instead of rewriting HOME**, pointing homeless
   users at persistent dirs under `perUserTmpDir` (with ownership checks, and `clean()`
   taught about them). This fixes the re-download-everything gotcha and preserves
   `~/.netrc`/git credentials for GOPRIVATE fetches.
4. **Set `GOTOOLCHAIN=local` and consider `GOENV=off`** in the build env (both
   overridable by the embedded `go.env` section, which already wins by append order).
5. **Harden `goBinaryPath()`** with a `/usr/local/go/bin/go` fallback for systemd/cron
   contexts, and use the same sanitized env in `goVer()` (`gorun.go:374`) as in
   `compile()` so version checks and builds agree.
6. **Verify ownership/mode of `perUserTmpDir`** before writing or exec'ing anything under
   it.

## Go version applicability (1.21-1.27)

Checked against the release notes for Go 1.24-1.27 (2026-09): the environment and cache
model this review is built on is unchanged from 1.21 through 1.27 — no changes to
GOCACHE/GOMODCACHE defaults or locations, GOPATH handling, GOTOOLCHAIN auto-download,
GOENV, `os.UserCacheDir`/XDG lookup, or behaviour when HOME is unset or unusable. All
scenarios and recommendations apply identically to the deployed 1.24.2 and to 1.27, so
targeting a later Go version does not simplify the logic; the fixes need not be
conditional on toolchain version. Version-relevant details:

- **GOAUTH (≥1.24, already deployed):** authenticates private module fetches without
  `~/.netrc`/`~/.gitconfig` (`go help goauth`) — softens the scenario-5 downside of an
  overridden HOME for GOPRIVATE fetches.
- **`runtime.GOROOT()` deprecated (1.24):** Go's own guidance is now to locate the `go`
  binary by path rather than trusting GOROOT — endorsing recommendation 5's change to
  `goBinaryPath()`.
- **1.25:** `go` command no longer auto-adds `toolchain` lines when updating `go.mod` —
  marginally less churn in embedded go.mod sections; cosmetic.
- **1.26/1.27:** nothing in scope (GODEBUG/GOEXPERIMENT/FIPS items only).

## Potential proposal: a gorun config file in /etc

Most of the complexity in recommendations 2-5 comes from gorun having to *infer* a safe
environment from whatever it inherited. A root-owned `/etc/gorun.conf` would replace
inference with declaration — the admin states the policy once and gorun stops caring what
leaked in:

- **`go_bin = /usr/local/go/bin/go`** eliminates the whole GOROOT/PATH search chain in
  `goBinaryPath()` and fixes the systemd/cron case (scenario 6) outright — no
  `Environment=PATH=...` per unit, no hardcoded fallback list. The clearest
  simplification.
- **`ignore_inherited_go_env = true` + `cache_base = /var/cache/gorun`** flips
  recommendation 2 from heuristic "validate each of HOME/GOCACHE/GOMODCACHE/GOPATH/
  XDG_CACHE_HOME against the euid and repair" to deterministic "set
  `GOCACHE`/`GOMODCACHE` to `<cache_base>/<uid>/...` unconditionally, unless the embedded
  `go.env` overrides". The GOPATH leak becomes structurally impossible rather than
  detected-and-repaired. Only the ownership/mode check on the per-user directory survives
  — and a `tmpfiles.d`-packaged `/var/cache/gorun` shrinks that too (also improving
  scenario 9, and giving homeless users reboot-surviving caches per recommendation 3).
- **Site-wide defaults for `GOTOOLCHAIN=local` / `GOENV=off`** (recommendation 4) get a
  natural home, with clean precedence: flags > embedded `go.env` (per-script,
  repeatable) > `/etc/gorun.conf` (site policy) > built-in defaults.
- **Replaces `GORUN_ARGS`** for site configuration: `GORUN_ARGS` is itself an environment
  variable, i.e. the same leak-prone channel this review is about (root's
  `-targetDirBase` or `-debug` would leak to a de-privileged child exactly like GOPATH
  did). A config file doesn't travel with the environment.

What it does *not* simplify: recommendation 1 stands regardless — `go.sh` exporting
GOPATH affects plain `go` usage too, not just gorun (though with a config file the
example `go.sh` shrinks to PATH-for-humans only). The trust checks don't vanish either:
gorun must require the config file to be root-owned and not group/world-writable before
honouring it, and per-user cache dirs still deserve the ownership check.

Costs: a new deployment artifact (fits configuration-management delivery), and machines
without `/etc/gorun.conf` (e.g. Mac dev machines) must keep working on built-in defaults —
the config should only ever *narrow* behaviour, never be required.

Net: if a config file is adopted, recommendation 2 is better reframed as "ignore the
inherited Go env by default and use configured locations" — less code and a stronger
guarantee.

## Supplementary tasks

1. In gorun — in `-embed`/`-extract`/`-extractIfMissing`/`-diff` mode, error out (or at
   least warn) when more than one argument is left after flag parsing, since extra file
   arguments can't mean anything there. Today only `flag.Arg(0)` is used
   (`gorun.go:141`) and the rest are silently ignored, so a shell glob like
   `gorun -embed dir/*.go` can operate on the wrong file (e.g. a `_test.go` file,
   depending on locale collation order) without any indication.

## Minor observation

`strings.Split(section, "\n")` on the `go.env` section (`gorun.go:439`) produces
empty-string entries in the env slice (the section starts and ends with `\n`); it appears
harmless today but is worth filtering when this code is touched.
