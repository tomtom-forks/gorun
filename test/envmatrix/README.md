# gorun environment test matrix

Container-based verification of how gorun handles the Go build environment
(GOPATH, GOCACHE, GOMODCACHE, HOME, XDG_CACHE_HOME, GOENV, GOTOOLCHAIN) for
every kind of user a goscript can run as: root, normal users, `nobody`, users
without a usable home directory, users who inherit root's environment, and
systemd/cron-style minimal environments.

## Usage

Build from the **repo root** (the image compiles gorun from this source tree):

    podman build -t gorun-envmatrix -f test/envmatrix/Containerfile .

Run (rootful podman/docker recommended; network needed for module downloads):

    podman run --rm --tmpfs /var/tmp gorun-envmatrix

Strict mode, exit non-zero on any failed check, for CI:

    podman run --rm --tmpfs /var/tmp -e STRICT=1 gorun-envmatrix

A clean run reports `17 cases, 0 failed checks`. Any `CHECK FAIL` is a
regression in gorun's environment handling.

## The design under test

gorun never infers a safe build environment from whatever it inherited. It
declares one:

- **Caches are gorun-managed and keyed by effective uid.** Every build uses
  `GOCACHE=<cache_base>/<uid>/gocache` and `GOMODCACHE=<cache_base>/<uid>/gomod`,
  set unconditionally. `cache_base` comes from `/etc/gorun.conf` (the example
  config uses `/var/cache/gorun`); without a config file the caches live in the
  per-user binary directory, `/var/tmp/gorun-<host>-<uid>/`. Inherited `GOPATH`,
  `GOCACHE`, `GOMODCACHE` and `XDG_CACHE_HOME` play no part in cache placement.
- **Credentials come from the user's real home.** `~/.netrc` and `~/.gitconfig`
  work for private module fetches wherever a home exists. Users with no usable
  home can use `GOAUTH` instead, via the embedded `go.env` section or the site
  config.
- **`GOTOOLCHAIN=local` and `GOENV=off` are built-in defaults.** Builds use
  exactly the installed toolchain and fail loudly on a version mismatch instead
  of downloading one, and a per-user `~/.config/go/env` cannot change a build.
  The site config and the embedded `go.env` section can both override these.
- **The toolchain is located deterministically:** `go_bin` from the config
  first, then the PATH, then `/usr/local/go/bin/go`. Units and cron jobs need no
  `Environment=PATH=...`.
- **Directories gorun writes to or executes from must be its own.** The
  per-user binary directory and the per-uid cache directories have predictable
  names under shared bases, so after creating one gorun verifies it is owned by
  the effective uid with mode 0700 and refuses to continue otherwise. The config
  file itself is honoured only when root-owned and not group/world writable.
- **Precedence:** command-line flags > embedded `go.env` section >
  `/etc/gorun.conf` > built-in defaults. `GORUN_ARGS` is an environment
  variable, so it can leak between users like any other; it is honoured only on
  machines without a config file.

The rest of the caller's environment (`GOPROXY`, `GOPRIVATE`, `HTTP_PROXY`,
`GOAUTH`, even `GOPATH`) passes through to `go build` untouched. `GOPATH` is
harmless once `GOMODCACHE` is forced.

The example `profile.d/go.sh` deliberately does not export `GOPATH`. Go has
defaulted it to `$HOME/go` since 1.8, and an exported `GOPATH` is exactly what
leaks through `su` and privilege-dropping daemons to less privileged users.
Shared tools are installed for everyone with `GOBIN=/usr/local/gopath/bin go install`
and found via the PATH.

### Trade-off: a separate module cache

gorun's builds use `<cache_base>/<uid>/{gocache,gomod}`, while a plain
`go build` by the same user uses Go's defaults (`~/.cache/go-build`,
`~/go/pkg/mod`). A module used both by a goscript and by normal `go` work is
therefore stored twice per user, and gorun's first compile gets no benefit from
a warm `~/go/pkg/mod`. In exchange every user type takes one code path with
identical behaviour, and the caches live in a bounded, known location that
`clean()` and ops tooling can manage. On production VMs, where gorun is the
only thing compiling Go, there is no duplication at all.

## Scenarios

Each scenario describes a situation a goscript runs in, what gorun does about
it, and which cases in `run-matrix.sh` cover it. The table maps every case to
its scenario:

