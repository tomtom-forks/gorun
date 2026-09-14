package main

import (
	"os"
	"path/filepath"
	"strings"
)

// goBuildEnv assembles the environment for go commands (see go-env-review.md). The
// caller's environment comes first so GOPROXY/GOPRIVATE/HTTP_PROXY etc. still work, then
// site config defaults, then the gorun-managed cache locations - forced, so that leaked
// GOPATH/GOCACHE/HOME values can never redirect where a build writes. The embedded go.env
// section is appended last: with last-entry-wins semantics that gives the precedence
// embedded go.env > config file > inherited environment. HOME is never touched, so
// ~/.netrc and git credentials keep working for private module fetches.
func (s *Script) goBuildEnv() (env []string) {
	env = os.Environ()
	// built-in defaults: build with exactly the installed toolchain rather than
	// auto-downloading one, and ignore any per-user go env config file so the embedded
	// go.env section stays the per-script authority. The config file (next) and the
	// embedded go.env (last) can both override these.
	env = append(env, "GOTOOLCHAIN=local", "GOENV=off")
	env = append(env, s.cfg.env...)
	// forced gorun-managed locations. GOTMPDIR sits alongside the binary so build
	// temporaries are auto-cleaned with it.
	env = append(env,
		"GOCACHE="+filepath.Join(s.cacheRoot, "gocache"),
		"GOMODCACHE="+filepath.Join(s.cacheRoot, "gomod"),
		"GOTMPDIR="+s.tmpDir)
	goEnvSection := string(getSection(s.content, "go.env"))
	for line := range strings.SplitSeq(goEnvSection, "\n") {
		if line != "" { // getSection output starts/ends with newlines - skip empty entries
			env = append(env, line)
		}
	}
	return env
}
