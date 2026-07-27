//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// TestWriteProducesBothArtifacts verifies JSON and markdown artifacts are written.
func TestWriteProducesBothArtifacts(t *testing.T) {
	dir := t.TempDir()
	artifacts, err := Write(dir, review.ReviewReport{
		Task:    review.ReviewTask{ID: "task-1", Status: review.StatusCompleted},
		Summary: "No high-confidence findings were detected.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("artifacts=%d, want 3", len(artifacts))
	}
	data, err := os.ReadFile(filepath.Join(dir, "review_report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"sha256"`) ||
		!strings.Contains(string(data), `"markdown_report"`) {
		t.Fatalf("json report does not contain complete artifact metadata: %s", data)
	}
	for _, a := range artifacts {
		if a.SHA256 == "" || a.SizeBytes <= 0 {
			t.Fatalf("artifact missing checksum or size: %+v", a)
		}
		if a.SizeBytes > maxArtifactBytes {
			t.Fatalf("artifact exceeds limit: %+v", a)
		}
		if _, err := os.Stat(a.Path); err != nil {
			t.Fatalf("artifact not written: %v", err)
		}
	}
}

func TestArtifactManifestMatchesPublishedReports(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, review.ReviewReport{
		Task: review.ReviewTask{ID: "task-manifest", Status: review.StatusCompleted},
	}); err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "artifact_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest []review.Artifact
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 2 {
		t.Fatalf("manifest entries=%d, want 2", len(manifest))
	}
	for _, entry := range manifest {
		body, err := os.ReadFile(entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != entry.SHA256 {
			t.Fatalf("%s checksum=%s, want %s", entry.Kind, got, entry.SHA256)
		}
		if int64(len(body)) != entry.SizeBytes {
			t.Fatalf("%s size=%d, want %d", entry.Kind, len(body), entry.SizeBytes)
		}
	}
}

// TestWriteRejectsOversizedReport verifies the artifact size cap is enforced.
func TestWriteRejectsOversizedReport(t *testing.T) {
	dir := t.TempDir()
	// Short words with spaces survive redaction, unlike one long token.
	big := strings.Repeat("very large summary ", maxArtifactBytes/19+2)
	_, err := Write(dir, review.ReviewReport{
		Task:    review.ReviewTask{ID: "task-big"},
		Summary: big,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds artifact limit") {
		t.Fatalf("expected artifact limit error, got %v", err)
	}
	// Nothing should be persisted when the limit rejects the report.
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected files written: %v", entries)
	}
}

// TestMarkdownIncludesFilterDecisions verifies filter decisions appear in markdown.
func TestMarkdownIncludesFilterDecisions(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, review.ReviewReport{
		Task: review.ReviewTask{ID: "task-2"},
		FilterDecisions: []review.FilterDecision{{
			RuleID: "SEC001", File: "a.go", Line: 3,
			Stage:    review.FilterStageConfidence,
			Decision: review.FilterDecisionKeep,
			Reason:   "confidence 0.96 >= 0.75 keeps the finding",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(dir, "review_report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "## Filter Decisions") ||
		!strings.Contains(string(md), "SEC001") {
		t.Fatalf("markdown missing filter decisions: %s", md)
	}
}
