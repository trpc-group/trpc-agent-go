//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "sort"

// Category groups findings by the risk type they belong to. The values match
// the seven risk types called out by the safety guard design.
const (
	CategoryDangerousCommand = "dangerous_command"
	CategoryNetworkExfil     = "network_exfil"
	CategoryShellBypass      = "shell_bypass"
	CategoryHostExecRisk     = "host_exec_risk"
	CategoryDependencyChange = "dependency_change"
	CategoryResourceAbuse    = "resource_abuse"
	CategorySensitiveLeak    = "sensitive_leak"
)

// Finding is a single rule hit produced by the scanner.
type Finding struct {
	// RuleID is the stable identifier of the rule that fired.
	RuleID string `json:"rule_id"`
	// Category is one of the Category* values above.
	Category string `json:"category"`
	// RiskLevel is the severity of this finding.
	RiskLevel RiskLevel `json:"risk_level"`
	// Decision is the action this finding recommends on its own.
	Decision Decision `json:"decision"`
	// Evidence is the matched fragment, already redacted of secrets.
	Evidence string `json:"evidence"`
	// Recommendation is a short, human-readable remediation hint.
	Recommendation string `json:"recommendation"`
	// Segment is the pipeline segment or script line that matched, if known.
	Segment string `json:"segment,omitempty"`
}

// ScanReport is the structured result of scanning one ScanInput.
type ScanReport struct {
	// ToolName is the model-visible tool name that was scanned.
	ToolName string `json:"tool_name"`
	// Backend is the normalised execution surface.
	Backend Backend `json:"backend"`
	// Command is the scanned command, already redacted of secrets.
	Command string `json:"command"`
	// Decision is the aggregated verdict (most restrictive finding).
	Decision Decision `json:"decision"`
	// RiskLevel is the aggregated severity (most severe finding).
	RiskLevel RiskLevel `json:"risk_level"`
	// Blocked reports whether the decision prevents execution.
	Blocked bool `json:"blocked"`
	// Redacted reports whether any secret was redacted from the outputs.
	Redacted bool `json:"redacted"`
	// Findings lists every rule hit, most severe first.
	Findings []Finding `json:"findings"`
	// DurationMS is the wall-clock scan time in milliseconds.
	DurationMS int64 `json:"duration_ms"`
}

// primaryFinding returns the finding that drives a non-allow decision, or nil
// when nothing blocks. Findings are sorted most-restrictive-first, so the head
// is the driver iff it blocks; an allow-only report (e.g. a lone
// net.allowed_domain informational finding) returns nil so it does not stamp a
// rule id onto an allowed call's audit record.
func (r *ScanReport) primaryFinding() *Finding {
	if len(r.Findings) == 0 || !r.Findings[0].Decision.blocks() {
		return nil
	}
	return &r.Findings[0]
}

// PrimaryRuleID returns the rule id of the primary finding, or "" when none.
func (r *ScanReport) PrimaryRuleID() string {
	if f := r.primaryFinding(); f != nil {
		return f.RuleID
	}
	return ""
}

// Reason renders a short, human-readable reason for a non-allow decision,
// suitable for a PermissionDecision.Reason. It is empty when the report allows.
func (r *ScanReport) Reason() string {
	f := r.primaryFinding()
	if f == nil || !r.Decision.blocks() {
		return ""
	}
	reason := f.RuleID + ": " + f.Recommendation
	return reason
}

// aggregate sorts findings most-severe-first and fills the report-level
// Decision, RiskLevel and Blocked fields from the findings. An empty findings
// list yields an allow / none report.
func (r *ScanReport) aggregate() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		fi, fj := r.Findings[i], r.Findings[j]
		if dr := decisionRank(fj.Decision) - decisionRank(fi.Decision); dr != 0 {
			return dr < 0
		}
		return riskRank(fj.RiskLevel) < riskRank(fi.RiskLevel)
	})
	r.Decision = DecisionAllow
	r.RiskLevel = RiskNone
	for i := range r.Findings {
		r.Decision = maxDecision(r.Decision, r.Findings[i].Decision)
		r.RiskLevel = maxRisk(r.RiskLevel, r.Findings[i].RiskLevel)
	}
	r.Blocked = r.Decision.blocks()
}
