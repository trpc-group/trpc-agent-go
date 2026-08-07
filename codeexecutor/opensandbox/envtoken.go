//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import (
	"fmt"
	"sort"
	"strings"
)

// minimalCleanPATH is the default search path injected for a CleanEnv
// spawn that does not carry its own PATH. The OpenSandbox sandbox
// always targets a Linux image, so no Windows branch is needed.
const minimalCleanPATH = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// envToken builds the leading `env ...` (or `env -i ...`) token that
// RunProgram splices in front of the quoted command inside the command
// string handed to the sandbox.
//
//   - base holds the runtime-supplied workspace variables
//     (WORKSPACE_DIR, SKILLS_DIR, ...); a base entry is dropped when
//     spec overrides the same key, matching the "user env wins"
//     behaviour.
//   - spec holds the caller/tool-supplied env. When a command policy
//     is active the tool layer has already scrubbed it before it
//     reaches the runtime.
//   - clean selects `env -i` (empty starting environment) and the
//     minimalCleanPATH fallback, injected only when spec carries no
//     PATH so `env -i` can still resolve the command name.
//
// The result is terminated with a trailing space, or empty when clean
// is false and there is nothing to inject; a clean token always
// contains at least PATH.
//
// All environment variable names (keys) are validated against the POSIX
// shell identifier rule [A-Za-z_][A-Za-z0-9_]*. A key containing shell
// metacharacters (e.g. "X; touch /tmp/pwned #") would be interpolated
// raw into the command string, allowing command injection through the
// outer `bash -c`. envToken rejects such keys with an error so
// RunProgram can surface them before the command is sent.
//
// Note: OpenSandbox's RunCommandRequest has an Envs field, but Envs
// is additive (merged into the sandbox process env) and cannot express
// `env -i`, so CleanEnv is still realized through the `env -i` prefix
// in the command string. The Envs field is left nil.
func envToken(base, spec map[string]string, clean bool) (string, error) {
	// Validate all keys before building the token so we never emit a
	// partially-constructed, potentially dangerous command string.
	for k := range base {
		if !validEnvName(k) {
			return "", fmt.Errorf(
				"opensandbox: invalid environment variable name %q in base env "+
					"(must match [A-Za-z_][A-Za-z0-9_]*)", k,
			)
		}
	}
	for k := range spec {
		if !validEnvName(k) {
			return "", fmt.Errorf(
				"opensandbox: invalid environment variable name %q in spec env "+
					"(must match [A-Za-z_][A-Za-z0-9_]*)", k,
			)
		}
	}

	parts := make([]string, 0, len(base)+len(spec))
	for _, k := range sortedEnvKeys(base) {
		if _, ok := spec[k]; ok {
			continue
		}
		parts = append(parts, k+"="+shellQuote(base[k]))
	}
	for _, k := range sortedEnvKeys(spec) {
		parts = append(parts, k+"="+shellQuote(spec[k]))
	}
	if clean {
		if !hasPathKey(base, spec) {
			parts = append(
				[]string{"PATH=" + shellQuote(minimalCleanPATH)},
				parts...,
			)
		}
		return "env -i " + strings.Join(parts, " ") + " ", nil
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "env " + strings.Join(parts, " ") + " ", nil
}

// validEnvName reports whether name is a valid POSIX shell environment
// variable identifier: it must start with a letter or underscore, and
// contain only letters, digits, and underscores. Empty strings and
// names containing shell metacharacters (spaces, ;, =, $, etc.) are
// rejected to prevent command injection through the env-assignment
// token spliced into the outer `bash -c` command string.
func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_') {
				return false
			}
		} else {
			if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
				(r >= '0' && r <= '9') || r == '_') {
				return false
			}
		}
	}
	return true
}

// hasPathKey reports whether the effective environment (base entries
// not overridden by spec, plus spec) already defines PATH. The check
// is case-sensitive because the sandbox targets Linux, where a
// lowercase "Path" is a distinct variable and must not suppress the
// minimalCleanPATH injection.
func hasPathKey(base, spec map[string]string) bool {
	if _, ok := spec["PATH"]; ok {
		return true
	}
	_, ok := base["PATH"]
	return ok
}

// sortedEnvKeys returns the map keys in sorted order so the generated
// command string is deterministic (Go map iteration order is
// randomised); environment-variable order on an `env` invocation is
// semantically irrelevant.
func sortedEnvKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
