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
)

func TestParseFindingsJSON(t *testing.T) {
	raw := `[{"severity":"high","category":"security","file":"a.go","line":3,"title":"SQL injection","evidence":"query","recommendation":"use params","confidence":0.9,"rule_id":"SEC-001"}]`
	items, err := ParseFindings(raw)
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
	items, err := ParseFindings(raw)
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0", len(items))
	}
}

func TestParseFindingsEmpty(t *testing.T) {
	items, err := ParseFindings("No issues found.")
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0", len(items))
	}
}

func TestParseFindingsRejectsEscapedFilePath(t *testing.T) {
	raw := `[{"severity":"critical","category":"security","file":"../../../etc/passwd","line":1,"title":"bad","evidence":"x","recommendation":"y","confidence":0.99,"rule_id":"SEC-999"}]`
	items, err := ParseFindings(raw)
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len = %d, want 0 for escaped file path", len(items))
	}
}

func TestParseFindingsClampsConfidence(t *testing.T) {
	raw := `[{"severity":"high","category":"security","file":"a.go","line":1,"title":"issue","evidence":"x","recommendation":"y","confidence":9.9,"rule_id":"SEC-001"}]`
	items, err := ParseFindings(raw)
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
