//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestRunWritesExpectedReports(t *testing.T) {
	oldPolicyPath := *policyPath
	oldReportPath := *reportPath
	oldAuditPath := *auditPath

	t.Cleanup(func() {
		*policyPath = oldPolicyPath
		*reportPath = oldReportPath
		*auditPath = oldAuditPath
	})

	tempDir := t.TempDir()
	*policyPath = "tool_safety_policy.yaml"
	*reportPath = filepath.Join(
		tempDir,
		"tool_safety_report.json",
	)
	*auditPath = filepath.Join(
		tempDir,
		"tool_safety_audit.jsonl",
	)

	if err := run(context.Background()); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	data, err := os.ReadFile(*reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}

	var reports []safety.ScanReport
	if err := json.Unmarshal(data, &reports); err != nil {
		t.Fatalf("decode report JSON: %v", err)
	}

	wantDecisions := []safety.Decision{
		safety.DecisionAllow,
		safety.DecisionDeny,
		safety.DecisionDeny,
		safety.DecisionDeny,
		safety.DecisionAllow,
		safety.DecisionDeny,
		safety.DecisionAsk,
		safety.DecisionAsk,
		safety.DecisionAsk,
		safety.DecisionAsk,
		safety.DecisionAsk,
		safety.DecisionAsk,
	}

	if len(reports) != len(wantDecisions) {
		t.Fatalf(
			"report count = %d, want %d",
			len(reports),
			len(wantDecisions),
		)
	}

	for i, report := range reports {
		if report.Decision != wantDecisions[i] {
			t.Errorf(
				"report[%d].Decision = %q, want %q",
				i,
				report.Decision,
				wantDecisions[i],
			)
		}

		if report.RiskLevel == "" {
			t.Errorf(
				"report[%d].RiskLevel is empty",
				i,
			)
		}

		if report.RuleID == "" {
			t.Errorf(
				"report[%d].RuleID is empty",
				i,
			)
		}

		if len(report.Evidence) == 0 {
			t.Errorf(
				"report[%d].Evidence is empty",
				i,
			)
		}

		if report.Recommendation == "" {
			t.Errorf(
				"report[%d].Recommendation is empty",
				i,
			)
		}

		wantBlocked :=
			report.Decision != safety.DecisionAllow
		if report.Blocked != wantBlocked {
			t.Errorf(
				"report[%d].Blocked = %t, want %t",
				i,
				report.Blocked,
				wantBlocked,
			)
		}
	}
}

func TestRunWritesValidAuditEvents(t *testing.T) {
	oldPolicyPath := *policyPath
	oldReportPath := *reportPath
	oldAuditPath := *auditPath

	t.Cleanup(func() {
		*policyPath = oldPolicyPath
		*reportPath = oldReportPath
		*auditPath = oldAuditPath
	})

	tempDir := t.TempDir()
	*policyPath = "tool_safety_policy.yaml"
	*reportPath = filepath.Join(
		tempDir,
		"tool_safety_report.json",
	)
	*auditPath = filepath.Join(
		tempDir,
		"tool_safety_audit.jsonl",
	)

	if err := run(context.Background()); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	auditData, err := os.ReadFile(*auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}

	lines := bytes.Split(
		bytes.TrimSpace(auditData),
		[]byte("\n"),
	)
	if len(lines) != len(samples()) {
		t.Fatalf(
			"audit line count = %d, want %d",
			len(lines),
			len(samples()),
		)
	}

	requiredFields := []string{
		"timestamp",
		"tool_name",
		"decision",
		"risk_level",
		"rule_id",
		"duration_ms",
		"redacted",
		"blocked",
		"backend",
	}

	for i, line := range lines {
		var event safety.AuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Errorf(
				"audit line %d is not valid JSON: %v",
				i,
				err,
			)
			continue
		}

		if event.Timestamp.IsZero() {
			t.Errorf(
				"audit line %d has zero timestamp",
				i,
			)
		}

		if event.ToolName == "" {
			t.Errorf(
				"audit line %d has empty tool_name",
				i,
			)
		}

		switch event.Decision {
		case safety.DecisionAllow,
			safety.DecisionAsk,
			safety.DecisionDeny:
		default:
			t.Errorf(
				"audit line %d has invalid decision %q",
				i,
				event.Decision,
			)
		}

		if event.RiskLevel == "" {
			t.Errorf(
				"audit line %d has empty risk_level",
				i,
			)
		}

		if event.RuleID == "" {
			t.Errorf(
				"audit line %d has empty rule_id",
				i,
			)
		}

		if event.Backend == "" {
			t.Errorf(
				"audit line %d has empty backend",
				i,
			)
		}

		if event.DurationMS < 0 {
			t.Errorf(
				"audit line %d has negative duration_ms",
				i,
			)
		}

		wantBlocked :=
			event.Decision != safety.DecisionAllow
		if event.Blocked != wantBlocked {
			t.Errorf(
				"audit line %d blocked = %t, want %t",
				i,
				event.Blocked,
				wantBlocked,
			)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			t.Errorf(
				"decode audit line %d fields: %v",
				i,
				err,
			)
			continue
		}

		for _, field := range requiredFields {
			if _, ok := raw[field]; !ok {
				t.Errorf(
					"audit line %d is missing field %q",
					i,
					field,
				)
			}
		}

		if len(raw) != len(requiredFields) {
			t.Errorf(
				"audit line %d has %d fields, want exactly %d: %v",
				i,
				len(raw),
				len(requiredFields),
				raw,
			)
		}
	}

	reportData, err := os.ReadFile(*reportPath)
	if err != nil {
		t.Fatalf("read report file: %v", err)
	}

	assertNoKnownSecret(
		t,
		"report",
		reportData,
	)
	assertNoKnownSecret(
		t,
		"audit",
		auditData,
	)
}

func assertNoKnownSecret(
	t *testing.T,
	name string,
	data []byte,
) {
	t.Helper()

	lower := strings.ToLower(string(data))
	for _, marker := range []string{
		"bearer ",
		"password=",
		"api_key=",
		"akia",
		"ghp_",
		"github_pat_",
		"begin private key",
	} {
		if strings.Contains(lower, marker) {
			t.Errorf(
				"%s contains secret marker %q",
				name,
				marker,
			)
		}
	}
}
