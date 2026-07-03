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
// Note: OpenSandbox's RunCommandRequest has an Envs field, but Envs
// is additive (merged into the sandbox process env) and cannot express
// `env -i`, so CleanEnv is still realized through the `env -i` prefix
// in the command string. The Envs field is left nil.
func envToken(base, spec map[string]string, clean bool) string {
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
		return "env -i " + strings.Join(parts, " ") + " "
	}
	if len(parts) == 0 {
		return ""
	}
	return "env " + strings.Join(parts, " ") + " "
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
