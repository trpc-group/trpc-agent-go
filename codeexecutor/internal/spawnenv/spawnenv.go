// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package spawnenv builds the leading `env ...` token sequence that
// the POSIX workspace runtimes (codeexecutor/container and
// codeexecutor/e2b) prepend to a shell command line to inject the
// per-call environment.
//
// Both runtimes execute a command through a bash string of the
// shape `... && cd <cwd> && env K=v K=v <cmd> <args>`, so the only
// lever they have over the spawned command's environment is the
// `env` token. This package centralises two concerns so the two
// backends stay in sync with codeexecutor/local's CleanEnv
// contract:
//
//   - When RunProgramSpec.CleanEnv is false the prefix is the
//     historical `env K=v ...`, i.e. the command inherits the
//     shell/sandbox environment plus the listed overrides.
//
//   - When RunProgramSpec.CleanEnv is true the prefix is
//     `env -i K=v ...`, so the command starts from an empty
//     environment and sees only the workspace base variables and
//     the (already policy-scrubbed) caller env. A MinimalPATH entry
//     is injected when the caller did not supply PATH, because
//     `env -i` clears PATH and `env` itself resolves the command
//     name through the environment it is about to set.
//
// The package has no dependencies outside the standard library and
// never mutates its inputs.
package spawnenv

import (
	"sort"
	"strings"
)

// MinimalPATH is the default search path injected for a CleanEnv
// spawn that does not carry its own PATH. It mirrors the POSIX
// branch of codeexecutor/local's cleanEnvPath so the three runtimes
// resolve bare commands against the same minimal set of system
// directories. The container and e2b runtimes always target Linux
// sandboxes, so the Windows branch local needs is not required here.
const MinimalPATH = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// pathKey is the canonical, case-sensitive PATH variable name. The
// container and e2b runtimes target Linux, where environment names
// are case-sensitive, so a caller-supplied "Path" is a distinct
// variable and must not suppress the MinimalPATH injection.
const pathKey = "PATH"

// Prefix returns the `env ...` (or `env -i ...`) token sequence,
// terminated with a trailing space, to splice in front of the
// quoted command on a shell command line.
//
//   - base holds the runtime-supplied workspace variables
//     (WORKSPACE_DIR, SKILLS_DIR, ...). A base entry is dropped when
//     spec overrides the same key, matching the runtimes' existing
//     "user env wins" behaviour.
//   - spec holds the caller/tool-supplied env. When a command policy
//     is active the tool layer has already scrubbed it via
//     internal/envscrub before it reaches the runtime.
//   - clean selects `env -i` (empty starting environment) and the
//     MinimalPATH fallback.
//   - quote shell-quotes a single value for the target runtime.
//
// The result is empty only when clean is false and there is nothing
// to inject; a clean prefix always contains at least PATH.
func Prefix(
	base, spec map[string]string,
	clean bool,
	quote func(string) string,
) string {
	parts := assignments(base, spec, quote)
	if clean {
		if !hasPath(base, spec) {
			parts = append(
				[]string{pathKey + "=" + quote(MinimalPATH)},
				parts...,
			)
		}
		return "env -i " + strings.Join(parts, " ") + " "
	}
	if len(parts) == 0 {
		return ""
	}
	return "env " + strings.Join(parts, " ") + " "
}

// assignments renders base (minus keys overridden by spec) followed
// by spec into a deterministic, sorted slice of quoted K=v tokens.
// Sorting keeps the generated command line stable across runs (Go
// map iteration order is randomised) without changing semantics:
// environment-variable order on an `env` invocation is irrelevant.
func assignments(
	base, spec map[string]string,
	quote func(string) string,
) []string {
	parts := make([]string, 0, len(base)+len(spec))
	for _, k := range sortedKeys(base) {
		if _, ok := spec[k]; ok {
			continue
		}
		parts = append(parts, k+"="+quote(base[k]))
	}
	for _, k := range sortedKeys(spec) {
		parts = append(parts, k+"="+quote(spec[k]))
	}
	return parts
}

// hasPath reports whether the effective environment (base entries
// not overridden by spec, plus spec) already defines PATH.
func hasPath(base, spec map[string]string) bool {
	if _, ok := spec[pathKey]; ok {
		return true
	}
	_, ok := base[pathKey]
	return ok
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
