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
	"fmt"
	"strings"
)

// envChecker validates environment variables passed to tool execution
// against the policy's allow/deny lists and value patterns.
type envChecker struct {
	policy *Policy
}

func (c *envChecker) Name() string { return "env" }

func (c *envChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	p := &c.policy.Env
	if len(p.AllowedKeys) == 0 && len(p.DeniedKeys) == 0 && len(p.DenyValues) == 0 {
		return nil, nil
	}
	if len(req.Env) == 0 {
		return nil, nil
	}

	// Check for denied keys first.
	for key := range req.Env {
		for _, denied := range p.DeniedKeys {
			if equalOrSuffixMatch(key, denied) {
				return &CheckResult{
					Decision:       DecisionDeny,
					RiskLevel:      RiskHigh,
					RuleID:         "ENV_DENIED_KEY",
					Evidence:       fmt.Sprintf("Environment variable %s is denied", key),
					Recommendation: fmt.Sprintf("Environment variable %s is forbidden by the safety policy.", key),
				}, nil
			}
		}
	}

	// Check for keys not in whitelist.
	if len(p.AllowedKeys) > 0 {
		for key := range req.Env {
			allowed := false
			for _, ak := range p.AllowedKeys {
				if equalOrSuffixMatch(key, ak) {
					allowed = true
					break
				}
			}
			if !allowed {
				return &CheckResult{
					Decision:       DecisionDeny,
					RiskLevel:      RiskHigh,
					RuleID:         "ENV_NOT_ALLOWED",
					Evidence:       fmt.Sprintf("Environment variable %s is not in the whitelist", key),
					Recommendation: fmt.Sprintf("Add %s to env.allowed_keys in the safety policy.", key),
				}, nil
			}
		}
	}

	// Check for denied value patterns using pre-compiled regexps.
	denyPatterns := c.policy.EnvDenyValueRegexps()
	if len(denyPatterns) > 0 {
		for key, val := range req.Env {
			for _, re := range denyPatterns {
				if re.MatchString(val) {
					return &CheckResult{
						Decision:       DecisionDeny,
						RiskLevel:      RiskCritical,
						RuleID:         "ENV_DENIED_VALUE",
						Evidence:       fmt.Sprintf("Environment variable %s matched denied pattern", key),
						Recommendation: "Environment variable value matches a forbidden pattern. Use a secret manager or the tool's env injection mechanism.",
					}, nil
				}
			}
		}
	}

	return nil, nil
}

// equalOrSuffixMatch returns true if key matches entry (exact or wildcard).
//
// Two wildcard forms are supported:
//   - "*_TOKEN" matches any key ending with "_TOKEN" (prefix wildcard)
//   - "TOKEN_*" matches any key starting with "TOKEN_" (suffix wildcard)
//
// Matching is case-insensitive.
func equalOrSuffixMatch(key, entry string) bool {
	key = strings.ToUpper(key)
	entry = strings.ToUpper(entry)
	if key == entry {
		return true
	}
	// Prefix wildcard: "*_TOKEN" → match any key ending with "_TOKEN".
	if strings.HasPrefix(entry, "*") {
		return strings.HasSuffix(key, entry[1:])
	}
	// Suffix wildcard: "TOKEN_*" → match any key starting with "TOKEN_".
	if strings.HasSuffix(entry, "*") {
		return strings.HasPrefix(key, entry[:len(entry)-1])
	}
	return false
}
