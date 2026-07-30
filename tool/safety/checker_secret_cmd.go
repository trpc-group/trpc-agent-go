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

// secretCmdChecker scans the command line itself for hard-coded secrets
// (API keys, tokens, passwords). This is the pre-execution check;
// checker_secret_output.go handles post-execution output desensitization.
type secretCmdChecker struct {
	policy *Policy
}

func (c *secretCmdChecker) Name() string { return "secret_cmd" }

func (c *secretCmdChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	patterns := c.policy.SecretRegexps()
	if len(patterns) == 0 {
		return nil, nil
	}

	text := req.Command
	if len(req.Args) > 0 {
		text += " " + strings.Join(req.Args, " ")
	}

	for _, re := range patterns {
		match := re.FindString(text)
		if match != "" {
			// Desensitize the match for the evidence string.
			masked := maskSecret(match)
			return &CheckResult{
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "SECRET_IN_COMMAND",
				Evidence:       fmt.Sprintf("Secret detected in command: %s", masked),
				Recommendation: "Never hard-code secrets in command arguments. Use the tool's env parameter or a secret manager to inject credentials at runtime.",
			}, nil
		}
	}

	return nil, nil
}

// maskSecret replaces the middle portion of a detected secret with "***"
// so the audit log does not record the raw secret value.
func maskSecret(s string) string {
	if len(s) <= 8 {
		return s[:min(len(s), 3)] + "***"
	}
	// Keep a small fixed prefix/suffix to aid debugging without exposing the secret.
	const keep = 4
	return s[:keep] + "***" + s[len(s)-keep:]
}
