// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package hostexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestExecCommandTool_SafetyScannerBlocksBeforeHostShell(t *testing.T) {
	scanner, err := safety.NewScanner(safety.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	set, err := NewToolSet(WithSafetyScanner(scanner))
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	var execTool tool.CallableTool
	for _, tl := range set.Tools(context.Background()) {
		if decl := tl.Declaration(); decl != nil && decl.Name == toolExecCommand {
			execTool = tl.(tool.CallableTool)
		}
	}
	if execTool == nil {
		t.Fatal("exec_command tool not found")
	}
	_, err = execTool.Call(context.Background(), []byte(`{
		"command": "rm -rf /tmp/project",
		"workdir": "."
	}`))
	if err == nil {
		t.Fatal("expected safety scanner to block host command")
	}
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("error = %v, want ErrBlocked", err)
	}
	if !strings.Contains(err.Error(), safety.RuleDangerousDelete) {
		t.Fatalf("error = %v, want rule %s", err, safety.RuleDangerousDelete)
	}
}

func TestExecCommandTool_SafetyScannerSanitizesOutput(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.BackendRules.HostExec.DefaultAction = safety.DecisionAllow
	policy.ResourceLimits.MaxOutputBytes = 24
	policy.ForbiddenPaths = []string{".env"}
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })
	set, err := NewToolSet(
		WithBaseDir(t.TempDir()),
		WithSafetyScanner(scanner),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	var execTool tool.CallableTool
	for _, tl := range set.Tools(context.Background()) {
		if decl := tl.Declaration(); decl != nil && decl.Name == toolExecCommand {
			execTool = tl.(tool.CallableTool)
		}
	}
	if execTool == nil {
		t.Fatal("exec_command tool not found")
	}
	got, err := execTool.Call(context.Background(), []byte(`{
		"command": "echo 012345678901234567890123456789",
		"timeout_sec": 1
	}`))
	if err != nil {
		t.Fatal(err)
	}
	output, _ := got.(map[string]any)["output"].(string)
	if len(output) > 24 || !strings.Contains(output, "[truncated]") {
		t.Fatalf("output = %q, want capped output", output)
	}
}

func TestExecCommandTool_SafetyScannerScansEffectiveDefaultTimeout(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.BackendRules.HostExec.DefaultAction = safety.DecisionAllow
	policy.ForbiddenPaths = []string{"/blocked/**"}
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })
	set, err := NewToolSet(
		WithBaseDir(t.TempDir()),
		WithSafetyScanner(scanner),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	var execTool tool.CallableTool
	for _, tl := range set.Tools(context.Background()) {
		if decl := tl.Declaration(); decl != nil && decl.Name == toolExecCommand {
			execTool = tl.(tool.CallableTool)
		}
	}
	if execTool == nil {
		t.Fatal("exec_command tool not found")
	}
	_, err = execTool.Call(context.Background(), []byte(`{"command":"echo ok"}`))
	if err == nil || !errors.Is(err, safety.ErrBlocked) ||
		!strings.Contains(err.Error(), safety.RuleResourceTimeout) {
		t.Fatalf("error = %v, want effective default timeout review", err)
	}
}

func TestExecCommandTool_SafetyScannerBlocksForbiddenWorkdir(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.BackendRules.HostExec.DefaultAction = safety.DecisionAllow
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })
	set, err := NewToolSet(WithSafetyScanner(scanner))
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	var execTool tool.CallableTool
	for _, tl := range set.Tools(context.Background()) {
		if decl := tl.Declaration(); decl != nil && decl.Name == toolExecCommand {
			execTool = tl.(tool.CallableTool)
		}
	}
	if execTool == nil {
		t.Fatal("exec_command tool not found")
	}
	_, err = execTool.Call(context.Background(), []byte(`{
		"command": "cat ssh_config",
		"workdir": "/etc/ssh"
	}`))
	if err == nil || !errors.Is(err, safety.ErrBlocked) ||
		!strings.Contains(err.Error(), safety.RuleForbiddenPath) {
		t.Fatalf("error = %v, want forbidden workdir block", err)
	}
}
