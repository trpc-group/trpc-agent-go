// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"path"
	"strings"
)

func scanPaths(policy Policy, cwd string, segments [][]string) []Finding {
	if finding, ok := deniedPathFinding(policy.DeniedPaths, cwd); ok {
		return []Finding{finding}
	}
	for _, argv := range segments {
		for _, arg := range argv[1:] {
			if finding, ok := deniedPathFinding(policy.DeniedPaths, arg); ok {
				return []Finding{finding}
			}
		}
	}
	return nil
}

func deniedPathFinding(deniedPaths []string, value string) (Finding, bool) {
	candidate := normalizePath(value)
	if candidate == "" {
		return Finding{}, false
	}
	for _, deniedPath := range deniedPaths {
		denied := normalizePath(deniedPath)
		if denied == "" || !matchesDeniedPath(candidate, denied) {
			continue
		}
		return newFinding(
			DecisionDeny, RiskHigh, "sensitive.path",
			"path matches denied_paths: "+denied,
			"use a path outside the denied_paths policy",
		), true
	}
	return Finding{}, false
}

func normalizePath(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" {
		return ""
	}
	cleaned := path.Clean(value)
	if cleaned == "." && value != "." {
		return ""
	}
	return cleaned
}

func matchesDeniedPath(candidate, denied string) bool {
	if candidate == denied {
		return true
	}
	if strings.Contains(denied, "/") {
		if denied == "/" {
			return strings.HasPrefix(candidate, "/")
		}
		return strings.HasPrefix(candidate, denied+"/")
	}
	return path.Base(candidate) == denied
}
