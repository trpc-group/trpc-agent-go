//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

// ruleMetadata evaluates tool-published metadata (Destructive, OpenWorld,
// ConcurrencySafe, ReadOnly) and produces findings when the metadata
// indicates a risk the policy should review. This makes the guard honor
// PermissionRequest.Metadata, not just the decoded arguments.
//
// Rule ids:
//
//   - metadata.destructive       tool declares Destructive=true.
//   - metadata.open_world        tool declares OpenWorld=true.
func ruleMetadata(in ScanInput, p Policy) []Finding {
	var out []Finding
	// Destructive tools: ask by default so the operator can approve
	// irreversible changes. The policy's DangerousCommands action
	// overrides this.
	if in.Metadata.Destructive {
		risk := RiskMedium
		action := p.Rules.DangerousCommands.Action
		// If the action is deny, use deny; otherwise ask (the operator
		// should approve destructive tools).
		decision := ruleDecision(action, risk, p)
		if decision == DecisionAllow {
			// Destructive tools should never be silently allowed.
			decision = DecisionAsk
		}
		out = append(out, Finding{
			RuleID:         "metadata.destructive",
			RiskLevel:      risk,
			Decision:       decision,
			Evidence:       "tool metadata declares Destructive=true",
			Recommendation: "Approve the destructive operation explicitly; verify the tool's scope",
		})
	}
	// OpenWorld tools: ask by default so the operator can review
	// external access. The policy's Network action overrides this.
	if in.Metadata.OpenWorld && !in.Metadata.SearchOrRead {
		risk := RiskMedium
		action := p.Rules.Network.Action
		decision := ruleDecision(action, risk, p)
		if decision == DecisionAllow {
			// OpenWorld tools that are not read-only should not be
			// silently allowed.
			decision = DecisionAsk
		}
		out = append(out, Finding{
			RuleID:         "metadata.open_world",
			RiskLevel:      risk,
			Decision:       decision,
			Evidence:       "tool metadata declares OpenWorld=true (non-read-only)",
			Recommendation: "Review the external access; allow only known-safe endpoints",
		})
	}
	return out
}
