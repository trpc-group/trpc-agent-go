//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"runtime"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/envscrub"
)

func (s *DefaultScanner) scanEnv(env map[string]string) []Finding {
	var findings []Finding
	for key, value := range env {
		if isProcessControlEnv(key) {
			findings = append(findings, Finding{
				RuleID:         "env.process_control",
				RiskLevel:      RiskHigh,
				Decision:       DecisionDeny,
				Evidence:       key,
				Recommendation: "remove process-control environment variables before execution",
			})
		}
		if len(s.policy.EnvAllowlist) > 0 && !s.envAllowed(key) {
			decision := DecisionAsk
			risk := RiskMedium
			if isProcessControlEnv(key) {
				risk = RiskHigh
			}
			findings = append(findings, Finding{
				RuleID:         "env.not_allowlisted",
				RiskLevel:      risk,
				Decision:       decision,
				Evidence:       key,
				Recommendation: "add the variable to env_allowlist or remove it from the tool environment",
			})
		}
		if looksSecretName(key) || containsSecret(value) {
			findings = append(findings, Finding{
				RuleID:         "secret.env_value",
				RiskLevel:      RiskCritical,
				Decision:       s.policy.SecretAction,
				Evidence:       key + "=<redacted>",
				Recommendation: "remove secrets from tool environment variables",
				Redacted:       true,
			})
		}
		findings = append(findings, s.scanProxyEnv(key, value)...)
	}
	return findings
}

func (s *DefaultScanner) envAllowed(key string) bool {
	for _, allowed := range s.policy.EnvAllowlist {
		if key == allowed || strings.EqualFold(key, allowed) {
			return true
		}
	}
	return false
}

func isProcessControlEnv(key string) bool {
	if envscrub.IsMalformedKey(key) ||
		envscrub.IsBlocked(key, runtime.GOOS == "windows") {
		return true
	}
	key = strings.ToUpper(strings.TrimSpace(key))
	if strings.HasPrefix(key, "LD_") {
		return true
	}
	switch key {
	case "BASH_ENV", "ENV", "CDPATH", "GLOBIGNORE", "PROMPT_COMMAND",
		"PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP", "NODE_PATH", "NODE_OPTIONS",
		"RUBYLIB", "RUBYOPT", "PERL5LIB", "PERL5OPT", "DYLD_INSERT_LIBRARIES",
		"DYLD_LIBRARY_PATH", "DYLD_FRAMEWORK_PATH", "DYLD_FALLBACK_LIBRARY_PATH",
		"PATHEXT":
		return true
	default:
		return false
	}
}
