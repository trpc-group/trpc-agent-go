//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rules

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diffparser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

func TestScanSecretAndDedup(t *testing.T) {
	diff := []byte(`diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,5 @@
 package foo
 
+var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
+var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
`)
	files, err := diffparser.ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	result := Scan(files)
	if len(result.Findings) == 0 {
		t.Fatal("expected finding")
	}
	if result.Findings[0].RuleID != "SEC001" {
		t.Fatalf("rule=%s", result.Findings[0].RuleID)
	}
	if result.Findings[0].Evidence == `var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"` {
		t.Fatal("evidence was not redacted")
	}
}

func TestDeduplicateKeepsHigherSeverity(t *testing.T) {
	in := []review.Finding{
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001", Severity: review.SeverityLow, Confidence: 0.9},
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001", Severity: review.SeverityCritical, Confidence: 0.8},
	}
	out := Deduplicate(in)
	if len(out) != 1 {
		t.Fatalf("dedup len=%d", len(out))
	}
	if out[0].Severity != review.SeverityCritical {
		t.Fatalf("severity=%s", out[0].Severity)
	}
}
