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

    podman run --rm --tmpfs /tmp gorun-envmatrix

Strict mode — exit non-zero on any failed check, for CI against a fixed gorun:

    podman run --rm --tmpfs /tmp -e STRICT=1 gorun-envmatrix

## Test cases

The `[scenario N]` references are to the scenario numbering in
`go-env-review.md`. "Current" is the result against the current gorun; every
FAIL corresponds to a gap described in the review.

| Case | Description                                                                    | Review scenario | Current |
|------|--------------------------------------------------------------------------------|-----------------|---------|
| 01   | root, login shell (profile.d sourced)                                          | 2               | OK      |
| 02   | alice (normal user), login shell, own home                                     | 1               | OK      |
| 03   | alice, login shell, script with module downloads                               | 1               | OK      |
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
| 15   | embedded go.mod requires go 1.99.0 (GOTOOLCHAIN auto-download)                 | 8               | FAIL    |
| 16   | /tmp squatting: alice pre-creates root's gorun directory                       | 9               | FAIL    |

## Reading the output

Each case prints the command, exit status, the tail of its output, which cache
directories gained files (and their owner), and `CHECK` lines asserting the
*desired* behaviour.

**CHECK FAILs against the current gorun are expected** — 8 at the time of
writing, per the table above. A fixed gorun should reach 0.

## Notes

- `setpriv` is used to change uid *without* touching the environment — the
  faithful reproduction of a daemon dropping privileges, which is how the
  original GOPATH leak occurred. `su`/`su -` cover the login-shell variants.
- The users baked into the image: `alice` (normal), `bob` (read-only home),
  `ghost` (passwd home path that doesn't exist), plus the stock `nobody`.
- `--tmpfs /tmp` gives every run a virgin /tmp; the script additionally wipes
  all caches between cases and uses an mtime marker to attribute new files.
- binfmt_misc registration and real systemd units are intentionally out of
  scope here (they don't affect the env-handling code paths); test those once
  on a host or with `podman run --privileged` / `--systemd=on`.
