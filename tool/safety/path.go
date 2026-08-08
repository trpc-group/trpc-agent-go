//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
				if finding, ok := deniedPathFindingFromCwd(
					policy.DeniedPaths, cwd, candidate,
				); ok {
					return []Finding{finding}
				}
			}
		}
	}
	return nil
}

func pathCandidates(argv []string, index int, value string) []string {
	base := ""
	if len(argv) > 0 {
		base = commandBase(argv[0])
	}
	if isNonPathOptionValue(base, argv, index) {
		var semanticCandidates []string
		semanticCandidates = append(
			semanticCandidates,
			optionFileReferences(base, argv[index-1], value)...,
		)
		if base == "curl" && strings.EqualFold(argv[index-1], "--url") {
			if filePath, ok := fileURLPath(value); ok {
				semanticCandidates = append(semanticCandidates, filePath)
			}
		}
		return semanticCandidates
	}
	var semanticCandidates []string
	if filePath, ok := fileURLPath(value); ok {
		semanticCandidates = append(semanticCandidates, filePath)
	}
	candidates := append([]string{value}, semanticCandidates...)
	if index == 0 || len(argv) == 0 {
		return candidates
	}
	candidates = append(candidates, attachedPathOptions(base, value)...)
	return candidates
}

func isNonPathOptionValue(base string, argv []string, index int) bool {
	if index <= 0 || index >= len(argv) {
		return false
	}
	for prior := 1; prior < index; prior++ {
		if argv[prior] == "--" {
			return false
		}
	}
	option := argv[index-1]
	if strings.Contains(option, "=") {
		return false
	}
	return isNonPathOption(base, option)
}

func isNonPathOption(base, option string) bool {
	option = normalizedOption(option)
	switch option {
	case "--filter", "--match", "--pattern", "--query", "--regex",
		"--regexp", "--expression", "--run", "-run", "-skip", "-bench":
		return true
	}
	switch base {
	case "curl":
		switch option {
		case "-H", "--header", "-d", "--data", "--data-ascii",
			"--data-binary", "--data-raw", "--data-urlencode", "--json",
			"--expand-variable", "-F", "--form", "--form-string",
			"--proxy-header",
			"--url-query", "--variable", "-X", "-x", "--request",
			"--user-agent", "--url":
			return true
		}
	case "wget":
		switch option {
		case "--header", "--post-data", "--method", "--user-agent":
			return true
		}
	case "rg", "ripgrep", "grep", "egrep", "fgrep":
		switch option {
		case "-e", "-g", "--glob", "--iglob", "--replace", "-r":
			return true
		}
	case "find":
		switch option {
		case "-name", "-iname", "-path", "-ipath", "-regex", "-iregex":
			return true
		}
	}
	return false
}

