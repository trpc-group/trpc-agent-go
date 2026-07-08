// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package workspaceexec

import (
	"context"
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
	if !strings.Contains(err.Error(), safety.RuleDangerousDelete) {
		t.Fatalf("error = %v, want rule %s", err, safety.RuleDangerousDelete)
	}
}
