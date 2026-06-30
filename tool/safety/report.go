//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Report is the structured scan result for one tool call. It is emitted as
// tool_safety_report.json and projected into an audit event.
type Report struct {
	ToolName   string    `json:"tool_name"`
	Backend    string    `json:"backend"`
	Command    string    `json:"command"`
	Decision   Decision  `json:"decision"`
	RiskLevel  RiskLevel `json:"risk_level"`
	Blocked    bool      `json:"blocked"`
	RuleIDs    []string  `json:"rule_ids"`
	Findings   []Finding `json:"findings"`
	Redacted   bool      `json:"redacted"`
	DurationUS int64     `json:"duration_us"`
	Timestamp  string    `json:"timestamp"`
}

// AuditEvent is the compact projection of a Report written to the audit log.
type AuditEvent struct {
	ToolName   string    `json:"tool_name"`
	Decision   Decision  `json:"decision"`
	RiskLevel  RiskLevel `json:"risk_level"`
	Backend    string    `json:"backend"`
	RuleIDs    []string  `json:"rule_ids"`
	Blocked    bool      `json:"blocked"`
	Redacted   bool      `json:"redacted"`
	DurationUS int64     `json:"duration_us"`
	Timestamp  string    `json:"timestamp"`
}

// buildReport assembles a Report from a scan result. Redaction is applied
// separately by redactReport before the report is emitted.
func buildReport(
	toolName, backend string,
	er ExecRequest,
	findings []Finding,
	decision Decision,
	risk RiskLevel,
	dur time.Duration,
) Report {
	if findings == nil {
		findings = []Finding{}
	}
	r := Report{
		ToolName:   toolName,
		Backend:    backend,
		Command:    er.Command,
		Decision:   decision,
		RiskLevel:  risk,
		Blocked:    decision != DecisionAllow,
		Findings:   findings,
		DurationUS: dur.Microseconds(),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	r.RuleIDs = r.ruleIDs()
	return r
}

// ruleIDs returns the distinct rule ids present in the report, in order.
func (r Report) ruleIDs() []string {
	seen := make(map[string]bool, len(r.Findings))
	ids := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		if seen[f.RuleID] {
			continue
		}
		seen[f.RuleID] = true
		ids = append(ids, f.RuleID)
	}
	return ids
}

// topFinding returns the finding that drove the decision: the strongest action
// first, then the highest risk.
func (r Report) topFinding() (Finding, bool) {
	var best Finding
	found := false
	bestAct, bestRisk := -1, -1
	for _, f := range r.Findings {
		act := actionRank(f.effectiveAction())
		risk := riskRank(f.RiskLevel)
		if act > bestAct || (act == bestAct && risk > bestRisk) {
			best, found = f, true
			bestAct, bestRisk = act, risk
		}
	}
	return best, found
}

// summary returns a human-readable reason for a non-allow decision, suitable
// for the PermissionDecision reason returned to the model.
func (r Report) summary() string {
	if r.Decision == DecisionAllow {
		return ""
	}
	f, ok := r.topFinding()
	if !ok {
		return string(r.Decision)
	}
	verb := "denied"
	if r.Decision == DecisionReview {
		verb = "needs human review"
	}
	return fmt.Sprintf("%s by %s: %s", verb, f.RuleID, f.Evidence)
}

// toAudit projects the report into a compact audit event.
func (r Report) toAudit() AuditEvent {
	return AuditEvent{
		ToolName:   r.ToolName,
		Decision:   r.Decision,
		RiskLevel:  r.RiskLevel,
		Backend:    r.Backend,
		RuleIDs:    r.ruleIDs(),
		Blocked:    r.Blocked,
		Redacted:   r.Redacted,
		DurationUS: r.DurationUS,
		Timestamp:  r.Timestamp,
	}
}

// WriteReportJSON writes the report as indented JSON to w.
func WriteReportJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
