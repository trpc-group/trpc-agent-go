//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// pathChecker validates that the command and its arguments do not
// reference sensitive paths (SSH keys, credentials, env files, etc.).
// Paths in the policy are glob patterns; ~ is expanded to $HOME.
type pathChecker struct {
	policy *Policy
}

func (c *pathChecker) Name() string { return "path" }

func (c *pathChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	if len(c.policy.Paths.Denied) == 0 {
		return nil, nil
	}

	// Build the full command line text to scan for path references.
	text := req.Command
	if len(req.Args) > 0 {
		text += " " + strings.Join(req.Args, " ")
	}

	for _, pattern := range c.policy.Paths.Denied {
		expanded := expandTilde(pattern)
		// Try direct path match.
		if matchPath(text, expanded) {
			return c.result(pattern, expanded)
		}
	}

	return nil, nil
}

func (c *pathChecker) result(pattern, path string) (*CheckResult, error) {
	switch {
	case strings.Contains(pattern, ".ssh") || strings.Contains(pattern, "id_rsa") || strings.Contains(pattern, ".pem"):
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskCritical,
			RuleID:         "PATH_SENSITIVE_SSH",
			Evidence:       pattern + " -> " + path,
			Recommendation: "Access to SSH keys and credentials is forbidden. Use a dedicated secret management tool.",
		}, nil
	case strings.Contains(pattern, ".env") || strings.Contains(pattern, "credentials"):
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskCritical,
			RuleID:         "PATH_SENSITIVE_ENV",
			Evidence:       pattern + " -> " + path,
			Recommendation: "Access to environment files and credentials is forbidden. Inject secrets via the tool's env parameter.",
		}, nil
	case strings.Contains(pattern, ".git/config"):
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskHigh,
			RuleID:         "PATH_SENSITIVE_CRED",
			Evidence:       pattern + " -> " + path,
			Recommendation: "Access to git configuration may leak credentials.",
		}, nil
	default:
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskHigh,
			RuleID:         "PATH_DENIED",
			Evidence:       pattern + " -> " + path,
			Recommendation: "The referenced path is forbidden by the safety policy.",
		}, nil
	}
}

// expandTilde replaces a leading ~ with $HOME.
func expandTilde(s string) string {
	if strings.HasPrefix(s, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return s
		}
		return filepath.Join(home, s[1:])
	}
	return s
}

// matchPath checks whether text contains a reference to the given path.
// It handles both literal path references and glob-style patterns
// (double-star ** matching any directory depth).
func matchPath(text, path string) bool {
	text = filepath.ToSlash(text)
	path = filepath.ToSlash(path)

	// Direct substring match (cat ~/.ssh/id_rsa, ls /etc/shadow, etc.)
	if containsNormalized(text, path) {
		return true
	}

	// Handle **/foo patterns: match foo anywhere in the text.
	if strings.HasPrefix(path, "**/") {
		suffix := path[3:]
		if containsNormalized(text, suffix) {
			return true
		}
	}

	// Try filepath.Match for simple globs.
	if matched, _ := filepath.Match(path, text); matched {
		return true
	}

	return false
}

// containsNormalized lower-cases both strings before checking.
// On Windows, case folding is required since the OS doesn't
// distinguish. On Linux/macOS, we fold anyway as defence-in-depth
// against an attacker using mixed case to evade path checks.
func containsNormalized(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
