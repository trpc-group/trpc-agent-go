//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestRunWritesReportAndAudit(t *testing.T) {
	policy, err := filepath.Abs("tool_safety_policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()

	originalPolicy := *policyPath
	originalReport := *reportPath
	originalAudit := *auditPath
	*policyPath = policy
	*reportPath = filepath.Join(tempDir, "report.json")
	*auditPath = filepath.Join(tempDir, "audit.jsonl")
	t.Cleanup(func() {
		*policyPath = originalPolicy
		*reportPath = originalReport
		*auditPath = originalAudit
	})

	if err := run(context.Background()); err != nil {
		t.Fatal(err)
	}

	reportData, err := os.ReadFile(*reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var reports []safety.Report
	if err := json.Unmarshal(reportData, &reports); err != nil {
		t.Fatal(err)
	}
	const wantSampleCount = 14
	wantSamples := samples()
	if len(wantSamples) != wantSampleCount {
		t.Fatalf("got %d bundled samples, want %d", len(wantSamples), wantSampleCount)
	}
	if len(reports) != wantSampleCount {
		t.Fatalf("got %d reports, want %d", len(reports), wantSampleCount)
	}
	for i, report := range reports {
		if report.ToolCallID != wantSamples[i].Name ||
			report.ToolName == "" ||
			!report.Backend.Valid() ||
			!report.Decision.Valid() ||
			report.RiskLevel == "" ||
			report.RuleID == "" {
			t.Fatalf("report %d has invalid shape: %+v", i, report)
		}
	}

	auditData, err := os.ReadFile(*auditPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(auditData), []byte{'\n'})
	if len(lines) != len(reports) {
		t.Fatalf("got %d audit events, want %d", len(lines), len(reports))
	}
	for i, line := range lines {
		var event safety.AuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode audit event %d: %v", i, err)
		}
		if event.ToolCallID != reports[i].ToolCallID ||
			event.ToolName != reports[i].ToolName ||
			event.Backend != reports[i].Backend ||
			event.Decision != reports[i].Decision ||
			event.PermissionAction == "" ||
			event.RuleID == "" {
			t.Fatalf("audit event %d has invalid shape: %+v", i, event)
		}
	}
}
