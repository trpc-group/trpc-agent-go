//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demos tool/safety.Guard against the sample matrix in issue 2002.
//
// Default path shown here (no model key required):
//  1. PermissionPolicy via Guard.CheckToolPermission
//  2. CommandLists() → the same allow/deny slices workspaceexec should use
//  3. AfterToolRedact callback on a leaky tool result
//  4. One JSONL audit line from the file auditor
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

type sample struct {
	Title string         `json:"title"`
	Tool  string         `json:"tool"`
	Args  map[string]any `json:"args"`
	Want  string         `json:"want"`
}

func main() {
	fmt.Println("tool/safety demo for issue 2002")
	fmt.Println("default wiring: PermissionPolicy + AfterToolRedact + audit + CommandLists")
	fmt.Println()

	if err := os.MkdirAll("output", 0o750); err != nil {
		log.Fatal(err)
	}

	auditPath := filepath.Join("output", "tool_safety_audit.jsonl")
	_ = os.Remove(auditPath) // fresh demo run; do not show stale fixture lines
	auditor, err := safety.NewFileAuditor(auditPath)
	if err != nil {
		log.Fatal(err)
	}

	guard := safety.NewGuard(
		safety.WithPolicyFile("tool_safety_policy.yaml"),
		safety.WithAuditor(auditor), // auto-wrapped AsyncAuditor on the hot path
	)

	// --- CommandLists ↔ workspaceexec (one source of truth) ---
	allow, deny := guard.Policy().CommandLists()
	fmt.Println("1) PermissionPolicy + dual lists")
	fmt.Println("   agent.WithToolPermissionPolicy(guard)")
	fmt.Printf("   CommandLists allow=%d deny=%d (feed workspaceexec spawn options)\n",
		len(allow), len(deny))
	fmt.Printf("   sample allow: %s\n", preview(allow, 5))
	fmt.Printf("   sample deny:  %s\n", preview(deny, 5))
	fmt.Println("   workspaceexec.NewExecTool(runner,")
	fmt.Println("       workspaceexec.WithAllowedCommands(allow...),")
	fmt.Println("       workspaceexec.WithDeniedCommands(deny...))")
	fmt.Println("   see tool/safety/DUAL_POLICY.md")
	fmt.Println()

	samples, err := loadSamples("tool_safety_samples.json")
	if err != nil {
		log.Fatal(err)
	}
	// Oversized stdin is awkward in committed JSON; append at runtime.
	samples = append(samples, sample{
		Title: "oversized stdin ask",
		Tool:  "workspace_exec",
		Args: map[string]any{
			"command": "cat",
			"stdin":   strings.Repeat("y", 2<<20),
		},
		Want: "ask",
	})

	ctx := context.Background()
	mismatched := 0
	fmt.Println("2) sample matrix (CheckToolPermission)")
	for i, s := range samples {
		raw, _ := json.Marshal(s.Args)
		dec, err := guard.CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:   s.Tool,
			ToolCallID: fmt.Sprintf("demo-%d", i+1),
			Arguments:  raw,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("[%d] %s\n  tool=%s action=%s want=%s reason=%s\n",
			i+1, s.Title, s.Tool, dec.Action, s.Want, dec.Reason)
		if s.Want != "" && string(dec.Action) != s.Want {
			mismatched++
			fmt.Printf("  MISMATCH: got %s want %s\n", dec.Action, s.Want)
		}
	}
	reports := guard.LastResults()
	if err := safety.WriteReportJSON(filepath.Join("output", "tool_safety_report.json"), reports); err != nil {
		log.Fatal(err)
	}
	if err := safety.WriteReportJSON("tool_safety_report.json", reports); err != nil {
		log.Fatal(err)
	}
	fmt.Println("wrote output/tool_safety_report.json and tool_safety_report.json")
	fmt.Println()

	// --- AfterToolRedact (host-side; PermissionPolicy never sees outputs) ---
	fmt.Println("3) AfterToolRedact (tool.Callbacks.RegisterAfterTool)")
	cbs := tool.NewCallbacks()
	cbs.RegisterAfterTool(safety.AfterToolRedact())
	leaky := `{"stdout":"token=supersecretvalue123","ok":true}`
	cb := safety.AfterToolRedact()
	out, err := cb(ctx, &tool.AfterToolArgs{
		ToolName:   "workspace_exec",
		ToolCallID: "demo-redact",
		Result:     leaky,
	})
	if err != nil {
		log.Fatal(err)
	}
	after := leaky
	if out != nil && out.CustomResult != nil {
		after = fmt.Sprint(out.CustomResult)
	}
	fmt.Printf("  before: %s\n  after:  %s\n", leaky, after)
	_ = cbs // shows the RegisterAfterTool wire-up site reviewers expect
	fmt.Println()

	// Drain async audit before reading the JSONL sample line.
	if err := guard.Close(); err != nil {
		log.Fatal(err)
	}

	fmt.Println("4) audit sample (first JSONL line)")
	line, err := firstLine(auditPath)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  %s\n", line)
	fmt.Printf("  (full log: %s)\n", auditPath)

	if mismatched > 0 {
		log.Fatalf("%d sample(s) mismatched expected action", mismatched)
	}
}

func preview(xs []string, n int) string {
	if len(xs) == 0 {
		return "(empty)"
	}
	if len(xs) < n {
		n = len(xs)
	}
	return strings.Join(xs[:n], ", ")
}

func firstLine(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("audit file empty: %s", path)
	}
	return sc.Text(), nil
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
