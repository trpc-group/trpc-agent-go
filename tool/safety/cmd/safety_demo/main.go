// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package main is a standalone, LLM-free demo of the tool/safety package.
//
// It loads the canonical policy YAML, runs the Scanner over three
// representative commands (one allow, one ask, one deny), writes the
// resulting Reports as a JSON array to -report, and appends one JSONL
// audit event per scan to -audit. The output files are the verified
// examples referenced by PR_DESCRIPTION.md.
//
// Run from the repo root:
//
//	go run ./tool/safety/cmd/safety_demo
//
// The defaults point at the in-tree policy and testdata paths. Override
// them with -policy, -report, and -audit to regenerate the examples
// elsewhere.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	policyPath := flag.String("policy", "tool/safety/tool_safety_policy.yaml", "path to the safety policy YAML")
	reportPath := flag.String("report", "tool/safety/testdata/example_report.json", "path to write the structured Report JSON array")
	auditPath := flag.String("audit", "tool/safety/testdata/example_audit.jsonl", "path to append JSONL audit events to")
	flag.Parse()

	policy, err := safety.LoadPolicy(*policyPath)
	if err != nil {
		log.Fatalf("load policy %q: %v", *policyPath, err)
	}
	scanner := safety.NewScanner(policy)

	// Representative scans: one safe, one elevated (ask), one denied.
	// These mirror the corpus categories in testdata/safety_corpus.yaml.
	scans := []safety.ScanInput{
		{ToolName: "workspace_exec", Backend: "workspaceexec", Command: "echo hello"},
		{ToolName: "workspace_exec", Backend: "workspaceexec", Command: "go get example.com/module"},
		{ToolName: "workspace_exec", Backend: "workspaceexec", Command: "rm -rf /"},
	}

	var reports []safety.Report
	for _, input := range scans {
		start := time.Now()
		report := scanner.Scan(input)
		report.DurationMS = time.Since(start).Milliseconds()
		if report.Decision != safety.DecisionAllow {
			report.Intercepted = true
		}
		reports = append(reports, report)
	}

	if err := writeReport(*reportPath, reports); err != nil {
		log.Fatalf("write report: %v", err)
	}

	if err := writeAudit(*auditPath, reports); err != nil {
		log.Fatalf("write audit: %v", err)
	}

	fmt.Printf("safety_demo: wrote %d reports to %s\n", len(reports), *reportPath)
	fmt.Printf("safety_demo: wrote %d audit events to %s\n", len(reports), *auditPath)
	for i, r := range reports {
		fmt.Printf("  [%d] command=%-32q decision=%-6s risk=%-8s intercepted=%v\n",
			i, r.Command, r.Decision, r.RiskLevel, r.Intercepted)
	}
}

func writeReport(path string, reports []safety.Report) error {
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal reports: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func writeAudit(path string, reports []safety.Report) error {
	// Truncate so re-running the demo produces a deterministic file.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear audit file: %w", err)
	}
	auditor, err := safety.NewJSONLAuditor(path)
	if err != nil {
		return fmt.Errorf("new jsonl auditor: %w", err)
	}
	defer auditor.Close()
	for _, report := range reports {
		if err := auditor.Write(report); err != nil {
			return fmt.Errorf("audit write: %w", err)
		}
	}
	return nil
}
