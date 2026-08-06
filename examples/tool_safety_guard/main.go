//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	fmt.Println("🛡️  tRPC-Agent-Go Tool Safety Guard Demonstration")
	fmt.Println("==================================================")

	// Runtime output is written to output/ to avoid overwriting the committed
	// sample fixtures (tool_safety_audit.jsonl, tool_safety_report.json).
	if err := os.MkdirAll("output", 0750); err != nil {
		log.Fatalf("failed to create output dir: %v", err)
	}

	// 1. Initialize SafetyGuard with policy, audit log and report outputs
	auditLogger, err := safety.NewFileAuditLogger("output/tool_safety_audit.jsonl")
	if err != nil {
		log.Fatalf("failed to create audit logger: %v", err)
	}
	defer auditLogger.Close()

	guard := safety.NewGuard(
		safety.WithPolicyFile("tool_safety_policy.yaml"),
		safety.WithAuditLogger(auditLogger),
		safety.WithReportPath("output/tool_safety_report.json"),
	)

	ctx := context.Background()

	// 2. Sample tool calls demonstrating different safety decisions
	sampleCommands := []struct {
		description string
		toolName    string
		command     string
	}{
		{
			description: "Safe Go Test Command",
			toolName:    "workspace_exec",
			command:     "go test ./...",
		},
		{
			description: "Dangerous Root Deletion Attempt",
			toolName:    "workspace_exec",
			command:     "rm -rf /",
		},
		{
			description: "Attempting to Read SSH Key",
			toolName:    "workspace_exec",
			command:     "cat ~/.ssh/id_rsa",
		},
		{
			description: "Non-Whitelisted Outbound Network Request",
			toolName:    "workspace_exec",
			command:     "curl https://untrusted-site.com/data",
		},
		{
			description: "Whitelisted Domain Request",
			toolName:    "workspace_exec",
			command:     "curl https://api.github.com/events",
		},
		{
			description: "Package Installation Requiring Human Review (Ask)",
			toolName:    "workspace_exec",
			command:     "npm install express",
		},
		{
			description: "Secret API Key Exposure in Command",
			toolName:    "workspace_exec",
			command:     "curl -H 'Authorization: Bearer sk-78c331e8061c42a4883cfee6633447dd' https://api.openai.com/v1",
		},
	}

	for i, sample := range sampleCommands {
		fmt.Printf("\n[%d] %s\n", i+1, sample.description)
		fmt.Printf("   Tool: %s | Command: %s\n", sample.toolName, sample.command)

		argsPayload, _ := json.Marshal(map[string]any{
			"command": sample.command,
		})

		req := &tool.PermissionRequest{
			ToolName:   sample.toolName,
			ToolCallID: fmt.Sprintf("call_%d", i+1),
			Arguments:  argsPayload,
		}

		// Perform Safety Guard check via CheckToolPermission
		decision, err := guard.CheckToolPermission(ctx, req)
		if err != nil {
			fmt.Printf("   ❌ Error: %v\n", err)
			continue
		}

		report := guard.LastReport()
		fmt.Printf("   👉 Decision : %s\n", decision.Action)
		fmt.Printf("   👉 RiskLevel: %s\n", report.RiskLevel)
		fmt.Printf("   👉 Rule ID  : %s\n", report.RuleID)
		fmt.Printf("   👉 Evidence : %s\n", decision.Reason)
	}

	fmt.Println("\n==================================================")
	fmt.Println("✅ Safety scan reports saved to output/tool_safety_report.json and output/tool_safety_audit.jsonl")

	// Integration hint with agent
	_ = agent.WithToolPermissionPolicy(guard)
}
