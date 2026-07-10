//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the tool execution safety guard.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type sample struct {
	Name           string          `json:"name"`
	Request        safety.Request  `json:"request"`
	ExpectDecision safety.Decision `json:"expect_decision,omitempty"`
	ExpectRule     string          `json:"expect_rule,omitempty"`
}

func main() {
	policyPath := flag.String("policy", "tool_safety_policy.yaml", "policy file")
	samplesPath := flag.String("samples", "samples.json", "sample manifest")
	reportPath := flag.String("report", "tool_safety_report.json", "report output")
	auditPath := flag.String("audit", "tool_safety_audit.jsonl", "audit output")
	demo := flag.Bool("demo", false, "run PermissionPolicy pre-execution gate demo")
	flag.Parse()

	if *demo {
		if err := runDemo(*policyPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := run(*policyPath, *samplesPath, *reportPath, *auditPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(policyPath, samplesPath, reportPath, auditPath string) error {
	policy, err := safety.LoadPolicy(policyPath)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	manifest, err := loadSamples(samplesPath)
	if err != nil {
		return fmt.Errorf("load samples: %w", err)
	}
	audit, err := os.Create(auditPath)
	if err != nil {
		return fmt.Errorf("create audit file: %w", err)
	}
	defer audit.Close()

	scanner := safety.NewScanner(policy)
	auditSink := safety.NewWriterAuditSink(audit)
	reports := make([]safety.Report, 0, len(manifest))
	for _, s := range manifest {
		report := normalizeReport(scanner.Scan(context.Background(), s.Request))
		reports = append(reports, report)
		if err := auditSink.WriteAudit(safety.AuditEventFromReport(report)); err != nil {
			return fmt.Errorf("write audit: %w", err)
		}
	}
	b, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(reportPath, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func loadSamples(path string) ([]sample, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var samples []sample
	if err := json.Unmarshal(b, &samples); err != nil {
		return nil, err
	}
	return samples, nil
}

func normalizeReport(report safety.Report) safety.Report {
	fixed := time.Unix(0, 0).UTC()
	report.ScannedAt = fixed
	report.DurationMS = 0
	report.Elapsed = 0
	return report
}

func runDemo(policyPath string) error {
	policy := safety.NewPermissionPolicy(
		safety.WithPolicyFile(policyPath),
	)
	cases := []struct {
		toolName string
		args     string
	}{
		{toolName: "workspace_exec", args: `{"command":"go test ./tool/safety"}`},
		{toolName: "workspace_exec", args: `{"command":"rm -rf /"}`},
		{toolName: "hostexec_exec_command", args: `{"command":"go test ./...","tty":true}`},
		{toolName: "execute_code", args: `{"code_blocks":[{"language":"bash","code":"cat .env"}]}`},
	}
	for _, tc := range cases {
		decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:  tc.toolName,
			Arguments: []byte(tc.args),
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s %s -> %s %s\n", tc.toolName, tc.args, decision.Action, decision.Reason)
	}
	return nil
}
