//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the Tool Execution Safety Guard with a mock
// corpus. It loads the policy, scans every sample, exercises
// CheckToolPermission with a fake request, attaches callbacks to a local
// tool.Callbacks, and writes the batch report and audit files.
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

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "tool_safety_guard example failed: %v\n", err)
		os.Exit(1)
	}
}

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
	corpus := buildCorpus()
	inputs := make([]safety.ScanInput, 0, len(corpus))
	for _, c := range corpus {
		inputs = append(inputs, c.input)
	}
	batch, err := guard.ScanBatch(ctx, inputs)
	if err != nil {
		return fmt.Errorf("scan batch: %w", err)
	}

	// Exercise CheckToolPermission with several representative requests
	// so the audit contains preflight events for allow, deny, and ask
	// decisions.
	permissionCases := []struct {
		name      string
		toolName  string
		arguments string
	}{
		{"dangerous delete", "workspace_exec", `{"command":"rm -rf /"}`},
		{"credential read", "workspace_exec", `{"command":"cat ~/.ssh/id_rsa"}`},
		{"whitelisted request", "workspace_exec", `{"command":"curl https://github.com/org/repo"}`},
		{"dependency install", "workspace_exec", `{"command":"npm install package"}`},
		{"safe go test", "workspace_exec", `{"command":"go test ./..."}`},
	}
	for _, pc := range permissionCases {
		decision, err := guard.CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:  pc.toolName,
			Arguments: []byte(pc.arguments),
		})
		if err != nil {
			return fmt.Errorf("check permission %q: %w", pc.name, err)
		}
		fmt.Printf("CheckToolPermission(%q) -> %s\n", pc.name, decision.Action)
	}

	// Attach callbacks to a local tool.Callbacks and exercise the
	// after-tool redaction path with a secret-bearing result.
	cbs := guard.Callbacks()
	afterOut, err := cbs.RunAfterTool(ctx, &tool.AfterToolArgs{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
		Result: map[string]any{
			"output": "API_KEY=sk_live_1234567890abcdef1234",
		},
	})
	if err != nil {
		return fmt.Errorf("after tool: %w", err)
	}
	if afterOut != nil && afterOut.CustomResult != nil {
		raw, _ := json.Marshal(afterOut.CustomResult)
		fmt.Printf("AfterTool redacted result: %s\n", string(raw))
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

func buildCorpus() []corpusEntry {
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
