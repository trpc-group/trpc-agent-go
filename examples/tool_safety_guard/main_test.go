//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type checkedAuditEvent struct {
	SchemaVersion    string           `json:"schema_version"`
	PolicyID         string           `json:"policy_id"`
	PolicyRevision   string           `json:"policy_revision"`
	Timestamp        time.Time        `json:"timestamp"`
	ToolName         string           `json:"tool_name"`
	Decision         safety.Decision  `json:"decision"`
	RiskLevel        safety.RiskLevel `json:"risk_level"`
	RuleID           safety.RuleID    `json:"rule_id"`
	RuleIDs          []safety.RuleID  `json:"rule_ids"`
	Backend          safety.Backend   `json:"backend"`
	CommandSHA256    string           `json:"command_sha256"`
	ScanDurationUS   int64            `json:"scan_duration_us"`
	Redacted         bool             `json:"redacted"`
	ExecutionBlocked bool             `json:"execution_blocked"`
}

func TestCheckedInArtifactsMatchSamples(t *testing.T) {
	policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	samples, err := loadSamples("tool_safety_samples.json")
	if err != nil {
		t.Fatalf("load samples: %v", err)
	}
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatalf("new scanner: %v", err)
	}
	reports, err := scanSamples(context.Background(), scanner, samples)
	if err != nil {
		t.Fatalf("scan samples: %v", err)
	}

	encoded, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatalf("encode reports: %v", err)
	}
	encoded = append(encoded, '\n')
	wantReport, err := os.ReadFile("tool_safety_report.json")
	if err != nil {
		t.Fatalf("read report artifact: %v", err)
	}
	if !bytes.Equal(wantReport, encoded) {
		t.Fatal("tool_safety_report.json does not match generated samples")
	}

	auditData, err := os.ReadFile("tool_safety_audit.jsonl")
	if err != nil {
		t.Fatalf("read audit artifact: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(auditData), []byte{'\n'})
	if len(lines) != len(reports) {
		t.Fatalf("audit line count = %d, want %d", len(lines), len(reports))
	}
	for index, line := range lines {
		var event checkedAuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode audit line %d: %v", index+1, err)
		}
		report := reports[index].Report
		if event.Timestamp.IsZero() || event.ScanDurationUS < 0 {
			t.Fatalf("audit line %d has invalid timing fields", index+1)
		}
		if event.ToolName != report.ToolName ||
			event.SchemaVersion != report.SchemaVersion ||
			event.PolicyID != report.PolicyID ||
			event.PolicyRevision != report.PolicyRevision ||
			event.Decision != report.Decision ||
			event.RiskLevel != report.RiskLevel ||
			event.RuleID != report.RuleID ||
			event.Backend != report.Backend ||
			event.CommandSHA256 != report.CommandSHA256 ||
			event.Redacted != report.Redacted ||
			event.ExecutionBlocked != report.Intercepted {
			t.Fatalf("audit line %d does not match report", index+1)
		}
		wantRuleIDs := findingRuleIDs(report.Findings)
		if !equalRuleIDs(event.RuleIDs, wantRuleIDs) {
			t.Fatalf("audit line %d rule_ids = %v, want %v", index+1, event.RuleIDs, wantRuleIDs)
		}
	}
}

func equalRuleIDs(left []safety.RuleID, right []safety.RuleID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func findingRuleIDs(findings []safety.Finding) []safety.RuleID {
	ruleIDs := make([]safety.RuleID, 0, len(findings))
	seen := make(map[safety.RuleID]struct{}, len(findings))
	for _, finding := range findings {
		if _, ok := seen[finding.RuleID]; ok {
			continue
		}
		seen[finding.RuleID] = struct{}{}
		ruleIDs = append(ruleIDs, finding.RuleID)
	}
	return ruleIDs
}
