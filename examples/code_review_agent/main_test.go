//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/orchestrator"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite checked-in golden reports")

func TestReviewRunWritesReportsAndStore(t *testing.T) {
	outDir := t.TempDir()
	result, err := orchestrator.Run(context.Background(), orchestrator.Options{
		FixtureDir: "testdata/fixtures",
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "fake",
		Now:        time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Report.Findings) == 0 {
		t.Fatalf("findings = 0, want fixture findings")
	}
	for _, path := range []string{result.JSONPath, result.MarkdownPath, result.DBPath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if strings.Contains(string(raw), "supersecretvalue") || strings.Contains(string(raw), "sk-live") {
			t.Fatalf("%s leaked a secret: %s", path, raw)
		}
	}
}

func TestCheckedInGoldenReportsMatch(t *testing.T) {
	outDir := t.TempDir()
	result, err := orchestrator.Run(context.Background(), orchestrator.Options{
		FixtureDir: "testdata/fixtures",
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "fake",
		Now:        time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	checkGolden(t, "review_report.json", result.JSONPath)
	checkGolden(t, "review_report.md", result.MarkdownPath)
}

func checkGolden(t *testing.T, name string, gotPath string) {
	t.Helper()
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("ReadFile(got %s) error = %v", name, err)
	}
	goldenPath := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(golden) error = %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0o600); err != nil {
			t.Fatalf("WriteFile(golden %s) error = %v", name, err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile(golden %s) error = %v", name, err)
	}
	if !bytes.Equal(normalizeNewlines(got), normalizeNewlines(want)) {
		t.Fatalf("%s does not match checked-in golden", name)
	}
}

func normalizeNewlines(in []byte) []byte {
	return bytes.ReplaceAll(in, []byte("\r\n"), []byte("\n"))
}
