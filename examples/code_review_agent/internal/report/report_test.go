//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestRenderReportsRedactSecrets(t *testing.T) {
	r := review.Report{
		Task:       review.ReviewTask{ID: "task-1", Status: review.TaskStatusPassed},
		Summary:    "token=supersecretvalue",
		Conclusion: "needs_human_review",
		Findings: []review.Finding{{
			Severity:       review.SeverityCritical,
			RuleID:         "security.secret_leak",
			File:           "pkg/config.go",
			Line:           4,
			Title:          "Secret",
			Evidence:       "password=supersecretvalue",
			Recommendation: "rotate",
		}},
	}
	jsonBytes, err := JSON(r)
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	mdBytes := Markdown(r)
	for _, data := range [][]byte{jsonBytes, mdBytes} {
		if strings.Contains(string(data), "supersecretvalue") {
			t.Fatalf("report leaked secret: %s", data)
		}
	}
}

func TestWriteReportsCreatesArtifacts(t *testing.T) {
	dir := t.TempDir()
	r := review.Report{Task: review.ReviewTask{ID: "task-1", Status: review.TaskStatusPassed}, Summary: "ok", Conclusion: "passed"}
	artifacts, err := Write(dir, r, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("len(artifacts) = %d, want 2", len(artifacts))
	}
	if _, err := os.Stat(filepath.Join(dir, "review_report.json")); err != nil {
		t.Fatalf("review_report.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "review_report.md")); err != nil {
		t.Fatalf("review_report.md missing: %v", err)
	}
}