func normalizedOption(option string) string {
	option = strings.TrimSpace(option)
	if strings.HasPrefix(option, "--") || len(option) > 2 {
		return strings.ToLower(option)
	}
	return option
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

func attachedPathOptions(base, arg string) []string {
	if option, value, ok := strings.Cut(arg, "="); ok && value != "" &&
		strings.HasPrefix(option, "--") {
		if references := optionFileReferences(base, option, value); len(references) > 0 {
			return references
		}
		if base == "curl" && strings.EqualFold(option, "--url") {
			if filePath, ok := fileURLPath(value); ok {
				return []string{filePath}
			}
		}
		if isNonPathOption(base, option) {
			return nil
		}
		if isLongPathOption(option) || isPathLike(value) {
			return []string{value}
		}
	}
	if base == "curl" {
		for _, option := range []string{"-H", "-d", "-F"} {
			if !strings.HasPrefix(arg, option) || len(arg) <= len(option) {
				continue
			}
			if references := optionFileReferences(
				base, option, strings.TrimPrefix(arg, option),
			); len(references) > 0 {
				return references
			}
		}
	}
	shortOptions := map[string][]string{
		"cp":      {"-t"},
		"curl":    {"-K", "-o", "-T"},
		"install": {"-t"},
		"mv":      {"-t"},
		"scp":     {"-F", "-i"},
		"sftp":    {"-F", "-i"},
		"ssh":     {"-F", "-i"},
		"tar":     {"-C", "-f"},
		"wget":    {"-O", "-P"},
	}
	for _, option := range shortOptions[base] {
		if strings.HasPrefix(arg, option) && len(arg) > len(option) {
			return []string{strings.TrimPrefix(arg, option)}
		}
	}
	return nil
}

func optionFileReferences(base, option, value string) []string {
	if base != "curl" {
		return nil
	}
	switch normalizedOption(option) {
	case "-H", "--header", "--proxy-header", "-d", "--data",
		"--data-ascii", "--data-binary", "--json":
		return leadingAtFileReference(value)
	case "--data-urlencode":
		return namedAtFileReference(value)
	case "--url-query":
		if strings.HasPrefix(value, "+") {
			return nil
		}
		return namedAtFileReference(value)
	case "--expand-variable", "--variable":
		if strings.HasPrefix(value, "%") {
			return nil
		}
		return namedAtFileReference(value)
	case "-F", "--form":
		return formFileReferences(value)
	default:
		return nil
	}
}

func leadingAtFileReference(value string) []string {
	if !strings.HasPrefix(value, "@") || len(value) == 1 {
		return nil
	}
	return []string{strings.TrimPrefix(value, "@")}
}

func namedAtFileReference(value string) []string {
	at := strings.Index(value, "@")
	if at < 0 || at+1 >= len(value) {
		return nil
	}
	if equals := strings.Index(value, "="); equals >= 0 && equals < at {
		return nil
	}
	return []string{value[at+1:]}
}

func formFileReferences(value string) []string {
	equals := strings.Index(value, "=")
	if equals < 0 || equals+1 >= len(value) {
		return nil
	}
	var references []string
	for _, part := range strings.Split(value[equals+1:], ",") {
		part = strings.TrimSpace(part)
		if len(part) < 2 || (part[0] != '@' && part[0] != '<') {
			continue
		}
		path := part[1:]
		if metadata := strings.Index(path, ";"); metadata >= 0 {
			path = path[:metadata]
		}
		path = strings.Trim(strings.TrimSpace(path), "\"")
		if path != "" {
			references = append(references, path)
		}
	}
	return references
}

func isLongPathOption(option string) bool {
	switch strings.ToLower(option) {
	case "--config", "--directory", "--directory-prefix", "--file",
		"--identity-file", "--output", "--output-directory",
		"--output-document", "--output-dir", "--target-directory",
		"--upload-file", "--work-tree", "--git-dir":
		return true
	default:
		return false
	}
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
	if len(deniedPaths) > 0 &&
		(candidate == ".." || strings.HasPrefix(candidate, "../")) {
		return newFinding(
			DecisionDeny, RiskHigh, "sensitive.path",
			"parent traversal may escape into denied_paths",
			"use a workspace-relative path without parent traversal",
		), true
	}
	return matchDeniedPathFinding(deniedPaths, candidate)
}

func deniedPathFindingFromCwd(
	deniedPaths []string,
	cwd string,
	value string,
) (Finding, bool) {
	candidate := normalizePath(value)
	if candidate == "" || (candidate != ".." && !strings.HasPrefix(candidate, "../")) {
		return deniedPathFinding(deniedPaths, value)
	}
	base := normalizePath(cwd)
	if base == "" || base == ".." || strings.HasPrefix(base, "../") {
		return deniedPathFinding(deniedPaths, value)
	}
	resolved := path.Clean(path.Join(base, candidate))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return deniedPathFinding(deniedPaths, value)
	}
	return matchDeniedPathFinding(deniedPaths, resolved)
}

func matchDeniedPathFinding(
	deniedPaths []string,
	candidate string,
) (Finding, bool) {
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
