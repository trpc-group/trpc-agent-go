//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestGoldenReportsMatch(t *testing.T) {
	out := t.TempDir()
	_, jsonPath, mdPath, err := RunReview(testContext(t), ReviewConfig{
		Fixture:   "security_issue",
		OutputDir: out,
		DBPath:    filepath.Join(out, "review.sqlite"),
		DryRun:    true,
		Executor:  "fake",
	})
	if err != nil {
		t.Fatalf("RunReview() error = %v", err)
	}
	checkGoldenFile(t, "review_report.json", normalizeGoldenJSON(t, readFile(t, jsonPath)))
	checkGoldenFile(t, "review_report.md", normalizeGoldenMarkdown(readFile(t, mdPath)))
}

func checkGoldenFile(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := readFile(t, path)
	if !bytes.Equal(normalizeNewlines(got), normalizeNewlines(want)) {
		t.Fatalf("%s does not match golden\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func normalizeGoldenJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var report ReviewReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	report.Task.ID = "review-golden"
	report.Task.StartedAt = fixedGoldenTime()
	report.Task.EndedAt = fixedGoldenTime()
	for i := range report.SandboxRuns {
		report.SandboxRuns[i].ID = "run-golden"
		report.SandboxRuns[i].TaskID = "review-golden"
		report.SandboxRuns[i].StartedAt = fixedGoldenTime()
		report.SandboxRuns[i].DurationMS = 0
	}
	for i := range report.Artifacts {
		report.Artifacts[i].ID = "artifact-golden"
		report.Artifacts[i].TaskID = "review-golden"
		report.Artifacts[i].Path = filepath.Base(report.Artifacts[i].Path)
		report.Artifacts[i].SizeBytes = 0
		report.Artifacts[i].CreatedAt = fixedGoldenTime()
	}
	for i := range report.Permissions {
		report.Permissions[i].ID = "perm-golden"
		report.Permissions[i].TaskID = "review-golden"
		report.Permissions[i].CreatedAt = fixedGoldenTime()
	}
	report.Metrics.TotalDurationMS = 0
	report.Metrics.SandboxDurationMS = 0
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func normalizeGoldenMarkdown(raw []byte) []byte {
	out := string(raw)
	out = regexp.MustCompile(`review-[a-f0-9]+`).ReplaceAllString(out, "review-golden")
	out = regexp.MustCompile("Total duration: `[^`]+`").ReplaceAllString(out, "Total duration: `0ms`")
	out = regexp.MustCompile("Sandbox duration: `[^`]+`").ReplaceAllString(out, "Sandbox duration: `0ms`")
	return []byte(out)
}

func fixedGoldenTime() time.Time {
	return time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func normalizeNewlines(in []byte) []byte {
	return bytes.ReplaceAll(in, []byte("\r\n"), []byte("\n"))
}
