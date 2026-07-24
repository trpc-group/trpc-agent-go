//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmreview

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/diff"
)

func sampleDiff(t *testing.T) *diff.Diff {
	t.Helper()
	d, err := diff.ParseUnifiedDiff(`diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,1 +1,2 @@
 package a
+var X = 1
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return d
}

func TestParseFindingsJSON(t *testing.T) {
	raw := `[{"severity":"high","category":"security","file":"a.go","line":2,"title":"SQL injection","evidence":"query","recommendation":"use params","confidence":0.9,"rule_id":"SEC-001"}]`
	items, err := ParseFindings(raw, sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1", len(items))
	}
	if items[0].Source != "llm" {
		t.Fatalf("source = %q, want llm", items[0].Source)
	}
}

func TestParseFindingsCodeFence(t *testing.T) {
	raw := "```json\n[]\n```"
	items, err := ParseFindings(raw, sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0", len(items))
	}
}

func TestParseFindingsEmpty(t *testing.T) {
	items, err := ParseFindings("No issues found.", sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0", len(items))
	}
}

func TestParseFindingsRejectsEscapedFilePath(t *testing.T) {
	raw := `[{"severity":"critical","category":"security","file":"../../../etc/passwd","line":1,"title":"bad","evidence":"x","recommendation":"y","confidence":0.99,"rule_id":"SEC-999"}]`
	items, err := ParseFindings(raw, sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0 for escaped file path", len(items))
	}
}

func TestParseFindingsClampsConfidence(t *testing.T) {
	raw := `[{"severity":"high","category":"security","file":"a.go","line":2,"title":"issue","evidence":"x","recommendation":"y","confidence":9.9,"rule_id":"SEC-001"}]`
	items, err := ParseFindings(raw, sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len = %d, want 1", len(items))
	}
	if items[0].Confidence != 1 {
		t.Fatalf("confidence = %v, want 1", items[0].Confidence)
	}
}

func TestParseFindingsRejectsEmptyFileOrZeroLine(t *testing.T) {
	raw := `[
		{"severity":"high","category":"security","file":"","line":2,"title":"empty file","evidence":"x","recommendation":"y","confidence":0.9,"rule_id":"X"},
		{"severity":"high","category":"security","file":"a.go","line":0,"title":"zero line","evidence":"x","recommendation":"y","confidence":0.9,"rule_id":"Y"}
	]`
	items, err := ParseFindings(raw, sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0", len(items))
	}
}

func TestParseFindingsRejectsUnchangedLine(t *testing.T) {
	raw := `[{"severity":"high","category":"security","file":"a.go","line":1,"title":"unchanged","evidence":"x","recommendation":"y","confidence":0.99,"rule_id":"HALLUC","source":"rule"}]`
	items, err := ParseFindings(raw, sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0 for unchanged line", len(items))
	}
}

func TestParseFindingsForcesLLMSource(t *testing.T) {
	raw := `[{"severity":"high","category":"security","file":"a.go","line":2,"title":"ok","evidence":"x","recommendation":"y","confidence":0.9,"rule_id":"X","source":"rule"}]`
	items, err := ParseFindings(raw, sampleDiff(t))
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 1 || items[0].Source != "llm" {
		t.Fatalf("items = %+v", items)
	}
}

func TestParseFindingsMalformedJSON(t *testing.T) {
	_, err := ParseFindings(`[not-valid-json]`, sampleDiff(t))
	if err == nil {
		t.Fatal("expected decode error for malformed JSON")
	}
}

func TestParseFindingsPreservesTopLevelADir(t *testing.T) {
	d, err := diff.ParseUnifiedDiff(`diff --git a/a/service.go b/a/service.go
--- a/a/service.go
+++ b/a/service.go
@@ -1,1 +1,2 @@
 package a
+var X = 1
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw := `[{"severity":"low","category":"testing","file":"a/service.go","line":2,"title":"ok","evidence":"x","recommendation":"y","confidence":0.7,"rule_id":"T"}]`
	items, err := ParseFindings(raw, d)
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 1 || items[0].File != "a/service.go" {
		t.Fatalf("items = %+v", items)
	}
}
