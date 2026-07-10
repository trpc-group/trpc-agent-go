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
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestToolSafetyGuardExample(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "tool_safety_report.json")
	auditPath := filepath.Join(dir, "tool_safety_audit.jsonl")
	if err := run("tool_safety_policy.yaml", "samples.json", reportPath, auditPath); err != nil {
		t.Fatalf("run example: %v", err)
	}

	samples, err := loadSamples("samples.json")
	if err != nil {
		t.Fatalf("load samples: %v", err)
	}
	var reports []safety.Report
	b, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err := json.Unmarshal(b, &reports); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(reports) != 12 {
		t.Fatalf("reports length = %d, want 12", len(reports))
	}
	for i, sample := range samples {
		report := reports[i]
		if report.Decision != sample.ExpectDecision {
			t.Fatalf("%s decision = %s, want %s",
				sample.Name, report.Decision, sample.ExpectDecision)
		}
		if sample.ExpectRule != "" && report.PrimaryRuleID() != sample.ExpectRule {
			t.Fatalf("%s rule = %s, want %s",
				sample.Name, report.PrimaryRuleID(), sample.ExpectRule)
		}
		if !report.ScannedAt.Equal(normalizeReport(report).ScannedAt) {
			t.Fatalf("%s report timestamp is not deterministic: %s",
				sample.Name, report.ScannedAt)
		}
	}

	f, err := os.Open(auditPath)
	if err != nil {
		t.Fatalf("open audit: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lines := 0
	for scanner.Scan() {
		lines++
		var ev safety.AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal audit line %d: %v", lines, err)
		}
		if ev.ToolName == "" || ev.Decision == "" {
			t.Fatalf("incomplete audit event: %#v", ev)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit: %v", err)
	}
	if lines != 12 {
		t.Fatalf("audit lines = %d, want 12", lines)
	}
}

func TestToolSafetyGuardDemoAndErrors(t *testing.T) {
	if err := runDemo("tool_safety_policy.yaml"); err != nil {
		t.Fatalf("run demo: %v", err)
	}

	dir := t.TempDir()
	if err := run("missing.yaml", "samples.json",
		filepath.Join(dir, "report.json"), filepath.Join(dir, "audit.jsonl")); err == nil {
		t.Fatal("run with missing policy succeeded")
	}
	if err := run("tool_safety_policy.yaml", "missing.json",
		filepath.Join(dir, "report.json"), filepath.Join(dir, "audit.jsonl")); err == nil {
		t.Fatal("run with missing samples succeeded")
	}
	if err := run("tool_safety_policy.yaml", "samples.json",
		filepath.Join(dir, "report.json"), filepath.Join(dir, "missing", "audit.jsonl")); err == nil {
		t.Fatal("run with bad audit path succeeded")
	}
	if _, err := loadSamples("missing.json"); err == nil {
		t.Fatal("loadSamples with missing file succeeded")
	}
	badSamples := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badSamples, []byte("{"), 0o600); err != nil {
		t.Fatalf("write bad samples: %v", err)
	}
	if _, err := loadSamples(badSamples); err == nil {
		t.Fatal("loadSamples with bad JSON succeeded")
	}
}

func TestMainFlagPaths(t *testing.T) {
	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	}()

	dir := t.TempDir()
	flag.CommandLine = flag.NewFlagSet("tool_safety_guard", flag.ContinueOnError)
	os.Args = []string{
		"tool_safety_guard",
		"-policy", "tool_safety_policy.yaml",
		"-samples", "samples.json",
		"-report", filepath.Join(dir, "report.json"),
		"-audit", filepath.Join(dir, "audit.jsonl"),
	}
	main()

	flag.CommandLine = flag.NewFlagSet("tool_safety_guard", flag.ContinueOnError)
	os.Args = []string{
		"tool_safety_guard",
		"-policy", "tool_safety_policy.yaml",
		"-demo",
	}
	main()
}
