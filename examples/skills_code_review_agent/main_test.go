//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIReportsSummary(t *testing.T) {
	outDir := t.TempDir()
	var out bytes.Buffer
	err := run([]string{
		"--fixture", "security_issue",
		"--dry-run",
		"--executor", "fake",
		"--output-dir", outDir,
		"--db", filepath.Join(outDir, "reviews.sqlite"),
	}, &out)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"task_id=review-",
		"json_report=" + filepath.Join(outDir, "review_report.json"),
		"markdown_report=" + filepath.Join(outDir, "review_report.md"),
		"findings=1 warnings=0 needs_human_review=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output missing %q:\n%s", want, text)
		}
	}
}

func TestRunCLIRejectsBadFlag(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--does-not-exist"}, &out); err == nil {
		t.Fatal("expected bad flag error")
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output on flag error, got %q", out.String())
	}
}