| Case | Description                                                                | Scenario |
|------|----------------------------------------------------------------------------|----------|
| 01   | root, login shell (profile.d sourced)                                      | 2        |
| 02   | alice (normal user), login shell, own home                                 | 1        |
| 03   | alice, login shell, script with module downloads                           | 1        |
| 04   | full root env (GOPATH, HOME) leaked to alice via daemon-style setuid       | 3        |
| 05   | `su alice -c` without `-` (GOPATH leaks, su resets HOME)                   | 3        |
| 06   | nobody, HOME=/nonexistent                                                  | 4        |
| 07   | nobody, HOME=/                                                             | 4        |
| 08   | nobody, HOME unset                                                         | 5        |
| 09   | ghost: passwd home path does not exist                                     | 5        |
| 10   | bob: read-only home directory                                              | 5        |
| 11   | nobody: module cache kept between compiles                                 | 4        |
| 12   | minimal env (systemd/cron): stock PATH, no profile.d                       | 6        |
| 13   | alice with leaked XDG_CACHE_HOME=/root/.cache                              | 7        |
| 14   | alice with leaked GOCACHE=/root/.cache/go-build                            | 7        |
| 15   | embedded go.mod requires go 1.99.0 (GOTOOLCHAIN)                           | 8        |
| 16   | dir squatting: alice pre-creates root's gorun dirs (/var/tmp + /var/cache) | 9        |
| 17   | alice with ~/.config/go/env setting GOPROXY=off                            | 7        |

### 1. Normal user, interactive shell, own home

The common case. gorun's builds use `<cache_base>/<uid>/{gocache,gomod}` and
never touch the user's home or root's caches. Cases 02 and 03 (03 adds module
downloads and checks the module cache lands in the gorun-managed location, not
in the user's home).

### 2. Root, interactive shell

Root is uid 0 like any other uid: builds use `<cache_base>/0/...`. Nothing is
written under `/home`. Case 01.

### 3. Root drops privileges without cleaning the environment

`su alice -c script.go` without `-`, a daemon that calls `setuid()`, or sudo
with a permissive `env_keep`. The child inherits `GOPATH=/usr/local/gopath` and
often `HOME=/root` and `XDG_CACHE_HOME`. Because gorun sets the cache locations
from the effective uid and ignores the inherited variables, the build succeeds
and writes nothing under `/root` or `/usr/local/gopath`. Case 04 reproduces the
daemon form with `setpriv` (full root environment, uid changed, nothing
cleaned); case 05 the non-login `su` form (GOPATH leaks, `su` resets HOME).

### 4. `nobody` and other system users

`HOME=/nonexistent` (Debian's convention for `nobody`) or `HOME=/`. The caches
do not depend on HOME and persist between compiles, so a recompile does not
re-download modules. Cases 06, 07 and 11 (11 times a compile, a cached
run and a recompile, and checks the module cache is still present afterwards).

### 5. HOME unset, missing, or read-only

HOME unset entirely; a passwd home path that does not exist; a home directory
that is read-only. In every case the build works and nothing is written into
the home. Cases 08, 09 and 10.

### 6. systemd/cron: minimal PATH, no profile.d

The stock service PATH does not include `/usr/local/go/bin`. gorun finds the
toolchain through `go_bin` in the config (or the well-known location) and runs.
Case 12.

### 7. Cache-related variables set in the environment

A leaked `XDG_CACHE_HOME` or `GOCACHE` pointing at root's cache would otherwise
redirect the build (`os.UserCacheDir` consults `XDG_CACHE_HOME` before
`$HOME/.cache`). gorun never consults them, so nothing is written under
`/root`. A user's `~/.config/go/env` is ignored because `GOENV=off` is the
default. Cases 13, 14 and 17.

### 8. Toolchain auto-download

A script whose embedded `go.mod` requires a Go version newer than the installed
one. With Go's default `GOTOOLCHAIN=auto` the `go` command would download a
toolchain into the module cache; with gorun's `GOTOOLCHAIN=local` default the
build fails fast with a version error and no download attempt. Case 15 uses a
`go 1.99.0` requirement, which no toolchain can satisfy, both with the default
and with an explicit `GOTOOLCHAIN=local`.

### 9. Directory squatting

`/var/tmp` is world-writable (sticky) and `/var/cache/gorun` is created the same
way, so another user can pre-create root's predictably named gorun directories
to capture, or substitute, what root writes and executes there. Case 16 has
alice pre-create root's directories under both bases, world-writable. Either
safe outcome passes: a binary whose every parent directory is root-owned, or a
refusal that names the ownership problem. Writing into, or running from, the
squatted directory fails the case.

## Reading the output

Each case prints the command, exit status, the tail of its output, which cache
directories gained files (and their owner), and `CHECK` lines asserting the
desired behaviour. The Containerfile installs the example `/etc/gorun.conf`
from `example/linux/etc/`, so the matrix exercises the configured paths under
`/var/cache/gorun`. The config-refusal and no-config code paths are covered by
gorun's own hard-error checks and built-in fallbacks rather than by this matrix.

## Notes

- `setpriv` changes uid without touching the environment, which is what a
  daemon dropping privileges does and how a GOPATH leak from root to a less
  privileged user happens. `su` and `su -` cover the login-shell variants.
- The users baked into the image: `alice` (normal), `bob` (read-only home),
  `ghost` (passwd home path that doesn't exist), plus the stock `nobody`.
- `--tmpfs /var/tmp` gives every run a fresh `/var/tmp`; the script additionally
  wipes all caches between cases and uses an mtime marker to attribute new files.
- binfmt_misc registration and real systemd units are out of scope here: they
  don't affect the environment-handling code paths. Test those on a host or
  with `podman run --privileged` / `--systemd=on`.
