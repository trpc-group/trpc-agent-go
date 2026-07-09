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
