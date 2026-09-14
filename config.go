package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// configPath is the optional site configuration file. When present it must be owned by
// root and not group/world writable, or gorun refuses to run.
const configPath = "/etc/gorun.conf"

// Config holds the layered settings: built-in defaults, overlaid by whatever the site
// config file sets. Command-line flags take these values as their defaults, so the
// precedence is: command-line flags > embedded go.env section > config file > built-in
// defaults. GORUN_ARGS is an environment variable and could leak across users just like
// GOPATH did, so it is honoured only when no config file exists.
type Config struct {
	exists        bool     // the config file was present
	goBin         string   // go_bin: absolute path to the go toolchain binary
	cacheBase     string   // cache_base: per-user caches live in <cache_base>/<uid>/{gocache,gomod}
	targetDirBase string   // target_dir_base: default for the -targetDirBase flag
	cleanDays     int64    // clean_days: default for the -cleanDays flag
	env           []string // uppercase KEY=VALUE lines, defaults for the build environment
}

// isEnvKey reports whether a config key names an environment variable (all uppercase)
func isEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r >= 'A' && r <= 'Z' || r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// loadConfig returns the built-in defaults overlaid with the optional site config. A
// missing file leaves the defaults untouched; a file that is present but insecurely
// owned/permissioned or unparseable is a hard error.
func loadConfig(path string) (cfg *Config, err error) {
	cfg = &Config{
		targetDirBase: "/var/tmp",
		cleanDays:     14,
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 {
		return nil, fmt.Errorf("%v must be owned by root", path)
	}
	if info.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("%v must not be group or world writable (mode %v)", path, info.Mode().Perm())
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg.exists = true
	for n, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("%v:%v: expected key=value, got %q", path, n+1, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "go_bin":
			cfg.goBin = value
		case "cache_base":
			cfg.cacheBase = value
		case "target_dir_base":
			cfg.targetDirBase = value
		case "clean_days":
			cfg.cleanDays, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%v:%v: clean_days: %v", path, n+1, err)
			}
		default:
			if !isEnvKey(key) {
				return nil, fmt.Errorf("%v:%v: unknown key %q", path, n+1, key)
			}
			cfg.env = append(cfg.env, key+"="+value)
		}
	}
	return cfg, nil
}
