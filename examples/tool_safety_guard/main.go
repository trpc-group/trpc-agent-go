//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main scans representative tool execution requests and writes
// structured reports and audit events.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

var (
	policyPath = flag.String(
		"policy",
		"tool_safety_policy.yaml",
		"path to the tool safety policy",
	)
	reportPath = flag.String(
		"report",
		"tool_safety_report.json",
		"path for structured scan reports",
	)
	auditPath = flag.String(
		"audit",
		"tool_safety_audit.jsonl",
		"path for JSONL audit events",
	)
)

type sample struct {
	name  string
	input safety.Input
	args  map[string]any
}

type namedReport struct {
	Name string        `json:"name"`
	Scan safety.Report `json:"scan"`
}

func main() {
	flag.Parse()
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "tool safety example: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	policy, err := safety.LoadPolicy(*policyPath)
	if err != nil {
		return err
	}
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		return err
	}
	auditFile, err := os.OpenFile(
		*auditPath,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("open audit output: %w", err)
	}
	defer auditFile.Close()
	guard := safety.NewGuard(
		scanner,
		safety.WithAuditor(safety.NewJSONLAuditor(auditFile)),
	)

	samples := sampleCases()
	reports := make([]namedReport, 0, len(samples))
	for _, item := range samples {
		report := scanner.Scan(ctx, item.input)
		reports = append(reports, namedReport{
			Name: item.name,
			Scan: report,
		})

		arguments, err := json.Marshal(item.args)
		if err != nil {
			return fmt.Errorf("marshal sample %q: %w", item.name, err)
		}
		_, err = guard.CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:  item.input.ToolName,
			Arguments: arguments,
		})
		if err != nil {
			return fmt.Errorf("audit sample %q: %w", item.name, err)
		}
		fmt.Printf(
			"%-28s decision=%-5s risk=%-8s rule=%s\n",
			item.name,
			report.Decision,
			report.RiskLevel,
			report.RuleID,
		)
	}

	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal reports: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*reportPath, data, 0o600); err != nil {
		return fmt.Errorf("write reports: %w", err)
	}
	return nil
}

func sampleCases() []sample {
	return []sample{
		{
			name: "safe_go_test",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "go test ./tool/...",
			},
			args: map[string]any{"command": "go test ./tool/..."},
		},
		{
			name: "dangerous_delete",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "rm -rf /",
			},
			args: map[string]any{"command": "rm -rf /"},
		},
		{
			name: "read_private_key",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "cat ~/.ssh/id_rsa",
			},
			args: map[string]any{"command": "cat ~/.ssh/id_rsa"},
		},
		{
			name: "blocked_network",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "curl https://collector.invalid/upload",
			},
			args: map[string]any{
				"command": "curl https://collector.invalid/upload",
			},
		},
		{
			name: "allowed_network",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "curl https://proxy.golang.org",
			},
			args: map[string]any{
				"command": "curl https://proxy.golang.org",
			},
		},
		{
			name: "shell_wrapper",
			input: safety.Input{
				ToolName: "exec_command",
				Backend:  safety.BackendHost,
				Command:  "bash -c 'go test ./...'",
			},
			args: map[string]any{"command": "bash -c 'go test ./...'"},
		},
		{
			name: "safe_pipeline",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "go test ./... | head -n 20",
			},
			args: map[string]any{
				"command": "go test ./... | head -n 20",
			},
		},
		{
			name: "dependency_install",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "go install example.com/cmd/tool@latest",
			},
			args: map[string]any{
				"command": "go install example.com/cmd/tool@latest",
			},
		},
		{
			name: "long_sleep",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "sleep 3600",
			},
			args: map[string]any{"command": "sleep 3600"},
		},
		{
			name: "unbounded_output",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "yes",
			},
			args: map[string]any{"command": "yes"},
		},
		{
			name: "host_pty_session",
			input: safety.Input{
				ToolName: "exec_command",
				Backend:  safety.BackendHost,
				Command:  "go test ./...",
				TTY:      true,
			},
			args: map[string]any{
				"command": "go test ./...",
				"tty":     true,
			},
		},
		{
			name: "human_review",
			input: safety.Input{
				ToolName: "workspace_exec",
				Backend:  safety.BackendWorkspace,
				Command:  "git status",
			},
			args: map[string]any{"command": "git status"},
		},
		{
			name: "environment_injection",
			input: safety.Input{
				ToolName: "exec_command",
				Backend:  safety.BackendHost,
				Command:  "go test ./...",
				Environment: map[string]string{
					"LD_PRELOAD": "/tmp/inject.so",
				},
			},
			args: map[string]any{
				"command": "go test ./...",
				"env": map[string]string{
					"LD_PRELOAD": "/tmp/inject.so",
				},
			},
		},
		{
			name: "code_infinite_loop",
			input: safety.Input{
				ToolName: "execute_code",
				Backend:  safety.BackendCodeExecutor,
				Language: "bash",
				Script:   "while true; do echo busy; done",
			},
			args: map[string]any{
				"code_blocks": []map[string]string{{
					"language": "bash",
					"code":     "while true; do echo busy; done",
				}},
			},
		},
	}
}
