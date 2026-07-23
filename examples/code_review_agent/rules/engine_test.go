//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules_test

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/rules"
)

func TestDedup(t *testing.T) {
	in := []review.Finding{
		{File: "a.go", Line: 1, RuleID: "CR-CON-001", Confidence: 0.8, Evidence: "short"},
		{File: "a.go", Line: 1, RuleID: "CR-CON-001", Confidence: 0.9, Evidence: "longer evidence"},
		{File: "b.go", Line: 2, RuleID: "CR-CON-001", Confidence: 0.7, Evidence: "other"},
	}
	out := rules.Dedup(in)
	if len(out) != 2 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0].Confidence != 0.9 {
		t.Fatalf("confidence=%v", out[0].Confidence)
	}
}

func TestClassify(t *testing.T) {
	in := []review.Finding{
		{Confidence: 0.9},
		{Confidence: 0.5},
		{Confidence: 0.2},
	}
	f, w := rules.Classify(in, 0.75)
	if len(f) != 1 || len(w) != 1 {
		t.Fatalf("findings=%d warnings=%d", len(f), len(w))
	}
}

func TestEngine_Goroutine(t *testing.T) {
	raw := `diff --git a/pkg/worker/worker.go b/pkg/worker/worker.go
--- a/pkg/worker/worker.go
+++ b/pkg/worker/worker.go
@@ -1,3 +1,6 @@
 package worker
+func Start() {
+	go func() { doWork() }()
+}
`
	b, err := input.ParseUnifiedDiff("fixture", raw)
	if err != nil {
		t.Fatal(err)
	}
	out := rules.Engine{}.Analyze(b)
	found := false
	for _, f := range out {
		if f.RuleID == "CR-CON-001" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing CR-CON-001: %+v", out)
	}
}

func TestEngine_ErrorHandling(t *testing.T) {
	raw := `diff --git a/pkg/svc/svc.go b/pkg/svc/svc.go
--- a/pkg/svc/svc.go
+++ b/pkg/svc/svc.go
@@ -1,3 +1,8 @@
 package svc
+func Run() {
+	_ = doWork()
+	tx, _ := db.Begin()
+	_ = tx
+}
`
	b, err := input.ParseUnifiedDiff("fixture", raw)
	if err != nil {
		t.Fatal(err)
	}
	out := rules.Engine{}.Analyze(b)
	n := 0
	for _, f := range out {
		if f.RuleID == "CR-ERR-001" {
			n++
		}
	}
	if n < 2 {
		t.Fatalf("expected >=2 CR-ERR-001, got %d: %+v", n, out)
	}
}

func TestEngine_ResourceCloseNearby(t *testing.T) {
	raw := "diff --git a/pkg/fileutil/read.go b/pkg/fileutil/read.go\n" +
		"--- a/pkg/fileutil/read.go\n" +
		"+++ b/pkg/fileutil/read.go\n" +
		"@@ -1,3 +1,10 @@\n" +
		" package fileutil\n" +
		"+import \"os\"\n" +
		"+func ReadAll(path string) ([]byte, error) {\n" +
		"+\tf, err := os.Open(path)\n" +
		"+\tif err != nil { return nil, err }\n" +
		"+\tdefer f.Close()\n" +
		"+\treturn nil, nil\n" +
		"+}\n"
	b, err := input.ParseUnifiedDiff("fixture", raw)
	if err != nil {
		t.Fatal(err)
	}
	out := rules.Engine{}.Analyze(b)
	for _, f := range out {
		if f.RuleID == "CR-RES-001" {
			t.Fatalf("unexpected CR-RES-001 when Close is nearby: %+v", f)
		}
	}
}
