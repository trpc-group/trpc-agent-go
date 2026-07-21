//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package reviewagent

import (
	"strings"
	"testing"
)

// TestParseModelReviewFencedJSON verifies fenced JSON model replies are parsed.
func TestParseModelReviewFencedJSON(t *testing.T) {
	content := "Here is my review:\n```json\n" +
		`{"summary":"one issue","findings":[{"severity":"HIGH","category":"Concurrency",` +
		`"file":"pkg/service/service.go","line":11,"title":"leak","evidence":"go func",` +
		`"recommendation":"add ctx","confidence":1.7,"rule_id":"LLM-CONC"}]}` +
		"\n```\nthanks"
	parsed, err := ParseModelReview(content, testFiles(), ModeLLM)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if parsed.Summary != "one issue" || len(parsed.Findings) != 1 {
		t.Fatalf("unexpected parse result: %+v", parsed)
	}
	f := parsed.Findings[0]
	if f.Severity != "high" || f.Category != "concurrency" {
		t.Fatalf("normalization failed: %+v", f)
	}
	if f.Confidence != 1 {
		t.Fatalf("confidence not clamped: %v", f.Confidence)
	}
	if f.Source != ModeLLM {
		t.Fatalf("source = %q", f.Source)
	}
}

// TestParseModelReviewDowngradesHallucinatedLocation verifies out-of-diff findings lose confidence.
func TestParseModelReviewDowngradesHallucinatedLocation(t *testing.T) {
	content := `{"summary":"","findings":[{"severity":"critical","category":"security",` +
		`"file":"not/in/diff.go","line":99,"title":"made up","evidence":"x",` +
		`"recommendation":"y","confidence":0.99,"rule_id":"LLM-SEC"}]}`
	parsed, err := ParseModelReview(content, testFiles(), ModeLLM)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got := parsed.Findings[0].Confidence; got > downgradedConfidence {
		t.Fatalf("hallucinated finding kept confidence %v", got)
	}
}

// TestParseModelReviewRedactsSecrets verifies secrets never survive parsing.
func TestParseModelReviewRedactsSecrets(t *testing.T) {
	content := `{"summary":"apiKey=sk-abcdefghijklmnopqrstuvwxyz123456 leaked",` +
		`"findings":[{"severity":"critical","category":"security",` +
		`"file":"pkg/service/service.go","line":11,"title":"secret",` +
		`"evidence":"apiKey=sk-abcdefghijklmnopqrstuvwxyz123456",` +
		`"recommendation":"rotate","confidence":0.9,"rule_id":"LLM-SEC"}]}`
	parsed, err := ParseModelReview(content, testFiles(), ModeLLM)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if strings.Contains(parsed.Summary, "sk-abcdefghijklmnopqrstuvwxyz123456") ||
		strings.Contains(parsed.Findings[0].Evidence, "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatal("secret leaked through model review parsing")
	}
}

// TestParseModelReviewRejectsNonJSON verifies non-JSON replies produce an error.
func TestParseModelReviewRejectsNonJSON(t *testing.T) {
	if _, err := ParseModelReview("no json here", testFiles(), ModeLLM); err == nil {
		t.Fatal("expected error for non-JSON reply")
	}
}
