// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestRunProducesReportAndMetadataOnlyAudit(t *testing.T) {
	outputDir := t.TempDir()
	if err := run(context.Background(), "tool_safety_policy.yaml", outputDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "tool_safety_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reports []sampleResult
	if err := json.Unmarshal(data, &reports); err != nil {
		t.Fatal(err)
	}
	if len(reports) != len(publicSamples) {
		t.Fatalf("reports = %d, want %d", len(reports), len(publicSamples))
	}
	for _, report := range reports {
		if report.Report.ToolName == "" || report.Report.Command == "" ||
			report.Report.Backend == "" || report.Report.RequestID == "" {
			t.Fatalf("report %q is missing required identity fields: %+v", report.Name, report.Report)
		}
		if report.Report.RiskLevel == "" || report.Report.Recommendation == "" {
			t.Fatalf("report %q is missing risk guidance: %+v", report.Name, report.Report)
		}
		encoded, err := json.Marshal(report.Report)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{
			"decision", "risk_level", "rule", "rule_ids", "evidence",
			"recommendation", "tool_name", "command", "backend", "blocked",
		} {
			if _, ok := fields[key]; !ok {
				t.Fatalf("report %q is missing schema field %q: %s", report.Name, key, encoded)
			}
		}
	}

	file, err := os.Open(filepath.Join(outputDir, "tool_safety_audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
		var event safety.AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("audit line %d: %v", count, err)
		}
		if event.RequestID == "" {
			t.Fatalf("audit line %d has no request digest", count)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(publicSamples) {
		t.Fatalf("audit lines = %d, want %d", count, len(publicSamples))
	}
}
