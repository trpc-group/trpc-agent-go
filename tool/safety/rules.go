// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

const (
	RuleAllowSafeCommand     = "TSG-ALLOW-SAFE-COMMAND"
	RuleDangerousDelete      = "TSG-DANGER-RM-RF"
	RuleDangerousOverwrite   = "TSG-DANGER-OVERWRITE-SYSTEM"
	RuleForbiddenPath        = "TSG-DANGER-FORBIDDEN-PATH"
	RuleNetworkDeniedDomain  = "TSG-NETWORK-DOMAIN-DENIED"
	RuleNetworkAllowedDomain = "TSG-NETWORK-DOMAIN-ALLOWED"
	RuleShellParseUnsafe     = "TSG-SHELL-PARSE-UNSAFE"
	RuleShellWrapper         = "TSG-SHELL-WRAPPER"
	RuleShellBypassConstruct = "TSG-SHELL-BYPASS-CONSTRUCT"
	RuleHostPTY              = "TSG-HOSTEXEC-PTY"
	RuleHostBackground       = "TSG-HOSTEXEC-BACKGROUND"
	RuleHostPrivilege        = "TSG-HOSTEXEC-PRIVILEGE"
	RuleDependencyInstall    = "TSG-DEPENDENCY-INSTALL"
	RuleResourceTimeout      = "TSG-RESOURCE-TIMEOUT"
	RuleResourceOutput       = "TSG-RESOURCE-OUTPUT"
	RuleResourceLongRunning  = "TSG-RESOURCE-LONG-RUNNING"
	RuleResourceParallelism  = "TSG-RESOURCE-PARALLELISM"
	RuleSecretLeak           = "TSG-SECRET-LEAK"
	RuleEnvNotAllowed        = "TSG-POLICY-ENV-NOT-ALLOWED"
	RulePolicyInvalid        = "TSG-POLICY-INVALID"
	RuleHumanReview          = "TSG-HUMAN-REVIEW"
)

func stricterDecision(a, b Decision) Decision {
	if decisionRank(b) > decisionRank(a) {
		return b
	}
	return a
}

func decisionRank(d Decision) int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionAsk, DecisionNeedsHumanReview:
		return 2
	case DecisionAllow:
		return 0
	default:
		return 2
	}
}

func higherRisk(a, b RiskLevel) RiskLevel {
	if riskRank(b) > riskRank(a) {
		return b
	}
	return a
}

func riskRank(r RiskLevel) int {
	switch r {
	case RiskCritical:
		return 5
	case RiskHigh:
		return 4
	case RiskMedium:
		return 3
	case RiskLow:
		return 2
	case RiskNone:
		return 1
	default:
		return 3
	}
}

func blocked(d Decision) bool {
	return d == DecisionDeny ||
		d == DecisionAsk ||
		d == DecisionNeedsHumanReview
}
