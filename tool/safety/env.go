// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import "strings"

func (s *Scanner) scanEnv(env map[string]string) []Finding {
	if len(env) == 0 {
		return nil
	}
	var findings []Finding
	for key, val := range env {
		if len(s.policy.EnvAllowlist) > 0 && !envAllowed(key, s.policy.EnvAllowlist) {
			findings = append(findings, finding(
				RuleEnvNotAllowed, CategoryPolicy, RiskMedium, DecisionAsk,
				"environment variable is not allowlisted: "+key,
				"env."+key,
				"Remove the variable or add its name to env_allowlist after review.",
			))
		}
		if hasSecret(key + "=" + val) {
			findings = append(findings, finding(
				RuleSecretLeak, CategorySecretLeak, RiskHigh, DecisionDeny,
				key+"="+val,
				"env."+key,
				"Do not pass secrets through tool execution environment overrides.",
			))
		}
	}
	return findings
}

func envAllowed(key string, allow []string) bool {
	for _, item := range allow {
		if strings.EqualFold(strings.TrimSpace(item), key) {
			return true
		}
	}
	return false
}
