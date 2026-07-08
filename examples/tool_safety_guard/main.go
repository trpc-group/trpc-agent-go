// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	policyPath := flag.String("policy", "examples/tool_safety_guard/tool_safety_policy.yaml", "safety policy path")
	samplesDir := flag.String("samples", "examples/tool_safety_guard/samples", "sample request directory")
	reportPath := flag.String("report", "examples/tool_safety_guard/tool_safety_report.json", "report output path")
	auditPath := flag.String("audit", "examples/tool_safety_guard/tool_safety_audit.jsonl", "audit output path")
	flag.Parse()

	if err := run(*policyPath, *samplesDir, *reportPath, *auditPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(policyPath, samplesDir, reportPath, auditPath string) error {
	policy, err := safety.LoadPolicy(policyPath)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		return fmt.Errorf("create scanner: %w", err)
	}
	auditFile, err := os.Create(auditPath)
	if err != nil {
		return fmt.Errorf("create audit: %w", err)
	}
	defer auditFile.Close()
	writer := safety.NewJSONLWriter(auditFile)

	files, err := filepath.Glob(filepath.Join(samplesDir, "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	var reports []safety.Report
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var req safety.ExecutionRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if req.ID == "" {
			req.ID = filepath.Base(file)
		}
		report, err := scanner.Scan(context.Background(), req)
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		if err := writer.WriteAuditEvent(context.Background(), auditEvent(report)); err != nil {
			return err
		}
		reports = append(reports, report)
		fmt.Printf("%s\t%s\t%s\t%s\n", req.ID, report.Decision, report.RiskLevel, first(report.RuleIDs))
	}
	out, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(reportPath, append(out, '\n'), 0o600)
}

func auditEvent(report safety.Report) safety.AuditEvent {
	return safety.AuditEvent{
		Timestamp:  reportTime(),
		RequestID:  report.RequestID,
		ToolName:   report.ToolName,
		Backend:    report.Backend,
		Decision:   report.Decision,
		RiskLevel:  report.RiskLevel,
		RuleID:     first(report.RuleIDs),
		AllRuleIDs: report.RuleIDs,
		DurationMS: report.DurationMS,
		Blocked:    report.Blocked,
		Redacted:   report.Redacted,
		Summary:    report.Recommendation,
	}
}

func reportTime() time.Time {
	return time.Now().UTC()
}

func first(in []string) string {
	if len(in) == 0 {
		return ""
	}
	return in[0]
}
