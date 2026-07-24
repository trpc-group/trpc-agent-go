//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the Tool Execution Safety Guard with a mock
// corpus. It loads the policy, scans every sample, executes a mock
// callable tool through WrapTool, and writes the batch report and audit
// files.
//
// The example never calls an external model, shell, package manager,
// network endpoint, or API-key-dependent service.
//
// Run from the examples module:
//
//	cd examples
//	go run ./tool_safety_guard
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// main runs the tool safety guard example and exits non-zero on
// failure.
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "tool_safety_guard example failed: %v\n", err)
		os.Exit(1)
	}
}

// run demonstrates permission checks, result redaction, batch
// scanning, and report/audit output for the safety guard.
func run() error {
	ctx := context.Background()

	// Use an in-memory audit writer so the example does not leave
	// audit files behind unless the caller asks for them.
	auditBuf := new(bytes.Buffer)
	guard, err := safety.NewGuard(
		safety.WithPolicyFile("tool_safety_guard/tool_safety_policy.yaml"),
		safety.WithAuditWriter(auditBuf),
		safety.WithTelemetry(true),
		safety.WithRedaction(true),
	)
	if err != nil {
		return fmt.Errorf("new guard: %w", err)
	}
	defer guard.Close()

	// Scan the corpus.
	corpus := buildExampleCorpus()
	inputs := make([]safety.ScanInput, 0, len(corpus))
	for _, c := range corpus {
		inputs = append(inputs, c.input)
	}
	batch, err := guard.ScanBatch(ctx, inputs)
	if err != nil {
		return fmt.Errorf("scan batch: %w", err)
	}

	wrapped, err := safety.WrapTool(&demoTool{}, guard)
	if err != nil {
		return fmt.Errorf("wrap tool: %w", err)
	}

	// Exercise the package-owned wrapper with representative requests
	// so the audit contains preflight and completion events.
	permissionCases := []struct {
		name      string
		arguments string
	}{
		{"dangerous delete", `{"command":"rm -rf /"}`},
		{"credential read", `{"command":"cat ~/.ssh/id_rsa"}`},
		{"whitelisted request", `{"command":"curl https://github.com/org/repo","timeout":10}`},
		{"dependency install", `{"command":"npm install package","timeout":10}`},
		{"safe go test", `{"command":"go test ./...","timeout":10}`},
	}
	var redactedResult any
	for _, pc := range permissionCases {
		result, err := wrapped.Call(ctx, []byte(pc.arguments))
		if err != nil {
			return fmt.Errorf("wrapped call %q: %w", pc.name, err)
		}
		action := tool.PermissionActionAllow
		if permission, ok := result.(tool.PermissionResult); ok {
			if permission.Status == tool.PermissionResultStatusDenied {
				action = tool.PermissionActionDeny
			} else {
				action = tool.PermissionActionAsk
			}
		} else if pc.name == "safe go test" {
			redactedResult = result
		}
		fmt.Printf("WrapTool(%q) -> %s\n", pc.name, action)
	}
	if redactedResult != nil {
		raw, _ := json.Marshal(redactedResult)
		fmt.Printf("Wrapped redacted result: %s\n", string(raw))
	}

	// Write the batch report.
	reportData, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile("tool_safety_guard/tool_safety_report.json", reportData, 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	// Write the audit JSONL (the in-memory buffer).
	if err := os.WriteFile("tool_safety_guard/tool_safety_audit.jsonl", auditBuf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write audit: %w", err)
	}

	// Print a one-line summary so the caller can verify the run.
	fmt.Printf("Scanned %d samples: %d allowed, %d denied, %d asked (duration=%v)\n",
		batch.Summary.Total, batch.Summary.Allowed, batch.Summary.Denied,
		batch.Summary.Asked, time.Since(batch.GeneratedAt).Round(time.Millisecond))
	fmt.Println("Wrote tool_safety_report.json and tool_safety_audit.jsonl")
	return nil
}

type corpusEntry struct {
	name  string
	input safety.ScanInput
}

type demoTool struct{}

func (*demoTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (*demoTool) Call(
	_ context.Context,
	arguments []byte,
) (any, error) {
	if bytes.Contains(arguments, []byte("go test")) {
		return map[string]any{
			"output": "API_KEY=sk_live_1234567890abcdef1234",
		}, nil
	}
	return map[string]any{"output": "ok"}, nil
}

// buildExampleCorpus returns the focused sample inputs shown by this
// example. The package quality gate uses a larger testdata corpus.
func buildExampleCorpus() []corpusEntry {
	return []corpusEntry{
		{name: "safe go test", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "go test ./..."}},
		{name: "safe git status", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "git status"}},
		{name: "dangerous delete", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "rm -rf /"}},
		{name: "ssh private key read", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "cat ~/.ssh/id_rsa"}},
		{name: "aws credentials read", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "cat ~/.aws/credentials"}},
		{name: "dotenv read", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "cat .env"}},
		{name: "non-whitelisted network", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "curl https://evil.example/x"}},
		{name: "whitelisted network", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "curl https://github.com/org/repo"}},
		{name: "shell wrapper bypass", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "sh -c 'rm -rf /'"}},
		{name: "shell substitution", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "echo $(whoami)"}},
		{name: "safe grep", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "grep foo bar.txt"}},
		{name: "safe pipeline cat grep", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "cat README.md | grep foo"}},
		{name: "dependency install npm", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "npm install package"}},
		{name: "long sleep", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "sleep 999999"}},
		{name: "output bomb yes", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "yes"}},
		{name: "host pty long session", input: safety.ScanInput{ToolName: "exec_command", Backend: safety.BackendHostExec, PTY: true, Timeout: 0, Command: "bash"}},
		{name: "host privilege escalation", input: safety.ScanInput{ToolName: "exec_command", Backend: safety.BackendHostExec, Command: "sudo id"}},
		{name: "secret in command", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "echo API_KEY=sk_live_1234567890abcdef1234"}},
		{name: "ask via dependency install pip", input: safety.ScanInput{ToolName: "workspace_exec", Backend: safety.BackendWorkspaceExec, Command: "pip install -r requirements.txt"}},
	}
}
