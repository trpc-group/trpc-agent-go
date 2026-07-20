//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"path"
	"path/filepath"
	"strings"
)

// isWrapperName returns true when name matches a shellsafe implicit-deny
// wrapper (sh, bash, eval, env, sudo, xargs, ...). The shellsafe layer
// remains the authoritative gate; this helper exists so the shell-bypass
// rule can attach a stable finding id to those names.
func isWrapperName(name string) bool {
	base := basenameLower(name)
	switch base {
	case "sh", "bash", "zsh", "ash", "dash", "ksh", "mksh", "fish",
		"pwsh", "powershell", "cmd",
		"busybox", "toybox",
		"eval", "exec", "command", "source", ".", "builtin",
		"xargs", "env", "nohup", "timeout",
		"sudo", "su", "doas",
		"setsid", "unshare", "chroot", "runuser",
		"time", "nice", "ionice", "taskset",
		"stdbuf", "strace", "ltrace",
		"script", "flock",
		"trap", "alias", "unalias", "enable", "hash",
		"export", "unset", "readonly",
		"local", "declare", "typeset",
		"set", "shopt",
		"cd", "pushd", "popd",
		"printf", "read", "getopts", "let", "mapfile", "readarray":
		return true
	}
	return false
}

// basenameLower returns the lowercased basename of name.
func basenameLower(name string) string {
	if name == "" {
		return ""
	}
	clean := filepath.ToSlash(name)
	base := path.Base(clean)
	return strings.ToLower(base)
}

// isNetworkCommand returns true for known network commands and any name
// ending in a configured network command name. The configured list is
// applied at the rule level; this helper only covers the built-in set.
func isNetworkCommand(exec string) bool {
	switch basenameLower(exec) {
	case "curl", "wget", "nc", "netcat", "ncat", "ssh", "scp", "sftp",
		"ftp", "git", "aria2c", "aria2", "telnet", "socat":
		return true
	}
	return false
}

// isSleepCommand returns true when argv is a sleep invocation.
func isSleepCommand(exec string, argv []string) bool {
	if basenameLower(exec) != "sleep" {
		return false
	}
	return len(argv) >= 2
}

// sleepSeconds parses the first numeric argument to sleep. Returns -1 when
// no number could be parsed. Handles bare decimal integers and floating
// point values. Non-numeric arguments like "infinity" return -1; the
// resource rule separately checks for "infinity" and treats it as
// unbounded.
func sleepSeconds(argv []string) int64 {
	if len(argv) < 2 {
		return -1
	}
	s := strings.TrimSpace(argv[1])
	if s == "" {
		return -1
	}
	// Check for infinity/non-numeric sleep targets that indicate
	// unbounded sleep.
	switch strings.ToLower(s) {
	case "infinity", "inf", "forever":
		// Return a very large value so the resource rule flags it.
		return 1<<62 - 1
	}
	var n int64
	if _, err := parseDecimalInt(s, &n); err != nil {
		// Try floating point: parse the integer part.
		dot := strings.IndexByte(s, '.')
		if dot > 0 {
			if _, err := parseDecimalInt(s[:dot], &n); err == nil {
				return n
			}
		}
		return -1
	}
	return n
}

// isInstallCommand returns true for known package install commands.
func isInstallCommand(exec string, argv []string) bool {
	base := basenameLower(exec)
	// go install, go get, go mod download (dependency changes).
	if base == "go" && hasToken(argv, "install", "get", "mod") {
		return true
	}
	// npm/pnpm/yarn install, add, i, ci.
	if base == "npm" || base == "pnpm" || base == "yarn" {
		if hasToken(argv, "install", "i", "add", "ci", "install-save") {
			return true
		}
	}
	if base == "pip" || base == "pip3" || base == "uv" || base == "poetry" {
		if hasToken(argv, "install") {
			return true
		}
	}
	if base == "python" || base == "python3" {
		if hasFlagSubcommand(argv, "-m", "pip") && hasToken(argv, "install") {
			return true
		}
	}
	switch base {
	case "apt", "apt-get", "yum", "dnf", "zypper", "brew", "cargo",
		"pacman", "pkg", "choco", "scoop", "winget", "go":
		if hasToken(argv, "install") {
			return true
		}
	}
	return false
}

// isOutputBomb returns true for unbounded output generators.
func isOutputBomb(exec string, argv []string) bool {
	base := basenameLower(exec)
	switch base {
	case "yes":
		return true
	case "seq":
		// seq with a single argument (end) is bounded; seq with two
		// arguments (start end) is bounded; seq with no arguments is
		// invalid. Only flag bare seq with no end argument.
		// Actually, seq always has an end — the last numeric argument.
		// The risk is when the end is very large, but that's a
		// resource concern, not an output bomb. So we do NOT flag seq.
		return false
	case "dd":
		// dd without count= is potentially unbounded.
		if !hasFlagPrefix(argv, "count=") {
			return true
		}
	case "tail":
		if hasFlag(argv, "-f", "--follow") {
			return true
		}
	case "tcpdump", "tshark":
		return true
	}
	return false
}

// hasToken returns true when any of names appears in argv.
func hasToken(argv []string, names ...string) bool {
	for _, a := range argv {
		for _, n := range names {
			if a == n {
				return true
			}
		}
	}
	return false
}

// hasFlag returns true when any of flags appears in argv.
func hasFlag(argv []string, flags ...string) bool {
	return hasToken(argv, flags...)
}

// hasFlagPrefix returns true when any argv token starts with prefix.
func hasFlagPrefix(argv []string, prefix string) bool {
	for _, a := range argv {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// hasFlagSubcommand returns true when flag (e.g. -m) is followed by
// subcommand (e.g. pip) anywhere in argv.
func hasFlagSubcommand(argv []string, flag, subcommand string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == subcommand {
			return true
		}
	}
	return false
}

// parseDecimalInt parses a non-negative decimal integer into out. Returns
// an error when the input contains non-digit bytes or is empty.
func parseDecimalInt(s string, out *int64) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errEmptyNumber
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return i, errNonDigit{c: c}
		}
		n = n*10 + int64(c-'0')
	}
	*out = n
	return len(s), nil
}

type errNonDigit struct{ c byte }

func (e errNonDigit) Error() string { return "non-digit byte in number" }

var errEmptyNumber = &parseError{msg: "empty number"}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }
