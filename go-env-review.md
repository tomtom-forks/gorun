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
leaked-but-wrong HOME all pass through unvalidated. The adopted fix is a root-owned,
optional `/etc/gorun.conf`: gorun sets its cache locations and toolchain path
deterministically from configuration (built-in defaults when the file is absent) and
stops consulting the inherited environment for cache decisions, making the leak
structurally impossible. Independently, the example `profile.d/go.sh` should stop
exporting GOPATH (unnecessary since Go 1.8, and that export is what leaks).

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

## Changes

These assume adoption of a root-owned, optional `/etc/gorun.conf` (see "Rationale and
trade-offs" below). Precedence throughout: command-line flags > embedded `go.env`
(per-script, repeatable) > `/etc/gorun.conf` (site policy) > built-in defaults.

1. **Add `/etc/gorun.conf` support to gorun.** Keys: `go_bin`, `cache_base`,
   `target_dir_base`, `clean_days`, and default build env settings (e.g. `GOTOOLCHAIN`,
   `GOENV`, `GOAUTH`). Honour the file only if root-owned and not group/world-writable;
   without it, built-in defaults apply (Mac dev machines keep working). This also
   supersedes `GORUN_ARGS` for site configuration — an env var, and therefore the same
   leak-prone channel as GOPATH.
2. **Make the build env deterministic in `compile()`.** Set
   `GOCACHE=<cache_base>/<euid>/gocache` and `GOMODCACHE=<cache_base>/<euid>/gomod`
   unconditionally; never consult inherited GOPATH/GOCACHE/GOMODCACHE/XDG_CACHE_HOME;
   leave HOME untouched (preserving `~/.netrc`/git credentials for GOPRIVATE). Delete the
   HOME/GOCACHE heuristic at `gorun.go:443-454` — including the EACCES/ENOENT trap at
   `gorun.go:447` — rather than fixing it. The embedded `go.env` section remains the
   per-script override. This makes the reported GOPATH leak structurally impossible.
3. **Create and guard the per-uid cache dirs.** `cache_base` defaults to
   `/var/cache/gorun` (packaged via `tmpfiles.d`/deployment tooling; `/tmp` fallback
   built-in when absent); per-uid subdirs 0700, verified owned-by-euid after MkdirAll
   (the ssh `~/.ssh` pattern). Teach `clean()` (`gorun.go:504-523`) to skip or age the
   cache dirs, and drop the chmod walk at `gorun.go:469-476` — the module cache no longer
   lives under the deleted per-run directory.
4. **Default `GOTOOLCHAIN=local` and `GOENV=off`** as built-in defaults, with site
   override in the config and per-script override in the embedded `go.env`.
5. **Rework `goBinaryPath()`** to use `go_bin` from the config first, then PATH, then the
   well-known `/usr/local/go/bin/go`; drop the deprecated `runtime.GOROOT()` lookup, and
   use the same env in `goVer()` (`gorun.go:374`) as in `compile()` so version checks and
   builds agree.
6. **`example/linux/etc/profile.d/go.sh`: stop exporting GOPATH.** gorun no longer needs
   anything from it, but plain `go` usage still benefits: keep only the PATH additions
   (`/usr/local/go/bin`, `/usr/local/gopath/bin`, and `$HOME/go/bin` guarded by
   `[ -n "$HOME" ]`), and drop the `HOME=/root` fixup — another leak vector. Ship an
   example `/etc/gorun.conf` alongside it in `example/linux/etc/`.
7. **Verify ownership/mode of `perUserTmpDir`** (the binary target area) before writing
   or exec'ing anything under it — or move `target_dir_base` under `/var/cache/gorun` via
   the config so it starts from a root-owned parent.

## Scenarios

### 1. Normal user, interactive shell, own home

**Today:** Works. GOPATH from `go.sh` is `$HOME/go`, cache goes to `~/.cache/go-build`,
module cache to `~/go/pkg/mod`. This matches modern Go defaults — which is exactly why
exporting GOPATH in `go.sh` adds nothing here.

**Should:** With `/etc/gorun.conf`, gorun no longer consults the inherited GOPATH/HOME
for its build caches at all — every user's gorun builds use
`<cache_base>/<uid>/{gocache,gomod}` deterministically, so this scenario needs no special
handling. `go.sh` should still stop exporting GOPATH for the benefit of plain `go` usage
(see recommendations).

### 2. Root, interactive shell

**Today:** `go.sh` gives root `GOPATH=/usr/local/gopath` (so `go install` binaries are
shared to everyone via PATH), cache in `/root/.cache/go-build`. Works, but see scenarios
3 and 6.

**Should:** Root's gorun builds use `<cache_base>/0/...` like any other uid — the config
makes the interactive-vs-service GOPATH inconsistency (scenario 6) irrelevant to gorun.
For the shared-binaries goal, keep `/usr/local/gopath/bin` on PATH but don't *export*
GOPATH into every subsequent process — install shared tools deliberately with
`GOBIN=/usr/local/gopath/bin go install`.

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

**Should:** With `/etc/gorun.conf` this leak becomes structurally impossible rather than
detected-and-repaired: gorun sets `GOCACHE`/`GOMODCACHE` to `<cache_base>/<euid>/...`
unconditionally and never consults inherited `GOPATH`, `GOCACHE`, `GOMODCACHE`,
`XDG_CACHE_HOME` or `HOME` for cache decisions. Only the embedded `go.env` section (part
of the script, so trusted and repeatable) may override. The entire heuristic in
`gorun.go:443-454` — including the EACCES/ENOENT trap at `gorun.go:447` — is deleted, not
fixed.

### 4. User `nobody` / system users

**Today:** With `HOME=/nonexistent` (Debian) the mkdir fails and the fallback works; with
`HOME=/` or unset it also works. But because HOME is pointed at the *per-run* directory,
which is deleted after the build (`gorun.go:427`), every recompile re-downloads all
modules and — on Go ≥1.21 — potentially an entire toolchain (see scenario 8). The README
documents this as a gotcha rather than fixing it.

**Should:** Solved by construction: `nobody` gets `<cache_base>/<uid>/{gocache,gomod}`
like every other user — persistent (surviving reboots when `cache_base` is
`/var/cache/gorun`), no HOME rewriting, no re-download on recompile. Two details:
`clean()` (`gorun.go:504-523`) must be taught to skip or age the cache dirs (today it
only deletes dirs containing `.lastRun`), and with the module cache no longer living
under the deleted per-run directory, the chmod walk at `gorun.go:469-476` can go
(`GOFLAGS=-modcacherw` remains the modern tool if a throwaway module cache is ever
wanted).

### 5. HOME unset entirely, or HOME on read-only/root-squashed storage

**Today:** Unset HOME → fallback works. Read-only home → `mkdir $HOME/.cache` fails →
fallback works. But note the fallback overrides HOME itself, which as a side effect hides
`~/.netrc` and `~/.gitconfig` — so private-module fetches (the very GOPRIVATE use case the
embedded `go.env` exists for) break for these users.

**Should:** HOME becomes irrelevant to gorun and is left completely alone: caches come
from `cache_base`, so there is no fallback that rewrites HOME, and `~/.netrc`/
`~/.gitconfig` credential lookup keeps working wherever a real home exists. For users
with no usable home at all, `GOAUTH` (Go ≥1.24, already deployed) covers private-module
authentication via the embedded `go.env` or site config.

### 6. Script run from systemd/cron (no profile.d)

**Today:** `profile.d/go.sh` is never sourced, so PATH is minimal. `goBinaryPath()`
(`gorun.go:404-421`) tries `runtime.GOROOT()` — empty in a `-trimpath` build unless a
GOROOT env var is set — then `exec.LookPath("go")`, which fails on a stock service PATH.
So goscripts in units/cron fail unless each unit sets PATH. Also note interactive root
and systemd root end up with *different* GOPATHs (`/usr/local/gopath` vs default
`/root/go`), so root compiles and caches everything twice depending on how the script was
invoked.

**Should:** `go_bin = /usr/local/go/bin/go` in `/etc/gorun.conf` solves this outright:
`goBinaryPath()` uses the configured path first, so units and cron jobs need no
`Environment=PATH=...` and the deprecated `runtime.GOROOT()` lookup can be dropped. On
machines without the config file, fall back to PATH plus the well-known
`/usr/local/go/bin/go` location. And since caches no longer depend on GOPATH, root's
interactive-vs-service cache inconsistency disappears too.

### 7. Explicit GOCACHE/GOMODCACHE/GOENV/XDG_CACHE_HOME in the environment

**Today:** Fully trusted. Two gaps: (a) a leaked root-owned `GOCACHE`/`GOMODCACHE` fails
exactly like the GOPATH leak, and (b) `XDG_CACHE_HOME` silently bypasses gorun's logic
entirely — Go's `os.UserCacheDir` consults it *before* `$HOME/.cache`, so gorun may
carefully create `$HOME/.cache` while the build actually writes to
`$XDG_CACHE_HOME/go-build`. `GOENV` (or a stray `~/.config/go/env`) can also change
proxy/flags per machine, undermining the repeatable-build story.

**Should:** All moot once `GOCACHE`/`GOMODCACHE` are set explicitly from `cache_base`:
inherited `GOCACHE`/`GOMODCACHE`/`XDG_CACHE_HOME` are simply never consulted. `GOENV=off`
becomes the built-in default (overridable in config or the embedded `go.env`), so a stray
`~/.config/go/env` can't change builds per machine — the embedded `go.env` section is the
single source of build configuration, squarely in the spirit of gorun's repeatability
goal.

### 8. Go ≥1.21 toolchain auto-download (GOTOOLCHAIN)

**Today:** Unhandled. If a script's embedded `go.mod` says `go 1.25` and the installed
toolchain is older, `go build` downloads a whole toolchain into the module cache. For
homeless users that means downloading a toolchain *on every compile* into a directory
that is deleted afterwards; combined with `-recompileWrongGoVer` semantics it also
muddies "which go compiled this".

**Should:** Default to `GOTOOLCHAIN=local` in the build env, as a built-in default with
`/etc/gorun.conf` able to set site policy and the embedded `go.env` able to override
per-script. Builds then use exactly the installed toolchain (1.24.2 in production) and
version mismatches fail loudly instead of downloading surprises.

### 9. Adjacent but important: /tmp directory trust

`perUserTmpDir` has a predictable name (`/tmp/gorun-<host>-<uid>`, `gorun.go:178-183`)
and `os.MkdirAll` (`gorun.go:267`) succeeds silently if another user pre-created it. The
compiled binary is then written and `exec`'d from a directory that may be attacker-owned —
a real privilege-escalation path when root runs goscripts. The same trust question
applies to any persistent cache added per scenario 4.

**Should:** Packaging `cache_base` as `/var/cache/gorun` (created root-owned via
`tmpfiles.d` or the deployment tooling, per-uid subdirs 0700) takes the caches out of
world-writable `/tmp`; consider pointing `targetDirBase` there too. The residual check
stays but is small: after MkdirAll of a per-uid directory, verify it is owned by the euid
with mode 0700 (the ssh `~/.ssh` pattern), and refuse if not. The same applies to
`/etc/gorun.conf` itself: honour it only if root-owned and not group/world-writable.

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

## Rationale and trade-offs for /etc/gorun.conf

Most of the complexity the pre-config recommendations carried came from gorun having to
*infer* a safe environment from whatever it inherited — validating each of
HOME/GOCACHE/GOMODCACHE/GOPATH/XDG_CACHE_HOME against the euid and repairing the unusable
ones. A root-owned `/etc/gorun.conf` replaces inference with declaration: the admin
states the policy once (where the toolchain is, where caches live, which build defaults
apply) and gorun stops caring what leaked in. Less code, and a stronger guarantee — the
leak class is eliminated by construction rather than detected case by case.

### Trade-off: separate module cache

Under this design gorun's builds use `<cache_base>/<uid>/gomod` and
`<cache_base>/<uid>/gocache`, while a regular `go build` by the same user uses the Go
defaults (`~/go/pkg/mod`, `~/.cache/go-build`). The separation is deliberate — it is what
lets gorun avoid asking "is this user's normal cache usable?" — but it costs:

- **Duplicate downloads and disk:** a module used both by a goscript and by normal `go`
  work is stored twice per user. The example Makefile hits this directly on dev machines
  (`go mod tidy`/`go test` use the normal cache, the `gorun` steps use gorun's).
- **No warm-cache benefit** from an existing `~/go/pkg/mod` on gorun's first compile.

In exchange: leak immunity, one code path for every user type, identical behaviour for
root/normal users/nobody/systemd, and a bounded, known location that `clean()` and ops
tooling can manage. The scope of the cost is limited: only gorun-compiled builds are
affected — interactive `go build`/`go test`/IDE work keeps its normal caches — and on
production VMs, where gorun is the only thing compiling Go, there is effectively no
duplication at all. A hybrid (use `cache_base` only for users without a usable home)
would reintroduce exactly the heuristics this design deletes and make dev and production
diverge; not recommended.

### Costs

A new deployment artifact (fits configuration-management delivery), and machines
without `/etc/gorun.conf` (e.g. Mac dev machines) must keep working on built-in defaults —
the config should only ever *narrow* behaviour, never be required.

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
