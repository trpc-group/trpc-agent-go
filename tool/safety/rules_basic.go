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
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func scanSecrets(input ScanInput) ([]Finding, bool) {
	values := make([]string, 0, len(input.Arguments)+len(input.CodeBlocks)+1)
	values = append(values, input.Command)
	values = append(values, input.Arguments...)
	for _, block := range input.CodeBlocks {
		values = append(values, block.Code)
	}
	for _, value := range input.Environment {
		values = append(values, value)
	}
	values = append(values, input.extraValues...)
	for _, value := range values {
		if _, changed := Redact(value); changed {
			return []Finding{finding(
				DecisionDeny,
				RiskLevelCritical,
				RuleSecretExposure,
				"execution input contains credential or private-key material",
				"remove secrets from tool arguments and use an isolated credential provider",
			)}, true
		}
	}
	return nil, false
}

func (s *Scanner) scanEnvironment(environment map[string]string) []Finding {
	if len(environment) == 0 {
		return nil
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var findings []Finding
	for _, key := range keys {
		if allowedEnvironmentKey(key, s.policy.AllowedEnvVars) {
			continue
		}
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleEnvironment,
			fmt.Sprintf("environment override %q is not allowed", key),
			"remove the override or add its variable name to allowed_env_vars",
		))
	}
	return findings
}

func allowedEnvironmentKey(key string, allowed []string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, pattern := range allowed {
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(key, strings.TrimSuffix(pattern, "*")) {
				return true
			}
			continue
		}
		if key == pattern {
			return true
		}
	}
	return false
}

func (s *Scanner) scanLimits(input ScanInput) []Finding {
	var findings []Finding
	if input.TimeoutSeconds < 0 {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			"requested timeout must not be negative",
			"use zero for an unspecified timeout or provide a positive bounded value",
		))
	}
	if input.RequestedOutputBytes < 0 {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleInvalidInput,
			"requested output limit must not be negative",
			"use zero for an unspecified output limit or provide a positive bounded value",
		))
	}
	if s.policy.MaxTimeoutSeconds > 0 &&
		input.TimeoutSeconds > s.policy.MaxTimeoutSeconds {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleTimeoutLimit,
			fmt.Sprintf(
				"requested timeout %d seconds exceeds policy maximum %d seconds",
				input.TimeoutSeconds,
				s.policy.MaxTimeoutSeconds,
			),
			"reduce the requested timeout and rely on executor cleanup after cancellation",
		))
	}
	if s.policy.MaxOutputBytes > 0 && input.RequestedOutputBytes >
		s.policy.MaxOutputBytes {
		findings = append(findings, finding(
			DecisionDeny,
			RiskLevelHigh,
			RuleResourceAbuse,
			fmt.Sprintf(
				"requested output limit %d bytes exceeds policy maximum %d bytes",
				input.RequestedOutputBytes,
				s.policy.MaxOutputBytes,
			),
			"lower the output limit and configure the executor to enforce the same cap",
		))
	}
	if input.Backend == BackendHost && (input.Background || input.PTY) {
		if input.TimeoutSeconds <= 0 {
			findings = append(findings, finding(
				DecisionDeny,
				RiskLevelHigh,
				RuleTimeoutLimit,
				"host PTY or background execution requires an explicit timeout",
				"set timeout_sec within max_timeout_seconds before requesting a long session",
			))
		}
		findings = append(findings, finding(
			DecisionAsk,
			RiskLevelHigh,
			RuleHostSession,
			"host execution requests a PTY or background session",
			"obtain human approval and retain the session id for explicit cleanup",
		))
	}
	return findings
}

func scanOpaqueMetadata(metadata tool.ToolMetadata) []Finding {
	switch {
	case metadata.Destructive:
		return []Finding{finding(
			DecisionAsk,
			RiskLevelHigh,
			RuleToolMetadata,
			"tool metadata marks an opaque operation as destructive",
			"review the concrete operation and its rollback or recovery path",
		)}
	case metadata.OpenWorld:
		return []Finding{finding(
			DecisionAsk,
			RiskLevelHigh,
			RuleToolMetadata,
			"tool metadata marks an opaque operation as open-world",
			"review its external destinations and data handling before approval",
		)}
	default:
		return nil
	}
}
