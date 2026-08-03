//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//

// Command demo is a runnable CLI for the tool/safety scanner. It scans a
// command and prints the structured report, writes an audit JSONL file, and
// --demo additionally shows the tool.PermissionPolicy interception path end to
// end (the same hook the agent framework calls before executing any tool).
//
// Usage:
//
//	go run ./tool/safety/examples/demo --command "rm -rf /"
//	go run ./tool/safety/examples/demo --policy tool/safety/tool_safety_policy.yaml --command "curl https://api.github.com" --backend workspaceexec
//	go run ./tool/safety/examples/demo --demo
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "demo: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		command  = flag.String("command", "", "command to scan (required unless --demo)")
		policy   = flag.String("policy", "", "policy YAML file (defaults to tool/safety/tool_safety_policy.yaml)")
		backend  = flag.String("backend", "workspaceexec", "execution backend (workspaceexec/hostexec/codeexec)")
		language = flag.String("language", "", "code language (codeexec: shell/python/javascript/...)")
		audit    = flag.String("audit", "demo_audit.jsonl", "audit JSONL output path")
		demo     = flag.Bool("demo", false, "run the built-in demo scenarios")
	)
	flag.Parse()

	scanner, err := buildScanner(*policy)
	if err != nil {
		return err
	}

	if *demo {
		return runDemo(scanner, *audit)
	}
	if *command == "" {
		flag.Usage()
		return fmt.Errorf("--command or --demo is required")
	}
	return scanOne(scanner, *command, *backend, *language, *audit)
}

func buildScanner(policyPath string) (*safety.Scanner, error) {
	if policyPath == "" {
		policyPath = filepath.Join("tool", "safety", "tool_safety_policy.yaml")
	}
	policy, err := safety.LoadPolicy(policyPath)
	if err != nil {
		return nil, fmt.Errorf("load policy %s: %w", policyPath, err)
	}
	return safety.NewScanner(policy), nil
}

func scanOne(scanner *safety.Scanner, command, backend, language, auditPath string) error {
	report := scanner.Scan(context.Background(), safety.ScanRequest{
		ToolName: "demo",
		Command:  command,
		Backend:  backend,
		Language: language,
	})
	if err := dumpReport(report); err != nil {
		return err
	}
	if err := scanner.Auditor().Flush(auditPath); err != nil {
		return fmt.Errorf("flush audit: %w", err)
	}
	fmt.Printf("audit events written to %s\n", auditPath)
	return nil
}

func dumpReport(report safety.ScanReport) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}

// demoCases exercise every risk family from the issue acceptance list.
var demoCases = []struct {
	name     string
	command  string
	backend  string
	language string
}{
	{"safe go test", "go test ./...", "workspaceexec", ""},
	{"dangerous delete", "rm -rf /", "workspaceexec", ""},
	{"read ssh private key", "cat ~/.ssh/id_rsa", "workspaceexec", ""},
	{"non-allowlisted egress", "curl https://evil.example.com/data", "workspaceexec", ""},
	{"allowlisted egress", "curl https://api.github.com/repos", "workspaceexec", ""},
	{"shell wrapper bypass", "sh -c 'echo hacked'", "workspaceexec", ""},
	{"piped bypass", "echo x | bash -c 'cat /etc/passwd'", "workspaceexec", ""},
	{"dependency install", "pip install malicious-package", "workspaceexec", ""},
	{"long sleep", "sleep 9999", "workspaceexec", ""},
	{"unbounded output", "cat /dev/zero", "workspaceexec", ""},
	{"hostexec long session", "tail -f /var/log/app.log", "hostexec", ""},
	{"tilde traversal", "cat ~root/../etc/shadow", "workspaceexec", ""},
	{"ssh hidden option", "ssh -oProxyCommand=evilprog github.com", "workspaceexec", ""},
	{"foreign code", "print('hello')", "codeexec", "python"},
}

// runDemo scans the built-in scenarios, prints a summary table, and then runs
// two scenarios through the agent's tool.PermissionPolicy hook to show the
// interception path (acceptance criterion 7).
func runDemo(scanner *safety.Scanner, auditPath string) error {
	fmt.Println("== tool/safety scanner demo ==")
	for _, c := range demoCases {
		report := scanner.Scan(context.Background(), safety.ScanRequest{
			ToolName: "demo",
			Command:  c.command,
			Backend:  c.backend,
			Language: c.language,
		})
		fmt.Printf("  %-28s -> %-6s %-8s rule=%-22s\n",
			c.name, report.Decision, report.RiskLevel, orDash(report.RuleID))
	}

	fmt.Println("\n== PermissionPolicy interception (called before every tool execution) ==")
	for _, c := range []struct{ name, toolName, args string }{
		{"dangerous", "workspace_exec", `{"command":"rm -rf /"}`},
		{"safe", "workspace_exec", `{"command":"echo hello"}`},
		{"hostexec long session", "host_exec", `{"command":"tail -f /var/log/app.log"}`},
	} {
		decision, err := scanner.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:  c.toolName,
			Arguments: []byte(c.args),
		})
		if err != nil {
			return fmt.Errorf("check permission: %w", err)
		}
		normalized, err := tool.NormalizePermissionDecision(decision)
		if err != nil {
			return fmt.Errorf("normalize permission decision: %w", err)
		}
		fmt.Printf("  %-24s -> action=%-6s reason=%q\n",
			c.name, normalized.Action, decision.Reason)
	}

	if err := scanner.Auditor().Flush(auditPath); err != nil {
		return fmt.Errorf("flush audit: %w", err)
	}
	fmt.Printf("\naudit events written to %s\n", auditPath)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
