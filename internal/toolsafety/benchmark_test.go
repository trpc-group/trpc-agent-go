// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety/checkers"
)

func generateLines(count int) string {
	cmds := []string{
		"echo 'hello world'",
		"ls -la",
		"cat /dev/null",
		"pwd",
		"date",
		"git status",
		"go build ./...",
		"python3 -c \"print('hi')\"",
		"curl https://api.github.com/repos",
		"sleep 1",
	}
	var b strings.Builder
	for i := 0; i < count; i++ {
		b.WriteString(cmds[i%len(cmds)])
		b.WriteString("\n")
	}
	return b.String()
}

func makeBenchScanner() *toolsafety.Scanner {
	policy := toolsafety.DefaultPolicy()
	scanner := toolsafety.NewScanner(policy)
	scanner.Add(checkers.NewDangerousCmdChecker(policy))
	scanner.Add(checkers.NewNetworkEgressChecker(policy))
	scanner.Add(checkers.NewShellBypassChecker())
	scanner.Add(checkers.NewResourceAbuseChecker(policy))
	scanner.Add(checkers.NewSensitiveLeakChecker(policy))
	scanner.Add(checkers.NewHostExecRiskChecker())
	return scanner
}

func BenchmarkScanner500Commands(b *testing.B) {
	scanner := makeBenchScanner()
	ctx := context.Background()
	lines := generateLines(500)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := scanner.Scan(ctx, &toolsafety.ScanRequest{
			ToolName: "bench",
			Command:  lines,
			Backend:  "workspaceexec",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScannerSingleCommand(b *testing.B) {
	scanner := makeBenchScanner()
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := scanner.Scan(ctx, &toolsafety.ScanRequest{
			ToolName: "bench",
			Command:  "curl https://api.github.com/repos",
			Backend:  "workspaceexec",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScannerDangerousCommand(b *testing.B) {
	scanner := makeBenchScanner()
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := scanner.Scan(ctx, &toolsafety.ScanRequest{
			ToolName: "bench",
			Command:  "rm -rf /",
			Backend:  "workspaceexec",
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestBenchmarkWithinLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark limit test in short mode")
	}

	scanner := makeBenchScanner()
	ctx := context.Background()
	lines := generateLines(500)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := scanner.Scan(ctx, &toolsafety.ScanRequest{
				ToolName: "bench",
				Command:  lines,
				Backend:  "workspaceexec",
			})
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	nsPerOp := result.NsPerOp()
	msPerOp := float64(nsPerOp) / 1_000_000
	t.Logf("500 commands: %.2f ms/op (%.0f ns/op)", msPerOp, float64(nsPerOp))

	if msPerOp > 1000 {
		t.Errorf("scan 500 commands took %.0f ms, want ≤ 1000 ms", msPerOp)
	}

	if nsPerOp > 0 {
		_ = fmt.Sprintf("ok")
	}
}
