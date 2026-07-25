//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package parser_test

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/parser"
)

func TestParseMultiFileDiff(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index abc..def 100644
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
 ctx
-old_foo
+new_foo
diff --git a/bar.go b/bar.go
index 123..456 100644
--- a/bar.go
+++ b/bar.go
@@ -5,3 +5,3 @@
 ctx
-old_bar
+new_bar
`
	files, err := parser.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(files), files)
	}
	if files[0].NewPath != "foo.go" || files[1].NewPath != "bar.go" {
		t.Errorf("wrong paths: %q %q", files[0].NewPath, files[1].NewPath)
	}
	if len(files[1].Hunks) == 0 {
		t.Error("bar.go has no hunks - multi-file parsing broken")
	}
}

func TestParseGitQuotedPath(t *testing.T) {
	// Git C-quotes paths with non-ASCII bytes; \303\251 is UTF-8 "é".
	diff := "diff --git \"a/\\303\\251.go\" \"b/\\303\\251.go\"\n" +
		"--- \"a/\\303\\251.go\"\n" +
		"+++ \"b/\\303\\251.go\"\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"+new\n"
	files, err := parser.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].NewPath != "é.go" {
		t.Errorf("quoted path not decoded: got %q, want %q", files[0].NewPath, "é.go")
	}
}
