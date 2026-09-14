package main

import (
	"fmt"
	"os"
	"syscall"
)

// ensureOwnedDir creates dir (mode 0700) if needed and verifies it is owned by our
// effective uid with no group/other access. Directories under shared, world-writable
// bases (/var/tmp, /var/cache/gorun) have predictable names, so another user may have
// pre-created one to capture what we write there: refuse to write to - or execute
// from - a directory someone else controls, rather than fall back (decision 4 in
// go-env-review.md: fail closed, the condition is root-fixable).
func ensureOwnedDir(dir string) (err error) {
	err = os.MkdirAll(dir, 0700)
	if err != nil {
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%v is owned by uid %v, not uid %v - refusing to use it", dir, st.Uid, os.Geteuid())
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%v is accessible by other users (mode %04o) - refusing to use it", dir, info.Mode().Perm())
	}
	return nil
}
