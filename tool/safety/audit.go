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
	"encoding/json"
	"io"
	"time"
)

// auditEvent is one JSONL-compatible safety scan event.
type auditEvent struct {
	SchemaVersion    string    `json:"schema_version"`
	PolicyID         string    `json:"policy_id"`
	PolicyRevision   string    `json:"policy_revision"`
	Timestamp        time.Time `json:"timestamp"`
	ToolName         string    `json:"tool_name"`
	Decision         Decision  `json:"decision"`
	RiskLevel        RiskLevel `json:"risk_level"`
	RuleID           RuleID    `json:"rule_id"`
	RuleIDs          []RuleID  `json:"rule_ids"`
	Backend          Backend   `json:"backend"`
	CommandSHA256    string    `json:"command_sha256"`
	ScanDurationUS   int64     `json:"scan_duration_us"`
	Redacted         bool      `json:"redacted"`
	ExecutionBlocked bool      `json:"execution_blocked"`
}

func (s *Scanner) writeAudit(report Report, elapsed time.Duration) error {
	if s.auditWriter == nil {
		return nil
	}
	ruleIDs := make([]RuleID, 0, len(report.Findings))
	seen := make(map[RuleID]struct{}, len(report.Findings))
	for _, finding := range report.Findings {
		if _, ok := seen[finding.RuleID]; ok {
			continue
		}
		seen[finding.RuleID] = struct{}{}
		ruleIDs = append(ruleIDs, finding.RuleID)
	}
	event := auditEvent{
		SchemaVersion:    report.SchemaVersion,
		PolicyID:         report.PolicyID,
		PolicyRevision:   report.PolicyRevision,
		Timestamp:        time.Now().UTC(),
		ToolName:         report.ToolName,
		Decision:         report.Decision,
		RiskLevel:        report.RiskLevel,
		RuleID:           report.RuleID,
		RuleIDs:          ruleIDs,
		Backend:          report.Backend,
		CommandSHA256:    report.CommandSHA256,
		ScanDurationUS:   elapsed.Microseconds(),
		Redacted:         report.Redacted,
		ExecutionBlocked: report.Intercepted,
	}
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	n, err := s.auditWriter.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return io.ErrShortWrite
	}
	return nil
}
