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

func (s *Scanner) scanCwd(cwd string) []Finding {
	clean := normalizePathToken(cwd)
	if clean == "" || !isForbiddenPath(clean, s.policy.ForbiddenPaths) {
		return nil
	}
	return []Finding{finding(
		RuleForbiddenPath, CategoryDangerousCommand, RiskCritical, DecisionDeny,
		"forbidden working directory: "+clean,
		"cwd",
		"Use a working directory outside forbidden system and credential paths.",
	)}
}

func (s *Scanner) scanForbiddenPathsInCwd(args []string, cwd, loc string) []Finding {
	var findings []Finding
	for _, arg := range args {
		clean := normalizePathToken(arg)
		if clean == "" {
			continue
		}
		candidates := []string{clean}
		if resolved := resolvePathAgainstCwd(clean, cwd); resolved != "" && resolved != clean {
			candidates = append(candidates, resolved)
		}
		for _, candidate := range candidates {
			if !isForbiddenPath(candidate, s.policy.ForbiddenPaths) {
				continue
			}
			findings = append(findings, finding(
				RuleForbiddenPath, CategoryDangerousCommand, RiskCritical, DecisionDeny,
				"forbidden path referenced: "+candidate,
				loc,
				"Do not access credentials, private keys, system paths, or workspace escape paths.",
			))
			break
		}
	}
	return findings
}

func resolvePathAgainstCwd(path, cwd string) string {
	cleanCwd := normalizePathToken(cwd)
	if cleanCwd == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.Join(cleanCwd, path)))
}

func normalizePathToken(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	if strings.HasPrefix(s, "-") {
		_, value, ok := strings.Cut(s, "=")
		if !ok {
			return ""
		}
		s = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if s == "" || strings.HasPrefix(s, "-") || strings.Contains(s, "://") {
		return ""
	}
	if strings.ContainsAny(s, `/\`) || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "~") {
		return filepath.ToSlash(filepath.Clean(s))
	}
	if filepath.Ext(s) == "" && !isKnownSensitiveBareFilename(s) {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(s))
}

func isKnownSensitiveBareFilename(s string) bool {
	switch strings.ToLower(s) {
	case "credentials", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519":
		return true
	default:
		return false
	}
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
