//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

const maxDiffLines = 5000

var ignoredErrRE = regexp.MustCompile(`^\+.*_\s*=\s*err`)

// runChecks validates diff content (mirrors scripts/run_checks.sh).
func runChecks(diff string) (stdout, stderr string, exitCode int) {
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return "", "error: empty diff", 2
	}
	scanner := bufio.NewScanner(strings.NewReader(diff))
	lines := 0
	hasGitHeader := false
	hasMinusHeader := false
	hasPlusHeader := false
	hasHunk := false
	for scanner.Scan() {
		lines++
		if lines > maxDiffLines {
			return "", fmt.Sprintf("error: diff exceeds line limit (%d > %d)", lines, maxDiffLines), 2
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			hasGitHeader = true
		}
		if strings.HasPrefix(line, "--- ") {
			hasMinusHeader = true
		}
		if strings.HasPrefix(line, "+++ ") {
			hasPlusHeader = true
		}
		if strings.HasPrefix(line, "@@ ") {
			hasHunk = true
		}
		if ignoredErrRE.MatchString(line) {
			return "", "sandbox check: ignored error pattern in diff", 2
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err.Error(), 2
	}
	if !hasGitHeader && !(hasMinusHeader && hasPlusHeader && hasHunk) {
		return "", "error: not a unified diff", 2
	}
	return fmt.Sprintf("sandbox checks passed (%d diff lines)", lines), "", 0
}
