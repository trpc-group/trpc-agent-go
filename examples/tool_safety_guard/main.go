//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the Tool Execution Safety Guard.
//
// This example shows how to:
//  1. Create a Guard with DefaultPolicy
//  2. Scan sample commands (both safe and dangerous)
//  3. Print structured scan results
//  4. Use WrapTool for pre-execution interception
//  5. Use the Guard as a tool.PermissionPolicy with agent.WithToolPermissionPolicy
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	ctx := context.Background()

	// Step 1: Create a Guard with the default fail-closed policy.
	guard, err := safety.NewGuard()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create guard: %v\n", err)
		os.Exit(1)
	}
	defer guard.Close()

	scanner := safety.NewScanner(safety.DefaultPolicy())

	// Step 2: Define sample commands to scan — safe and dangerous.
	commands := []struct {
		label   string
		tool    string
		backend string
		cmd     string
	}{
		{"Safe: go test", "workspace_exec", "workspaceexec", "go test ./..."},
		{"Dangerous: rm -rf /", "workspace_exec", "workspaceexec", "rm -rf /"},
		{"Credential access: cat ~/.ssh/id_rsa", "workspace_exec", "workspaceexec", "cat ~/.ssh/id_rsa"},
		{"Network: curl non-whitelisted", "workspace_exec", "workspaceexec", "curl http://evil.example.com"},
		{"Dependency: pip install requests", "workspace_exec", "workspaceexec", "pip install requests"},
		{"Shell bypass: sh -c 'rm -rf /'", "workspace_exec", "workspaceexec", "sh -c 'rm -rf /'"},
	}

	// Step 3: Scan each command and print structured results.
	fmt.Println("=== Safety Scan Results ===")
	fmt.Println()
	for _, c := range commands {
		input := safety.ScanInput{
			Command:  c.cmd,
			ToolName: c.tool,
			Backend:  c.backend,
		}
		result := scanner.Scan(ctx, input)
		printScanResult(c.label, result)
	}

	// Step 4: Demonstrate WrapTool for pre-execution interception.
	fmt.Println()
	fmt.Println("=== WrapTool Demo ===")
	fmt.Println()
	fmt.Println("WrapTool wraps any tool.Tool with the safety guard.")
	fmt.Println("If the guard denies the call, the tool is not executed.")
	fmt.Println("Example usage:")
	fmt.Println()
	fmt.Println("  guard, _ := safety.NewGuard()")
	fmt.Println("  safeTool := safety.WrapTool(myTool, guard)")
	fmt.Println("  // safeTool.Call() checks safety before delegating to myTool")
	fmt.Println()

	// Step 5: Show how to use the Guard as a tool.PermissionPolicy
	// with agent.WithToolPermissionPolicy.
	fmt.Println("=== Agent Integration Demo ===")
	fmt.Println()
	fmt.Println("The Guard implements tool.PermissionPolicy, so it can be")
	fmt.Println("passed directly to agent.WithToolPermissionPolicy:")
	fmt.Println()
	fmt.Println("  guard, _ := safety.NewGuard()")
	fmt.Println("  opts := agent.NewRunOptions(")
	fmt.Println("      agent.WithToolPermissionPolicy(guard),")
	fmt.Println("  )")
	fmt.Println("  // Every tool call is now safety-checked before execution")
	fmt.Println()
	fmt.Println("Alternatively, load policy from a YAML file:")
	fmt.Println()
	fmt.Println("  guard, _ := safety.NewGuard(")
	fmt.Println("      safety.WithPolicyFile(\"tool_safety_policy.yaml\"),")
	fmt.Println("      safety.WithAuditFile(\"audit.jsonl\"),")
	fmt.Println("  )")
	fmt.Println()

	// Show report output for the dangerous command.
	fmt.Println("=== Structured Report (rm -rf /) ===")
	fmt.Println()
	dangerousInput := safety.ScanInput{
		Command:  "rm -rf /",
		ToolName: "workspace_exec",
		Backend:  "workspaceexec",
	}
	dangerousResult := scanner.Scan(ctx, dangerousInput)
	report := safety.NewReport(dangerousResult)
	reportJSON, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(reportJSON))
}

func printScanResult(label string, result safety.ScanResult) {
	fmt.Printf("--- %s ---\n", label)
	fmt.Printf("  Command:     %s\n", result.Command)
	fmt.Printf("  Decision:    %s\n", result.Decision)
	fmt.Printf("  Risk Level:  %s\n", result.RiskLevel)
	fmt.Printf("  Intercepted: %v\n", result.Intercepted)
	if len(result.Findings) > 0 {
		fmt.Println("  Findings:")
		for _, f := range result.Findings {
			fmt.Printf("    - [%s] %s: %s\n", f.RuleID, f.RuleName, f.Evidence)
			fmt.Printf("      Risk: %s | Decision: %s\n", f.RiskLevel, f.Decision)
			fmt.Printf("      Recommendation: %s\n", f.Recommendation)
		}
	} else {
		fmt.Println("  Findings: (none)")
	}
	fmt.Println()
}
