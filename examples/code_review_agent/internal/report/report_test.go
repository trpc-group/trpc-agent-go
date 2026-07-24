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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	storemodel "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

const reportTestSecret = "password=report-secret-value"

func TestRenderParityAndRedaction(t *testing.T) {
	snapshot := Build(reportTestReview())
	documents, err := Render(snapshot)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Contains(string(documents.JSON), reportTestSecret) ||
		strings.Contains(string(documents.Markdown), reportTestSecret) {
		t.Fatal("rendered documents contain secret")
	}
	if redact.ContainsSecret(string(documents.Markdown)) {
		t.Fatalf("Markdown is not redaction-stable:\n%s\n--- redacted again ---\n%s", documents.Markdown, redact.String(string(documents.Markdown)))
	}
	var decoded Snapshot
	if err := json.Unmarshal(documents.JSON, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Summary.Findings != 1 || decoded.Summary.Warnings != 1 ||
		decoded.Summary.HumanReview != 1 || decoded.Governance.Blocked != 1 {
		t.Fatalf("report summary = %#v", decoded)
	}
	for _, expected := range []string{"Findings: 1", "Warnings: 1", "Needs human review: 1",
		"Blocked decisions: 1", "Severity distribution", "high: 1", "Error type distribution", `sandbox\_timeout: 1`} {
		if !strings.Contains(string(documents.Markdown), expected) {
			t.Fatalf("Markdown missing %q", expected)
		}
	}
	review := reportTestReview()
	review.Findings, review.Decisions, review.Runs, review.Artifacts = nil, nil, nil, nil
	documents, err = Render(Build(review))
	if err != nil {
		t.Fatalf("Render(empty) error = %v", err)
	}
	if strings.Contains(string(documents.JSON), ": null") {
		t.Fatalf("report contains null collection: %s", documents.JSON)
	}
	review = reportTestReview()
	review.Findings[0].Evidence = strings.Repeat("x", maxJSONBytes)
	if _, err := Render(Build(review)); err == nil {
		t.Fatal("Render(oversized) error = nil")
	}
}

func TestWriteAndRemoveReports(t *testing.T) {
	documents, err := Render(Build(reportTestReview()))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	outputDir := t.TempDir()
	written, err := Write(outputDir, documents)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	assertWrittenContent(t, written.JSONPath, documents.JSON)
	assertWrittenContent(t, written.MarkdownPath, documents.Markdown)
	if _, err := Write(outputDir, documents); err == nil {
		t.Fatal("Write(existing) error = nil")
	}
	if err := written.Remove(); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(written.JSONPath); !os.IsNotExist(err) {
		t.Fatalf("JSON report remains: %v", err)
	}
}

func TestWriteRemovesJSONWhenMarkdownCannotPublish(t *testing.T) {
	outputDir := t.TempDir()
	markdownPath := filepath.Join(outputDir, markdownFileName)
	if err := os.WriteFile(markdownPath, []byte("existing"), reportFileMode); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Write(outputDir, Documents{JSON: []byte("{}"), Markdown: []byte("report")}); err == nil {
		t.Fatal("Write() error = nil")
	}
	content, err := os.ReadFile(markdownPath)
	if err != nil || string(content) != "existing" {
		t.Fatalf("existing Markdown = %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, jsonFileName)); !os.IsNotExist(err) {
		t.Fatalf("partial JSON remains: %v", err)
	}
}

func TestRenderNeutralizesMarkdownStructure(t *testing.T) {
	const hostile = "unsafe\n# forged | `code` _emphasis_ ~~strike~~ <script>alert(1)</script> [REDACTED:named_secret:00000000](javascript:alert(1))"
	review := reportTestReview()
	review.Findings[0].Title = hostile
	review.Findings[0].File = hostile
	review.Decisions[0].Reason = hostile
	review.Artifacts[0].Path = hostile
	documents, err := Render(Build(review))
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	markdown := string(documents.Markdown)
	for _, unsafe := range []string{"\n# forged", "<script>", " | `code`"} {
		if strings.Contains(markdown, unsafe) {
			t.Fatalf("Markdown contains unsafe structure %q: %s", unsafe, markdown)
		}
	}
	if !strings.Contains(markdown, `\# forged \| \`+"`"+`code\`+"`"+` \_emphasis\_ \~\~strike\~\~ &lt;script&gt;`) ||
		!strings.Contains(markdown, `\[REDACTED:named_secret:00000000\]\(javascript:alert\(1\)\)`) {
		t.Fatalf("Markdown did not preserve escaped text: %s", markdown)
	}
	if !strings.Contains(markdown, `\[REDACTED:named_secret:`) {
		t.Fatalf("redaction marker is not safely visible: %s", markdown)
	}
	var decoded Snapshot
	if err := json.Unmarshal(documents.JSON, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Findings[0].Title != hostile || decoded.Governance.Decisions[0].Reason != hostile {
		t.Fatalf("JSON was Markdown-escaped: %#v", decoded)
	}
}

func TestWriteAtomicConcurrentNoClobber(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "report.md")
	const writers = 32
	var group sync.WaitGroup
	type result struct {
		content string
		err     error
	}
	results := make(chan result, writers)
	candidates := make(map[string]struct{}, writers)
	for index := 0; index < writers; index++ {
		content := fmt.Sprintf("writer-%d", index)
		candidates[content] = struct{}{}
		group.Add(1)
		go func(content string) {
			defer group.Done()
			results <- result{content: content, err: writeAtomic(target, []byte(content))}
		}(content)
	}
	group.Wait()
	close(results)
	succeeded := 0
	for result := range results {
		if result.err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful writers = %d", succeeded)
	}
	content, err := os.ReadFile(target)
	if _, ok := candidates[string(content)]; err != nil || !ok {
		t.Fatalf("published content = %q, %v", content, err)
	}
	temporary, err := filepath.Glob(filepath.Join(dir, ".review-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary reports = %v, %v", temporary, err)
	}
}

func reportTestReview() storemodel.Review {
	started := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	findings := []reviewmodel.Finding{
		reportFinding(findingSpec{reviewmodel.BucketFindings, "high", "security", "fix security"}),
		reportFinding(findingSpec{reviewmodel.BucketWarnings, "medium", "tests", "add tests"}),
		reportFinding(findingSpec{reviewmodel.BucketHumanReview, "low", "context", "review context"}),
	}
	return storemodel.Review{Task: storemodel.Task{ID: "task-1", Status: storemodel.StatusCompleted,
		InputKind: "fixture", InputDigest: "digest", StartedAt: started, FinishedAt: &finished,
		Conclusion: "changes requested"}, Input: storemodel.InputSummary{FileCount: 1, HunkCount: 1,
		AddedLines: 3, Packages: []string{"example/pkg"}}, Findings: findings,
		Decisions: []storemodel.Decision{{ID: "decision-1", Stage: "permission", CheckID: "go-test",
			Action: "deny", Reason: reportTestSecret, At: started}},
		Runs: []storemodel.SandboxRun{{ID: "run-1", CheckID: "go-test", Status: "passed", DurationMS: 10}},
		Metrics: storemodel.Metrics{TotalDurationMS: 20, SandboxDurationMS: 10, ToolCalls: 1,
			PermissionBlocks: 1, FindingCount: 3, SeverityCounts: map[string]int{"high": 1},
			ErrorTypeCounts: map[string]int{"sandbox_timeout": 1}},
		Artifacts: []storemodel.Artifact{{ID: "artifact-1", Kind: "check-result", Path: "result.json",
			SHA256: "digest", SizeBytes: 10, CreatedAt: finished}}}
}

type findingSpec struct {
	bucket         reviewmodel.Bucket
	severity       string
	category       string
	recommendation string
}

func reportFinding(spec findingSpec) reviewmodel.Finding {
	return reviewmodel.Finding{Bucket: spec.bucket, Severity: spec.severity, Category: spec.category,
		File: "sample.go", Line: 4, Title: "finding", Evidence: reportTestSecret,
		Recommendation: spec.recommendation, Confidence: 0.9, Source: "patch", RuleID: "RULE-1"}
}

func assertWrittenContent(t *testing.T, path string, expected []byte) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(content) != string(expected) {
		t.Fatalf("content mismatch for %q", path)
	}
}
