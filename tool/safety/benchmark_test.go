//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"
	"time"
)

// BenchmarkScan measures the per-scan cost so reviewers can use it as a
// regression signal. The plan expects stable allocations and no per-rule
// shell reparsing.
func BenchmarkScan(b *testing.B) {
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		b.Fatal(err)
	}
	s := NewScanner(p)
	in := ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go test ./... && go vet ./... && go build ./...",
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Scan(ctx, in)
	}
}

// BenchmarkScanDangerous measures the cost of a command that fires
// multiple rules (dangerous delete + system write + not-allowed).
func BenchmarkScanDangerous(b *testing.B) {
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		b.Fatal(err)
	}
	s := NewScanner(p)
	in := ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "rm -rf /",
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Scan(ctx, in)
	}
}

// BenchmarkScanCodeBlock measures the cost of scanning a 500-line code
// block for unbounded loops and secrets.
func BenchmarkScanCodeBlock(b *testing.B) {
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		b.Fatal(err)
	}
	s := NewScanner(p)
	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, "print('line "+itoa(i)+"')")
	}
	in := ScanInput{
		ToolName:   "execute_code",
		Backend:    BackendCodeExec,
		CodeBlocks: []CodeBlock{{Language: "python", Code: joinLines(lines)}},
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Scan(ctx, in)
	}
}

// BenchmarkScanBatch500 measures the per-input amortized cost of a
// 500-command batch.
func BenchmarkScanBatch500(b *testing.B) {
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	if err != nil {
		b.Fatal(err)
	}
	s := NewScanner(p)
	inputs := make([]ScanInput, 0, 500)
	for i := 0; i < 500; i++ {
		inputs = append(inputs, ScanInput{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspaceExec,
			Command:  "echo " + itoa(i),
		})
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.ScanBatch(ctx, inputs)
	}
}

// Ensure time is referenced so the import is not flagged as unused when
// the benchmark file is built standalone.
var _ = time.Second
