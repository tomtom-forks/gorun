# gorun environment test matrix

Container-based verification of how gorun handles the Go build environment
(GOPATH, GOCACHE, GOMODCACHE, HOME, XDG_CACHE_HOME, GOTOOLCHAIN) for the user
scenarios enumerated in `go-env-review.md`: root, normal users, `nobody`,
users without a usable home directory, leaked root environments, and
systemd/cron-style minimal environments.

## Usage

Build from the **repo root** (the image compiles gorun from this source tree):

    podman build -t gorun-envmatrix -f test/envmatrix/Containerfile .

Run (rootful podman/docker recommended; network needed for module downloads):

    podman run --rm --tmpfs /var/tmp gorun-envmatrix

Strict mode — exit non-zero on any failed check, for CI against a fixed gorun:

    podman run --rm --tmpfs /var/tmp -e STRICT=1 gorun-envmatrix

## Test cases

The `[scenario N]` references are to the scenario numbering in
`go-env-review.md`. The CHECK lines assert the *target* design from that review
(the `/etc/gorun.conf` approach: deterministic gorun-managed caches, GOTOOLCHAIN
local and GOENV off by default, configured `go_bin`). "Pre-fix" is the result
against gorun *before* the review's changes were implemented (`origin/master`
at `e0c4727`); each FAIL marked a gap versus the design. The implemented gorun
passes all 17 cases (0 failed checks), so any FAIL now indicates a regression.

| Case | Description                                                                    | Review scenario | Pre-fix |
|------|--------------------------------------------------------------------------------|-----------------|---------|
| 01   | root, login shell (profile.d sourced)                                          | 2               | OK      |
| 02   | alice (normal user), login shell, own home                                     | 1               | OK      |
| 03   | alice, login shell, script with module downloads                               | 1               | FAIL    |
| 04   | INCIDENT: full root env (GOPATH, HOME) leaked to alice via daemon-style setuid | 3               | FAIL    |
| 05   | INCIDENT variant: `su alice -c` without `-` (GOPATH leaks, su resets HOME)     | 3               | FAIL    |
| 06   | nobody, HOME=/nonexistent                                                      | 4               | OK      |
| 07   | nobody, HOME=/                                                                 | 4               | OK      |
| 08   | nobody, HOME unset                                                             | 5               | OK      |
| 09   | ghost: passwd home path does not exist                                         | 5               | OK      |
| 10   | bob: read-only home directory                                                  | 5               | OK      |
| 11   | nobody: module cache thrown away between compiles (re-download every time)     | 4               | FAIL    |
| 12   | minimal env (systemd/cron): stock PATH, no profile.d                           | 6               | FAIL    |
| 13   | alice with leaked XDG_CACHE_HOME=/root/.cache                                  | 7               | FAIL    |
| 14   | alice with leaked GOCACHE=/root/.cache/go-build                                | 7               | FAIL    |
| 15   | embedded go.mod requires go 1.99.0 (default GOTOOLCHAIN behaviour)             | 8               | FAIL    |
| 16   | dir squatting: alice pre-creates root's gorun dirs (/var/tmp + /var/cache)     | 9               | FAIL    |
| 17   | alice with ~/.config/go/env setting GOPROXY=off                                | 7               | FAIL    |

Note case 03: the script compiles and runs today, but the module cache lands in
alice's home rather than the gorun-managed per-uid location the target design
prescribes — the FAIL marks the cache *location*, not a broken build.

## Reading the output

Each case prints the command, exit status, the tail of its output, which cache
directories gained files (and their owner), and `CHECK` lines asserting the
*desired* behaviour.

**The implemented gorun passes all cases: 17 cases, 0 failed checks.** Any
CHECK FAIL indicates a regression against the design in `go-env-review.md`
(strict mode makes that a non-zero exit for CI). The Containerfile installs the
example `/etc/gorun.conf` from `example/linux/etc/`, so the matrix exercises
the configured paths (`/var/cache/gorun`); the config-refusal and no-config
code paths are covered by gorun's own hard-error checks and the built-in
fallbacks. Case 16 accepts either safe outcome: a binary whose every parent
directory is root-owned, or a fail-closed refusal naming the ownership problem.

## Notes

- `setpriv` is used to change uid *without* touching the environment — the
  faithful reproduction of a daemon dropping privileges, which is how the
  original GOPATH leak occurred. `su`/`su -` cover the login-shell variants.
- The users baked into the image: `alice` (normal), `bob` (read-only home),
  `ghost` (passwd home path that doesn't exist), plus the stock `nobody`.
- `--tmpfs /var/tmp` gives every run a virgin /var/tmp (gorun's default
  `targetDirBase` since upstream commit 00bc87f); the script additionally wipes
  all caches between cases and uses an mtime marker to attribute new files.
- binfmt_misc registration and real systemd units are intentionally out of
  scope here (they don't affect the env-handling code paths); test those once
  on a host or with `podman run --privileged` / `--systemd=on`.
