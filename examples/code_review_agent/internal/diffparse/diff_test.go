//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package diffparse

import "testing"

func TestParseLineNumbers(t *testing.T) {
	data := []byte("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -10,2 +10,3 @@ func f() {\n old\n-removed\n+added\n+more\n")
	files := mustParse(t, data)
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("files = %#v", files)
	}
	lines := files[0].Hunks[0].Lines
	if len(lines) != 4 {
		t.Fatalf("got %d lines", len(lines))
	}
	if lines[2].NewLine != 11 || lines[3].NewLine != 12 {
		t.Fatalf("added line numbers = %d, %d", lines[2].NewLine, lines[3].NewLine)
	}
	hunks, added := Stats(files)
	if hunks != 1 || added != 2 {
		t.Fatalf("Stats() = %d, %d", hunks, added)
	}
}

func TestParseRenameAndDelete(t *testing.T) {
	data := []byte("diff --git a/old.go b/new.go\nsimilarity index 100%\nrename from old.go\nrename to new.go\n--- a/old.go\n+++ b/new.go\n@@ -1 +1 @@\n-old\n+new\ndiff --git a/gone.go b/gone.go\ndeleted file mode 100644\n--- a/gone.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-gone\n")
	files := mustParse(t, data)
	if len(files) != 2 || !files[0].Renamed || !files[1].Deleted {
		t.Fatalf("files = %#v", files)
	}
}

func TestParseCopyIsNotRename(t *testing.T) {
	data := []byte("diff --git a/source.go b/copy.go\nsimilarity index 100%\ncopy from source.go\ncopy to copy.go\n--- a/source.go\n+++ b/copy.go\n")
	files := mustParse(t, data)
	if len(files) != 1 || files[0].Renamed {
		t.Fatalf("files = %#v", files)
	}
}

func TestParseChangedPathWithoutExtendedHeadersIsRename(t *testing.T) {
	data := []byte("diff --git a/old.go b/new.go\n--- a/old.go\n+++ b/new.go\n@@ -1 +1 @@\n-old\n+new\n")
	files := mustParse(t, data)
	if len(files) != 1 || !files[0].Renamed {
		t.Fatalf("files = %#v", files)
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse([]byte("not a diff")); err == nil {
		t.Fatal("Parse() error = nil")
	}
}

func TestParseGitBinaryPatch(t *testing.T) {
	data := []byte("diff --git a/image.bin b/image.bin\nnew file mode 100644\nindex 0000000..1234567\nGIT binary patch\nliteral 1\nIc$@\n")
	files := mustParse(t, data)
	if len(files) != 1 || !files[0].Binary || len(files[0].Hunks) != 0 {
		t.Fatalf("files = %#v", files)
	}
}

func mustParse(t *testing.T, data []byte) []ChangedFile {
	t.Helper()
	files, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return files
}
