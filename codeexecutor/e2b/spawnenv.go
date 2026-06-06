//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"sort"
	"strings"
)

// minimalCleanPATH is the default search path injected for a CleanEnv
// program that does not carry its own PATH. It mirrors the POSIX branch
// of codeexecutor/local's cleanEnvPath.
const minimalCleanPATH = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// cleanWrapperCommand starts the E2B bash wrapper itself from a
// minimal environment. The bootstrap shell that RunCode uses only
// evaluates this exec line; the framing wrapper and target program run
// in the clean bash process.
func cleanWrapperCommand(script string) string {
	return "/usr/bin/env -i PATH=" + shellQuote(minimalCleanPATH) +
		" /bin/bash --noprofile --norc -c " + shellQuote(script)
}

// envToken builds the leading `env ...` or `env -i ...` token that
// RunProgram places in front of the quoted command.
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
			parts = append([]string{
				"PATH=" + shellQuote(minimalCleanPATH),
			}, parts...)
		}
		return "env -i " + strings.Join(parts, " ") + " "
	}
	if len(parts) == 0 {
		return ""
	}
	return "env " + strings.Join(parts, " ") + " "
}

func hasPathKey(base, spec map[string]string) bool {
	if _, ok := spec["PATH"]; ok {
		return true
	}
	_, ok := base["PATH"]
	return ok
}

func sortedEnvKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
