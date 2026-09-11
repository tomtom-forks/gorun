package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// wellKnownGoBin is where the example config installs the go toolchain; the last-resort
// fallback for systemd/cron contexts whose minimal PATH does not include it.
const wellKnownGoBin = "/usr/local/go/bin/go"

// goBinaryPath returns the path to the go binary: the configured go_bin first, then the
// PATH, then the well-known install location. runtime.GOROOT is deprecated (and empty in
// a -trimpath build) and is no longer consulted.
func (s *Script) goBinaryPath() (gobin string, err error) {
	if s.cfg.goBin != "" {
		if _, err = os.Stat(s.cfg.goBin); err != nil {
			return "", fmt.Errorf("go_bin in %v: %v", configPath, err)
		}
		return s.cfg.goBin, nil
	}
	if gobin, err = exec.LookPath("go"); err == nil {
		return gobin, nil
	}
	if _, statErr := os.Stat(wellKnownGoBin); statErr == nil {
		return wellKnownGoBin, nil
	}
	return "", fmt.Errorf("can't find go tool via go_bin in %v, the PATH (%v) or %v",
		configPath, os.Getenv("PATH"), wellKnownGoBin)
}

// goVer extracts a goversion from the output of a "go version %v" command, run with the
// same environment as builds so version checks and builds always agree
func (s *Script) goVer(args []string, verPos int) (version string, err error) {
	gobin, err := s.goBinaryPath()
	if err != nil {
		return
	}
	var stdoutBuf bytes.Buffer
	cmd := exec.Command(gobin, args...)
	cmd.Stdout = &stdoutBuf
	cmd.Env = s.goBuildEnv()
	err = cmd.Run()
	if err == nil {
		fields := strings.Fields(stdoutBuf.String())
		if len(fields) >= 2 {
			version = fields[len(fields)+verPos]
		} else {
			err = fmt.Errorf("unable to find version in %+v", fields)
		}
	}
	return
}

// compiledVersion returns the version of go used to compile a file
func (s *Script) compiledVersion(filepath string) (fileVersion string, err error) {
	// last entry is the version for a file:
	// /tmp/gorun-myhost-0/_usr_local_bin_myFile.go/myFile.go.bin: go1.23.2
	fileVersion, err = s.goVer([]string{"version", filepath}, -1)
	return
}

// installedGoVersion returns the version of go installed on the system
func (s *Script) installedGoVersion() (gobinVersion string, err error) {
	// second last entry is the version for a file:
	// go version go1.23.2 linux/amd64
	gobinVersion, err = s.goVer([]string{"version"}, -2)
	return
}
