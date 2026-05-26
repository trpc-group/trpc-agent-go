// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package envscrub holds the shared blocklist of "policy-mode"
// environment variables that must be removed before handing a map
// to a child process when a workspace_exec command-name policy is
// active.
//
// Two call sites use it today:
//
//   - tool/workspaceexec scrubs caller-supplied env before setting
//     spec.CleanEnv = true (so a model-supplied PATH / BASH_ENV /
//     LD_PRELOAD cannot rearm a command admitted by the policy).
//
//   - codeexecutor.mergeProviderEnv scrubs the provider-supplied
//     env when spec.CleanEnv is true, so a RunEnvProvider returning
//     a "PATH" / "LD_PRELOAD" / "BASH_ENV" cannot reintroduce them
//     after the workspace_exec scrub already ran.
//
// Keeping the blocklist in one place ensures the two scrubs stay
// in sync. The package is intentionally minimal: no policy state,
// no allocation when the input is empty, no dependencies outside
// the standard library.
package envscrub

import "strings"

// shellStartupBlocklist is the canonical name set for variables
// that can redirect the shell's start-up path, the dynamic
// linker's resolution, or word-splitting / glob semantics. All
// keys are upper-case so the case-insensitive lookup can fold
// once and compare directly.
var shellStartupBlocklist = map[string]struct{}{
	// Shell start-up file selection.
	"HOME":           {}, // sh -l sources $HOME/.profile
	"ENV":            {}, // sh sources $ENV on every invocation
	"BASH_ENV":       {}, // bash sources $BASH_ENV on non-interactive starts
	"PROMPT_COMMAND": {}, // bash hook executed before each prompt
	"PS4":            {}, // bash debug prompt, can re-enter shell
	"SHELL":          {}, // some tools spawn $SHELL
	"SHELLOPTS":      {}, // bash options
	"BASHOPTS":       {}, // bash options

	// Executable / search-path control. The allow/deny policy
	// only reasons about command names, so a caller-supplied PATH
	// pointing at workspace-controlled binaries would let a
	// malicious "echo" / "python" / "git" pass the policy and
	// execute attacker code. Drop PATH and rely on the shell's
	// default.
	"PATH": {},

	// Word-splitting / glob-expansion semantics.
	"IFS":        {}, // changes word-splitting semantics
	"CDPATH":     {}, // affects how `cd` resolves arguments
	"GLOBIGNORE": {},

	// Dynamic linker hijacks.
	"LD_PRELOAD":                {},
	"LD_LIBRARY_PATH":           {},
	"LD_AUDIT":                  {},
	"DYLD_INSERT_LIBRARIES":     {}, // macOS
	"DYLD_LIBRARY_PATH":         {}, // macOS
	"DYLD_FORCE_FLAT_NAMESPACE": {},
}

const bashFuncPrefix = "BASH_FUNC_"

// IsBlocked reports whether name should be removed from a policy-
// mode env map. caseInsensitive should be true on Windows (where
// the runtime treats env keys case-insensitively) so that
// "Path=./bin" / "Home=." / "bash_func_x" cannot survive by
// varying capitalisation.
func IsBlocked(name string, caseInsensitive bool) bool {
	if isShellStartupKey(name, caseInsensitive) {
		return true
	}
	return isBashFuncKey(name, caseInsensitive)
}

// IsMalformedKey reports whether name is not usable as a POSIX
// env name. An empty key, or one containing "=", "\0", "\n" or
// "\r", would otherwise survive the blocklist and either confuse
// the kernel's env serialisation (e.g. a key "PATH=." becomes the
// env entry "PATH=.=<value>", which libc parses as PATH set to
// ".=<value>") or split a single entry into two on libc
// implementations that scan for newlines.
func IsMalformedKey(name string) bool {
	if name == "" {
		return true
	}
	return strings.ContainsAny(name, "=\x00\n\r")
}

// Scrub returns a fresh map containing every entry from in whose
// key is neither malformed nor in the blocklist. The input is
// never mutated. nil and empty inputs return nil so callers can
// distinguish "nothing to do" without a follow-up length check.
func Scrub(in map[string]string, caseInsensitive bool) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if IsMalformedKey(k) {
			continue
		}
		if IsBlocked(k, caseInsensitive) {
			continue
		}
		out[k] = v
	}
	return out
}

func isShellStartupKey(name string, caseInsensitive bool) bool {
	if _, ok := shellStartupBlocklist[name]; ok {
		return true
	}
	if !caseInsensitive {
		return false
	}
	_, ok := shellStartupBlocklist[strings.ToUpper(name)]
	return ok
}

func isBashFuncKey(name string, caseInsensitive bool) bool {
	if strings.HasPrefix(name, bashFuncPrefix) {
		return true
	}
	if caseInsensitive &&
		strings.HasPrefix(strings.ToUpper(name), bashFuncPrefix) {
		return true
	}
	return false
}
