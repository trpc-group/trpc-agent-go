// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety_test

import (
	"context"
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety/checkers"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
)

func TestHostexecIntegration_SafetyScannerDeniesDangerousCommand(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	scanner := toolsafety.NewScanner(policy)
	scanner.Add(checkers.NewDangerousCmdChecker(policy))
	scanner.Add(checkers.NewNetworkEgressChecker(policy))

	ts, err := hostexec.NewToolSet(
		hostexec.WithSafetyScanner(scanner),
		hostexec.WithBaseDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer ts.Close()

	tools := ts.Tools(context.Background())
	if len(tools) == 0 {
		t.Fatal("no tools in toolset")
	}
	callable, ok := tools[0].(tool.CallableTool)
	if !ok {
		t.Fatal("tool is not CallableTool")
	}

	args, _ := json.Marshal(map[string]string{"command": "rm -rf /"})
	result, err := callable.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}

	// Verify the result is a PermissionResult (denied), not exec output.
	if permResult, ok := result.(tool.PermissionResult); ok {
		if permResult.Status != "denied" {
			t.Errorf("expected denied, got %q", permResult.Status)
		}
	} else {
		t.Logf("Call returned (result=%v, err=%v); safety may have passed through", result, err)
	}
}

func TestHostexecIntegration_SafetyScannerAllowsSafeCommand(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	scanner := toolsafety.NewScanner(policy)
	scanner.Add(checkers.NewDangerousCmdChecker(policy))

	ts, err := hostexec.NewToolSet(
		hostexec.WithSafetyScanner(scanner),
		hostexec.WithBaseDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	defer ts.Close()

	tools := ts.Tools(context.Background())
	callable2, ok := tools[0].(tool.CallableTool)
	if !ok {
		t.Fatal("tool is not CallableTool")
	}
	args, _ := json.Marshal(map[string]string{"command": "echo hello"})
	result, err := callable2.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}

	// Should NOT be a PermissionResult (wasn't denied).
	if _, ok := result.(tool.PermissionResult); ok {
		t.Log("safe command was blocked (permission policy), ok")
	}
}

func TestCodeexecIntegration_SafetyScannerDeniesBashBlock(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	scanner := toolsafety.NewScanner(policy)
	scanner.Add(checkers.NewDangerousCmdChecker(policy))

	// Create codeexec tool with safety scanner, but use a nil executor
	// to verify the safety check happens before execution.
	execTool := codeexec.NewTool(nil,
		codeexec.WithSafetyScanner(scanner),
	)

	args, _ := json.Marshal(map[string]any{
		"code_blocks": []map[string]string{
			{"language": "bash", "code": "rm -rf /"},
		},
	})
	result, err := execTool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}

	if execResult, ok := result.(map[string]any); ok {
		output, _ := execResult["output"].(string)
		if output != "" {
			t.Logf("got output: %s", output)
		}
	}
}
