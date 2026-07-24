//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// ruleDependency evaluates package-installation rules. Package-manager
// installation is RiskHigh and the default action is DecisionAsk so the
// operator approves environment changes before they happen.
//
// Rule ids:
//
//   - dependency.package_install   go/npm/pip/apt/yum/brew/cargo install.
func ruleDependency(a *analysis, p Policy) []Finding {
	if !p.Rules.Dependencies.Enabled {
		return nil
	}
	if !a.InstallPackages {
		return nil
	}
	return []Finding{{
		RuleID:         "dependency.package_install",
		RiskLevel:      RiskHigh,
		Decision:       ruleDecision(p.Rules.Dependencies.Action, RiskHigh, p),
		Evidence:       "package manager install command detected",
		Recommendation: "Approve the dependency change explicitly; pin versions and verify provenance",
	}}
}
