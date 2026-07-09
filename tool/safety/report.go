// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

// Telemetry attribute names emitted for safety decisions.
const (
	AttrDecision  = "tool.safety.decision"
	AttrRiskLevel = "tool.safety.risk_level"
	AttrRuleID    = "tool.safety.rule_id"
	AttrBackend   = "tool.safety.backend"
	AttrBlocked   = "tool.safety.blocked"
	AttrRedacted  = "tool.safety.redacted"
)

func newReport(req ExecutionRequest, command string, findings []Finding, durMS float64, redactor *Redactor) Report {
	decision := DecisionAllow
	risk := RiskNone
	ruleIDs := make([]string, 0, len(findings))
	recommendation := "Execution request passed safety policy."
	redactedAny := false
	for i := range findings {
		f := &findings[i]
		f.Action = normalizeAction(f.Action)
		decision = stricterDecision(decision, f.Action)
		risk = higherRisk(risk, f.RiskLevel)
		ruleIDs = append(ruleIDs, f.RuleID)
		if f.Recommendation != "" && blocked(f.Action) {
			recommendation = f.Recommendation
		}
		if redactor != nil {
			if s, ok := redactor.Redact(f.Evidence); ok {
				f.Evidence = s
				f.Redacted = true
				redactedAny = true
			}
			if s, ok := redactor.Redact(f.Recommendation); ok {
				f.Recommendation = s
				f.Redacted = true
				redactedAny = true
			}
		}
	}
	if len(findings) == 0 {
		ruleIDs = []string{RuleAllowSafeCommand}
	}
	if decision == DecisionAllow {
		risk = RiskNone
	}
	if command != "" && redactor != nil {
		if s, ok := redactor.Redact(command); ok {
			command = s
			redactedAny = true
		}
	}
	primary := primaryFindingRuleID(findings, ruleIDs)
	ruleIDs = moveRuleFirst(ruleIDs, primary)
	return Report{
		SchemaVersion:  "1",
		RequestID:      req.ID,
		ToolName:       nonEmpty(req.ToolName, "unknown"),
		Backend:        normalizeBackend(req.Backend),
		Command:        command,
		Decision:       decision,
		RiskLevel:      risk,
		Blocked:        blocked(decision),
		DurationMS:     durMS,
		RuleIDs:        ruleIDs,
		Findings:       findings,
		Recommendation: recommendation,
		Redacted:       redactedAny,
		TelemetryAttributes: map[string]any{
			AttrDecision:  string(decision),
			AttrRiskLevel: string(risk),
			AttrRuleID:    primary,
			AttrBackend:   string(normalizeBackend(req.Backend)),
			AttrBlocked:   blocked(decision),
			AttrRedacted:  redactedAny,
		},
	}
}

func moveRuleFirst(ids []string, primary string) []string {
	if primary == "" || len(ids) == 0 || ids[0] == primary {
		return ids
	}
	out := make([]string, 0, len(ids))
	out = append(out, primary)
	for _, id := range ids {
		if id == primary && len(out) == 1 {
			continue
		}
		out = append(out, id)
	}
	return out
}

func normalizeAction(d Decision) Decision {
	if d == "" {
		return DecisionAsk
	}
	return d
}

func primaryFindingRuleID(findings []Finding, fallback []string) string {
	if len(findings) == 0 {
		return primaryRuleID(fallback)
	}
	best := findings[0]
	for _, f := range findings[1:] {
		if decisionRank(f.Action) > decisionRank(best.Action) ||
			(decisionRank(f.Action) == decisionRank(best.Action) &&
				riskRank(f.RiskLevel) > riskRank(best.RiskLevel)) {
			best = f
		}
	}
	if best.RuleID == "" {
		return primaryRuleID(fallback)
	}
	return best.RuleID
}

func primaryRuleID(ids []string) string {
	if len(ids) == 0 {
		return RuleAllowSafeCommand
	}
	return ids[0]
}

func normalizeBackend(b Backend) Backend {
	switch b {
	case BackendWorkspaceExec, BackendHostExec, BackendCodeExec, BackendMCP, BackendSkill:
		return b
	default:
		return BackendUnknown
	}
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
