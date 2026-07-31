//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demos tool/safety.Guard as a tool.PermissionPolicy (#2002).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	fmt.Println("trpc-agent-go tool safety guard demo (#2002)")
	fmt.Println("PermissionPolicy + shellsafe, fail-closed")

	if err := os.MkdirAll("output", 0o750); err != nil {
		log.Fatal(err)
	}

	auditor, err := safety.NewFileAuditor(filepath.Join("output", "tool_safety_audit.jsonl"))
	if err != nil {
		log.Fatal(err)
	}

	guard := safety.NewGuard(
		safety.WithPolicyFile("tool_safety_policy.yaml"),
		safety.WithAuditor(auditor),
	)

	samples := []struct {
		title string
		tool  string
		args  map[string]any
	}{
		{"safe go test", "workspace_exec", map[string]any{"command": "go test ./..."}},
		{"dangerous delete", "workspace_exec", map[string]any{"command": "rm -rf /"}},
		{"read ssh key", "workspace_exec", map[string]any{"command": "cat ~/.ssh/id_rsa"}},
		{"denied host", "workspace_exec", map[string]any{"command": "curl https://evil.example/x"}},
		{"allowed host", "workspace_exec", map[string]any{"command": "curl https://api.github.com/events"}},
		{"shell wrapper", "workspace_exec", map[string]any{"command": "bash -c 'id'"}},
		{"npm install ask", "workspace_exec", map[string]any{"command": "npm install express"}},
		{"secret token", "workspace_exec", map[string]any{"command": "curl -H 'Authorization: Bearer sk-78c331e8061c42a4883cfee6633447dd' https://api.github.com"}},
		{"hostexec ask", "exec_command", map[string]any{"command": "go test ./..."}},
		{"code_blocks secret", "execute_code", map[string]any{
			"code_blocks": []map[string]string{{"language": "python", "code": "api_key=supersecretvalue123"}},
		}},
	}

	ctx := context.Background()
	var reports []safety.Result
	for i, s := range samples {
		raw, _ := json.Marshal(s.args)
		dec, err := guard.CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:   s.tool,
			ToolCallID: fmt.Sprintf("demo-%d", i+1),
			Arguments:  raw,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("[%d] %s\n  tool=%s action=%s reason=%s\n",
			i+1, s.title, s.tool, dec.Action, dec.Reason)
	}
	reports = guard.LastResults()
	if err := safety.WriteReportJSON(filepath.Join("output", "tool_safety_report.json"), reports); err != nil {
		log.Fatal(err)
	}
	// Also write stable sample fixtures for the issue deliverable.
	_ = safety.WriteReportJSON("tool_safety_report.json", reports)
	fmt.Println("wrote output/tool_safety_report.json and tool_safety_report.json")
}
