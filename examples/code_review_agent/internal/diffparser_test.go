//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDiff_SimpleFile(t *testing.T) {
	diff := `diff --git a/foo/bar.go b/foo/bar.go
index 1234567..89abcde 100644
--- a/foo/bar.go
+++ b/foo/bar.go
@@ -10,6 +10,8 @@ package foo
 import "fmt"

 func main() {
+	fmt.Println("hello")
+	fmt.Println("world")
 }
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	f := files[0]
	require.Equal(t, "foo/bar.go", f.Path)
	require.False(t, f.IsNew)
	require.False(t, f.IsDeleted)

	require.Len(t, f.Hunks, 1)
	h := f.Hunks[0]
	require.Equal(t, 10, h.OldStart)
	require.Equal(t, 10, h.NewStart)

	addedLines := 0
	for _, l := range h.Lines {
		if l.Type == LineAdded {
			addedLines++
		}
	}
	require.Equal(t, 2, addedLines)
}

func TestParseDiff_NewFile(t *testing.T) {
	diff := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package main
+
+func main() {}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].IsNew)
	require.Equal(t, "new.go", files[0].Path)
}

func TestParseDiff_DeletedFile(t *testing.T) {
	diff := `diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func main() {}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].IsDeleted)
}

func TestParseDiff_MultipleHunks(t *testing.T) {
	diff := `diff --git a/multi.go b/multi.go
index 1234567..89abcde 100644
--- a/multi.go
+++ b/multi.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"

@@ -10,3 +11,4 @@ func b() {
 func c() {
 }
+func d() {}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Len(t, files[0].Hunks, 2)
}

func TestParseDiff_FromFile(t *testing.T) {
	diff := `diff --git a/foo/bar.go b/foo/bar.go
--- a/foo/bar.go
+++ b/foo/bar.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {}
`
	// Write to temp file and read back.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.diff")
	require.NoError(t, os.WriteFile(path, []byte(diff), 0o644))

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	files, err := ParseDiff(f)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "foo/bar.go", files[0].Path)
}

func TestParseDiff_Rename(t *testing.T) {
	diff := `diff --git a/old.go b/new.go
rename from old.go
rename to new.go
--- a/old.go
+++ b/new.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {}
`
	files, err := ParseDiffString(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].IsRename)
	require.Equal(t, "old.go", files[0].OldPath)
	require.Equal(t, "new.go", files[0].Path)
}

func TestDiffSummary(t *testing.T) {
	files := []DiffFile{
		{
			Path: "a.go",
			Hunks: []DiffHunk{{
				Lines: []DiffLine{
					{Type: LineAdded, Content: "a"},
					{Type: LineAdded, Content: "b"},
					{Type: LineRemoved, Content: "c"},
				},
			}},
		},
	}
	summary := DiffSummary(files)
	require.Contains(t, summary, "1 files")
	require.Contains(t, summary, "+2")
	require.Contains(t, summary, "-1")
}

func TestDiffSummaryCountsDeletedLines(t *testing.T) {
	files := []DiffFile{{
		Path:      "deleted.go",
		IsDeleted: true,
		Hunks: []DiffHunk{{Lines: []DiffLine{
			{Type: LineRemoved, Content: "package old"},
			{Type: LineRemoved, Content: "func removed() {}"},
		}}},
	}}
	require.Equal(t, "1 files, +0 -2", DiffSummary(files))
}

func TestChangedGoFiles(t *testing.T) {
	files := []DiffFile{
		{Path: "foo.go", Hunks: []DiffHunk{{Lines: []DiffLine{{Type: LineAdded}}}}},
		{Path: "bar.txt", Hunks: []DiffHunk{{Lines: []DiffLine{{Type: LineAdded}}}}},
		{Path: "baz.go", IsDeleted: true},
	}
	goFiles := ChangedGoFiles(files)
	require.Len(t, goFiles, 1)
	require.Equal(t, "foo.go", goFiles[0].Path)
}
