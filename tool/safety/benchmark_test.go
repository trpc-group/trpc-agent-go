//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"strings"
	"testing"
)

func BenchmarkScan500Commands(b *testing.B) {
	cmds := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		cmds = append(cmds, "echo ok")
	}
	req := Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  strings.Join(cmds, "; "),
	}
	scanner := NewScanner(DefaultPolicy())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = scanner.Scan(context.Background(), req)
	}
}

func BenchmarkScan500LineCodeBlock(b *testing.B) {
	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, "print('ok')")
	}
	req := Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     strings.Join(lines, "\n"),
		}},
	}
	scanner := NewScanner(DefaultPolicy())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = scanner.Scan(context.Background(), req)
	}
}
