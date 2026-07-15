// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package workspaceexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestExecTool_SafetyScannerBlocksBeforeExecutor(t *testing.T) {
	scanner, err := safety.NewScanner(safety.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	tl := NewExecTool(nil, WithSafetyScanner(scanner))
	_, err = tl.Call(context.Background(), []byte(`{"command":"rm -rf /tmp/project"}`))
	if err == nil {
		t.Fatal("expected safety scanner to block command")
	}
	if !errors.Is(err, safety.ErrBlocked) {
		t.Fatalf("error = %v, want ErrBlocked", err)
	}
	if !strings.Contains(err.Error(), safety.RuleDangerousDelete) {
		t.Fatalf("error = %v, want rule %s", err, safety.RuleDangerousDelete)
	}
}

func TestExecTool_SafetyScannerSanitizesOutput(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.ResourceLimits.MaxOutputBytes = 20
	scanner, err := safety.NewScanner(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scanner.Close() })
	tool := &ExecTool{safetyScanner: scanner}
	out := tool.sanitizeOutput(execOutput{
		Output: "token=super-secret-value and trailing output",
	})
	if len(out.Output) > 20 {
		t.Fatalf("output length = %d, want <= 20", len(out.Output))
	}
	if strings.Contains(out.Output, "super-secret-value") {
		t.Fatalf("output leaked secret: %q", out.Output)
	}
}
