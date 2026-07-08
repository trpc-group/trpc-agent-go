// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

func (s *Scanner) scanForbiddenPaths(args []string, loc string) []Finding {
	var findings []Finding
	for _, arg := range args {
		clean := normalizePathToken(arg)
		if clean == "" {
			continue
		}
		if isForbiddenPath(clean, s.policy.ForbiddenPaths) {
			findings = append(findings, finding(
				RuleForbiddenPath, CategoryDangerousCommand, RiskCritical, DecisionDeny,
				"forbidden path referenced: "+clean,
				loc,
				"Do not access credentials, private keys, system paths, or workspace escape paths.",
			))
		}
	}
	return findings
}

func normalizePathToken(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	if s == "" || strings.HasPrefix(s, "-") || strings.Contains(s, "://") {
		return ""
	}
	if strings.ContainsAny(s, `/\`) || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return filepath.ToSlash(filepath.Clean(s))
	}
	return ""
}

func isForbiddenPath(path string, patterns []string) bool {
	p := filepath.ToSlash(path)
	lp := strings.ToLower(p)
	if strings.Contains(lp, ".env") ||
		strings.Contains(lp, ".ssh") ||
		strings.Contains(lp, "credential") ||
		strings.Contains(lp, "private") && strings.Contains(lp, "key") {
		return true
	}
	if strings.HasPrefix(p, "../") || p == ".." {
		return true
	}
	for _, pat := range patterns {
		pat = filepath.ToSlash(strings.TrimSpace(pat))
		if pat == "" {
			continue
		}
		if runtime.GOOS == "windows" {
			pat = strings.ToLower(pat)
			lp = strings.ToLower(p)
		}
		if ok, _ := doublestar.PathMatch(pat, p); ok {
			return true
		}
		if ok, _ := doublestar.PathMatch(pat, lp); ok {
			return true
		}
		if strings.HasPrefix(pat, "/") && strings.HasPrefix(p, strings.TrimSuffix(pat, "**")) {
			return true
		}
	}
	return false
}
