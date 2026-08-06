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

// pathChecker validates that the command, its arguments, and the working
// directory do not reference sensitive paths (SSH keys, credentials, env
// files, etc.). Paths in the policy are glob patterns; ~ is expanded to $HOME.
type pathChecker struct {
	policy *Policy
}

func (c *pathChecker) Name() string { return "path" }

func (c *pathChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	if len(c.policy.Paths.Denied) == 0 {
		return nil, nil
	}

	// Build the full text to scan for path references. Include Cwd so
	// that denied working directories (e.g. cwd="~/.ssh/") are caught
	// even when the command itself does not mention them.
	text := req.Command
	if req.Cwd != "" {
		text += " " + req.Cwd
	}
	if len(req.Args) > 0 {
		text += " " + strings.Join(req.Args, " ")
	}

	for _, pattern := range c.policy.Paths.Denied {
		expanded := expandTilde(pattern)
		if matchPath(text, expanded) || matchPath(text, pattern) {
			return c.result(pattern, expanded)
		}
	}

	return nil, nil
}

func (c *pathChecker) result(pattern, path string) (*CheckResult, error) {
	rule := classifyPathRule(pattern)
	switch rule {
	case "PATH_SENSITIVE_SSH":
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskCritical,
			RuleID:         rule,
			Evidence:       pattern + " -> " + path,
			Recommendation: "Access to SSH keys and credentials is forbidden. Use a dedicated secret management tool.",
		}, nil
	case "PATH_SENSITIVE_ENV":
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskCritical,
			RuleID:         rule,
			Evidence:       pattern + " -> " + path,
			Recommendation: "Access to environment files and credentials is forbidden. Inject secrets via the tool's env parameter.",
		}, nil
	case "PATH_SENSITIVE_CRED":
		return &CheckResult{
			Decision:       DecisionDeny,
			RiskLevel:      RiskHigh,
			RuleID:         rule,
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

// classifyPathRule maps a denied-path glob pattern to a structured RuleID.
// It matches against the policy pattern (not the scanned text), so it is
// immune to adversarial crafting of command-line arguments.
func classifyPathRule(pattern string) string {
	// Normalise to forward slashes for consistent matching.
	p := filepath.ToSlash(pattern)
	base := filepath.Base(p)

	// SSH-related: .ssh directory, id_rsa, *.pem key files.
	if base == ".ssh" || strings.HasSuffix(p, "/.ssh") || base == "id_rsa" ||
		strings.HasPrefix(base, "id_") && strings.HasSuffix(base, ".pem") {
		return "PATH_SENSITIVE_SSH"
	}

	// Environment / credential files.
	if base == ".env" || strings.HasSuffix(p, "/.env") ||
		base == "credentials" || strings.HasSuffix(p, "/credentials") {
		return "PATH_SENSITIVE_ENV"
	}

	// Git config (may contain embedded credentials).
	if base == "config" && strings.Contains(p, ".git") {
		return "PATH_SENSITIVE_CRED"
	}

	// PEM key files (generic).
	if strings.HasSuffix(base, ".pem") {
		return "PATH_SENSITIVE_SSH"
	}

	return "PATH_DENIED"
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
func matchPath(text, path string) bool {
	text = filepath.ToSlash(text)
	path = filepath.ToSlash(path)

	if containsNormalized(text, path) {
		return true
	}

	// **/ patterns: Go's filepath.Match does not support **, so we
	// strip the **/ prefix and match the remainder against the base
	// name of every whitespace-separated token in the text.
	if strings.HasPrefix(path, "**/") {
		suffix := path[3:]
		for _, token := range strings.Fields(text) {
			token = strings.Trim(token, `"'`)
			if matched, _ := filepath.Match(suffix, token); matched {
				return true
			}
			// Also match against the leaf name so deep paths work:
			// "**/credentials" matches "/home/user/.config/credentials".
			if base := filepath.Base(token); base != "" && base != token {
				if matched, _ := filepath.Match(suffix, base); matched {
					return true
				}
			}
		}
	}

	if matched, _ := filepath.Match(path, text); matched {
		return true
	}

	return false
}

// containsNormalized lower-cases both strings before checking.
func containsNormalized(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
