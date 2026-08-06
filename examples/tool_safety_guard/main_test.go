//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestDemo_SampleMatrixMatchesWant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	auditor, err := safety.NewFileAuditor(auditPath)
	if err != nil {
		t.Fatal(err)
	}

	guard := safety.NewGuard(
		safety.WithPolicyFile("tool_safety_policy.yaml"),
		safety.WithAuditor(auditor),
	)
	t.Cleanup(func() { _ = guard.Close() })

	samples, err := loadSamples("tool_safety_samples.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) < 12 {
		t.Fatalf("expected >=12 samples, got %d", len(samples))
	}

	ctx := context.Background()
	for i, s := range samples {
		raw, err := json.Marshal(s.Args)
		if err != nil {
			t.Fatalf("%s: %v", s.Title, err)
		}
		dec, err := guard.CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:   s.Tool,
			ToolCallID: "test-" + s.Title,
			Arguments:  raw,
		})
		if err != nil {
			t.Fatalf("%s: %v", s.Title, err)
		}
		if s.Want != "" && string(dec.Action) != s.Want {
			t.Fatalf("[%d] %s: got %s want %s reason=%s", i, s.Title, dec.Action, s.Want, dec.Reason)
		}
	}

	reports := guard.LastResults()
	if len(reports) != len(samples) {
		t.Fatalf("reports=%d samples=%d", len(reports), len(samples))
	}
	for _, rep := range reports {
		if rep.Decision == "" || rep.RiskLevel == "" {
			t.Fatalf("incomplete report: %+v", rep)
		}
		if rep.Decision != safety.DecisionAllow {
			if rep.RuleID == "" || rep.Evidence == "" || rep.Advice == "" {
				t.Fatalf("non-allow report missing fields: %+v", rep)
			}
		}
	}

	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lines := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev safety.AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.ToolName == "" || ev.Decision == "" {
			t.Fatalf("incomplete audit event: %+v", ev)
		}
		lines++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if lines < len(samples) {
		t.Fatalf("audit lines=%d samples=%d", lines, len(samples))
	}
}

func TestDemo_CommandListsNonEmpty(t *testing.T) {
	t.Parallel()
	guard := safety.NewGuard(safety.WithPolicyFile("tool_safety_policy.yaml"))
	_, deny := guard.Policy().CommandLists()
	if len(deny) == 0 {
		t.Fatal("CommandLists deny is empty; DefaultPolicy denials should remain")
	}
}
