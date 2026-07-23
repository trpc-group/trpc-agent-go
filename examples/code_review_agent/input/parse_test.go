//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input_test

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
)

// TestParseUnifiedDiff_HunksAndPackages verifies related behavior.
func TestParseUnifiedDiff_HunksAndPackages(t *testing.T) {
	raw := `diff --git a/pkg/worker/worker.go b/pkg/worker/worker.go
--- a/pkg/worker/worker.go
+++ b/pkg/worker/worker.go
@@ -1,5 +1,8 @@
 package worker
 
-func Start() {}
+func Start() {
+	go func() { doWork() }()
+}
`
	b, err := input.ParseUnifiedDiff("diff_file", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 {
		t.Fatalf("files=%d", len(b.Files))
	}
	f := b.Files[0]
	if f.Path != "pkg/worker/worker.go" {
		t.Fatalf("path=%s", f.Path)
	}
	if f.Language != "go" {
		t.Fatalf("lang=%s", f.Language)
	}
	if f.Package != "worker" {
		t.Fatalf("pkg=%s", f.Package)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("hunks=%d", len(f.Hunks))
	}
	var added int
	for _, l := range f.Hunks[0].Lines {
		if l.Kind == '+' {
			added++
			if l.NewLineNo <= 0 {
				t.Fatalf("missing new line no: %+v", l)
			}
		}
	}
	if added < 2 {
		t.Fatalf("added=%d", added)
	}
	if !strings.Contains(b.Summary, "1 files") {
		t.Fatalf("summary=%s", b.Summary)
	}
	if b.Digest == "" {
		t.Fatal("empty digest")
	}
}

// TestParseUnifiedDiff_Empty verifies related behavior.
func TestParseUnifiedDiff_Empty(t *testing.T) {
	b, err := input.ParseUnifiedDiff("diff_file", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 0 {
		t.Fatalf("want 0 files, got %d", len(b.Files))
	}
}
