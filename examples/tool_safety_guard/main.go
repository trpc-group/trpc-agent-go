//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demos tool/safety.Guard against the sample matrix in issue 2002.
package main

import (
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

	// PermissionPolicy never sees tool outputs. Show the host-side half:
	// RedactJSON / AfterToolRedact scrub results before they reach the model.
	fmt.Println()
	fmt.Println("output redact (host-side; wire via tool.Callbacks.RegisterAfterTool)")
	leaky := `{"stdout":"token=supersecretvalue123","ok":true}`
	fmt.Printf("  before: %s\n  after:  %s\n", leaky, safety.RedactJSON([]byte(leaky)))

	if mismatched > 0 {
		log.Fatalf("%d sample(s) mismatched expected action", mismatched)
	}
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
