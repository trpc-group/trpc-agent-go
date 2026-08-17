//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const benchmarkOutput = `goos: linux
goarch: amd64
pkg: example.com/project/processor
cpu: benchmark
BenchmarkStable/history=16-8       100  10 ns/op  0 B/op  0 allocs/op
BenchmarkStable/history=256-8      100  11 ns/op  0 B/op  0 allocs/op
BenchmarkBounded/history=256-8     100  50 ns/op  2048 B/op  7 allocs/op
PASS
`

func TestRunPassesBudgetsAndInvariants(t *testing.T) {
	inputPath := writeTestFile(t, "benchmark.txt", benchmarkOutput)
	budgetPath := writeTestFile(t, "budgets.json", `{
  "version": 1,
  "benchmarks": [
    {
      "package": "example.com/project/processor",
      "name": "BenchmarkStable/history=16",
      "max_bytes_per_op": 0,
      "max_allocs_per_op": 0
    },
    {
      "package": "example.com/project/processor",
      "name": "BenchmarkBounded/history=256",
      "max_bytes_per_op": 4096,
      "max_allocs_per_op": 8
    }
  ],
  "invariants": [
    {
      "name": "stable history cost",
      "package": "example.com/project/processor",
      "baseline": "BenchmarkStable/history=16",
      "candidate": "BenchmarkStable/history=256",
      "max_bytes_delta": 0,
      "max_allocs_delta": 0
    }
  ]
}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-input", inputPath, "-budgets", budgetPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "passed (2 budgets, 1 invariants)") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestRunReportsBudgetViolation(t *testing.T) {
	inputPath := writeTestFile(t, "benchmark.txt", benchmarkOutput)
	budgetPath := writeTestFile(t, "budgets.json", `{
  "version": 1,
  "benchmarks": [{
    "package": "example.com/project/processor",
    "name": "BenchmarkBounded/history=256",
    "max_bytes_per_op": 1024,
    "max_allocs_per_op": 6
  }]
}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-input", inputPath, "-budgets", budgetPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	for _, want := range []string{"2048 B/op", "7 allocs/op"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected %q in %s", want, stderr.String())
		}
	}
}

func TestRunReportsMissingBenchmark(t *testing.T) {
	inputPath := writeTestFile(t, "benchmark.txt", benchmarkOutput)
	budgetPath := writeTestFile(t, "budgets.json", `{
  "version": 1,
  "benchmarks": [{
    "package": "example.com/project/processor",
    "name": "BenchmarkMissing",
    "max_bytes_per_op": 0
  }]
}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-input", inputPath, "-budgets", budgetPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "missing benchmark") {
		t.Fatalf("unexpected error: %s", stderr.String())
	}
}

func TestRunReportsInvariantViolation(t *testing.T) {
	inputPath := writeTestFile(t, "benchmark.txt", strings.Replace(
		benchmarkOutput,
		"BenchmarkStable/history=256-8      100  11 ns/op  0 B/op  0 allocs/op",
		"BenchmarkStable/history=256-8      100  11 ns/op  64 B/op  1 allocs/op",
		1,
	))
	budgetPath := writeTestFile(t, "budgets.json", `{
  "version": 1,
  "invariants": [{
    "name": "stable history cost",
    "package": "example.com/project/processor",
    "baseline": "BenchmarkStable/history=16",
    "candidate": "BenchmarkStable/history=256",
    "max_bytes_delta": 0,
    "max_allocs_delta": 0
  }]
}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-input", inputPath, "-budgets", budgetPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	for _, want := range []string{"grows by 64 B/op", "grows by 1 allocs/op"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected %q in %s", want, stderr.String())
		}
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	inputPath := writeTestFile(t, "benchmark.txt", benchmarkOutput)
	budgetPath := writeTestFile(t, "budgets.json", `{"version": 2}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-input", inputPath, "-budgets", budgetPath}, &stdout, &stderr); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unsupported config version") {
		t.Fatalf("unexpected error: %s", stderr.String())
	}
}

func writeTestFile(t *testing.T, name string, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
