// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"net/url"
	"path"
	"strings"
)

func scanPaths(policy Policy, cwd string, segments [][]string) []Finding {
	if finding, ok := deniedPathFinding(policy.DeniedPaths, cwd); ok {
		return []Finding{finding}
	}
	for _, argv := range segments {
		for index, arg := range argv {
			if index == 0 && !isPathLike(arg) {
				continue
			}
			for _, candidate := range pathCandidates(argv, index, arg) {
				if finding, ok := deniedPathFinding(policy.DeniedPaths, candidate); ok {
					return []Finding{finding}
				}
			}
		}
	}
	return nil
}

func pathCandidates(argv []string, index int, value string) []string {
	candidates := []string{value}
	if filePath, ok := fileURLPath(value); ok {
		candidates = append(candidates, filePath)
	}
	if index == 0 || len(argv) == 0 {
		return candidates
	}
	if attached, ok := attachedPathOption(commandBase(argv[0]), value); ok {
		candidates = append(candidates, attached)
	}
	return candidates
}

// fileURLPath converts a file URL into the decoded filesystem path evaluated
// by path policy. Non-local authorities remain UNC-style absolute paths.
func fileURLPath(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !strings.EqualFold(parsed.Scheme, "file") {
		return "", false
	}
	decoded, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil || decoded == "" {
		return "/", true
	}
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		return "//" + parsed.Host + decoded, true
	}
	return decoded, true
}

func attachedPathOption(base, arg string) (string, bool) {
	for _, option := range []string{
		"--config=", "--identity-file=", "--output=", "--output-document=",
	} {
		if strings.HasPrefix(arg, option) && len(arg) > len(option) {
			return strings.TrimPrefix(arg, option), true
		}
	}
	shortOptions := map[string][]string{
		"curl": {"-K", "-o"},
		"wget": {"-O"},
		"ssh":  {"-F", "-i"},
		"scp":  {"-F", "-i"},
		"sftp": {"-F", "-i"},
	}
	for _, option := range shortOptions[base] {
		if strings.HasPrefix(arg, option) && len(arg) > len(option) {
			return strings.TrimPrefix(arg, option), true
		}
	}
	return "", false
}

func isPathLike(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), "\"'")
	value = strings.ReplaceAll(value, "\\", "/")
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") ||
		strings.HasPrefix(value, "../") || strings.HasPrefix(value, "~/") ||
		strings.Contains(value, "/")
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
	for _, component := range strings.Split(candidate, "/") {
		if component == denied {
			return true
		}
	}
	return false
}
