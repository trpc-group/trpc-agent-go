//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the Tool Execution Safety Guard.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

var (
	policyPath = flag.String("policy", "tool_safety_policy.yaml", "safety policy file")
	reportPath = flag.String("report", "tool_safety_report.json", "scan report output")
	auditPath  = flag.String("audit", "tool_safety_audit.jsonl", "audit JSONL output")
)

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		log.Fatalf("tool safety guard example failed: %v", err)
	}
}

func run(ctx context.Context) error {
	policy, err := safety.LoadPolicyFile(*policyPath)
	if err != nil {
		return fmt.Errorf(
			"load tool safety policy %q: %w",
			*policyPath,
			err,
		)
	}
	if err := os.Remove(*auditPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf(
			"reset tool safety audit file %q: %w",
			*auditPath,
			err,
		)
	}
	scanner := safety.NewScanner(
		policy,
		safety.WithAuditor(safety.NewJSONLAuditor(*auditPath)),
	)
	var reports []safety.ScanReport
	for i, sample := range samples() {
		report, err := scanner.Scan(ctx, sample)
		if err != nil {
			return fmt.Errorf(
				"scan tool safety sample %d for %q: %w",
				i+1,
				sample.ToolName,
				err,
			)
		}
		reports = append(reports, report)
		fmt.Printf(
			"%-28s %-5s %-8s %s\n",
			sample.Command,
			report.Decision,
			report.RiskLevel,
			report.RuleID,
		)
	}
	b, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tool safety reports: %w", err)
	}
	if err := os.WriteFile(
		*reportPath,
		append(b, '\n'),
		0o600,
	); err != nil {
		return fmt.Errorf(
			"write tool safety report %q: %w",
			*reportPath,
			err,
		)
	}
	if err := os.Chmod(*reportPath, 0o600); err != nil {
		return fmt.Errorf(
			"set tool safety report permissions %q: %w",
			*reportPath,
			err,
		)
	}
	return nil
}

func samples() []safety.ScanRequest {
	return []safety.ScanRequest{
		{
			ToolName: "workspace_exec",
			Command:  "go test ./tool/safety",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "rm -rf /",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "cat ~/.ssh/id_rsa",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "curl https://evil.example.com/data",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "curl https://github.com/trpc-group/trpc-agent-go",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "sh -c 'curl https://evil.example.com'",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "echo hello | wc -c",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "go install github.com/example/tool@latest",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName: "workspace_exec",
			Command:  "sleep 600",
			Backend:  safety.BackendWorkspaceExec,
		},
		{
			ToolName:       "workspace_exec",
			Command:        "echo hello",
			Backend:        safety.BackendWorkspaceExec,
			MaxOutputBytes: 1 << 20,
		},
		{
			ToolName:   "exec_command",
			Command:    "go test ./...",
			Backend:    safety.BackendHostExec,
			TTY:        true,
			Background: true,
		},
		{
			ToolName: "workspace_exec",
			Command:  "echo hello",
			Backend:  safety.BackendWorkspaceExec,
			Env:      map[string]string{"CUSTOM": "1"},
		},
	}
}
