//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package diffparse

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSimpleDiff(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
index a1b2c3d..e4f5g6h 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 package main
 
-func greet() string {
-	return "hello"
+func greet(name string) string {
+	return "hello " + name
 }
`
	pd, err := Parse(diff)
	require.NoError(t, err)
	require.Len(t, pd.Files, 1)

	f := pd.Files[0]
	assert.Equal(t, "main.go", f.OldPath)
	assert.Equal(t, "main.go", f.NewPath)
	assert.False(t, f.Deleted)
	assert.False(t, f.NewFile)
	assert.Len(t, f.Hunks, 1)

	h := f.Hunks[0]
	assert.Equal(t, 1, h.OldStart)
	assert.Equal(t, 3, h.OldCount)
	assert.Equal(t, 1, h.NewStart)
	assert.Equal(t, 5, h.NewCount)
}

func TestParseDeletedFile(t *testing.T) {
	diff := `diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,5 +0,0 @@
-package main
-
-func oldFunc() {
-}
`
	pd, err := Parse(diff)
	require.NoError(t, err)
	require.Len(t, pd.Files, 1)

	f := pd.Files[0]
	assert.Equal(t, "old.go", f.OldPath)
	assert.True(t, f.Deleted)
}

func TestParseNewFile(t *testing.T) {
	diff := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package main
+
+func newFunc() {}
`
	pd, err := Parse(diff)
	require.NoError(t, err)
	require.Len(t, pd.Files, 1)

	f := pd.Files[0]
	assert.Equal(t, "new.go", f.NewPath)
	assert.True(t, f.NewFile)
	assert.Len(t, f.Hunks, 1)
}

func TestParseRenamedFile(t *testing.T) {
	diff := `diff --git a/old_name.go b/new_name.go
rename from old_name.go
rename to new_name.go
--- a/old_name.go
+++ b/new_name.go
@@ -1,3 +1,3 @@
 package main
 
 func hello() {}
`
	pd, err := Parse(diff)
	require.NoError(t, err)
	require.Len(t, pd.Files, 1)

	f := pd.Files[0]
	assert.Equal(t, "old_name.go", f.OldPath)
	assert.Equal(t, "new_name.go", f.NewPath)
	assert.True(t, f.Renamed)
	assert.False(t, f.Deleted)
}

func TestParseMultipleHunks(t *testing.T) {
	diff := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 
-func main() {
+func main() {
+	fmt.Println("hello")
 }
@@ -10,3 +11,5 @@
-func helper() {
+func helper(v string) {
+	return v
 }
`
	pd, err := Parse(diff)
	require.NoError(t, err)
	require.Len(t, pd.Files, 1)
	assert.Len(t, pd.Files[0].Hunks, 2)
}

func TestParseCopiedFile(t *testing.T) {
	diff := `diff --git a/original.go b/copy.go
copy from original.go
copy to copy.go
--- a/original.go
+++ b/copy.go
@@ -1,3 +1,3 @@
 package main
 
 func hello() {}
`
	pd, err := Parse(diff)
	require.NoError(t, err)
	require.Len(t, pd.Files, 1)

	f := pd.Files[0]
	assert.Equal(t, "original.go", f.OldPath)
	assert.Equal(t, "copy.go", f.NewPath)
	assert.False(t, f.Renamed, "copied files should not be marked as renamed")
}

func TestAddedLines(t *testing.T) {
	cf := ChangedFile{
		NewPath: "test.go",
		Hunks: []Hunk{
			{
				Lines: []ChangedLine{
					{Kind: "+", NewLine: 1, Content: "package main"},
					{Kind: " ", NewLine: 2, Content: ""},
					{Kind: "-", OldLine: 1, Content: "// old comment"},
					{Kind: "+", NewLine: 3, Content: "// new comment"},
				},
			},
		},
	}
	added := AddedLines(cf)
	assert.Len(t, added, 2)
}

func TestAllAddedLines(t *testing.T) {
	pd := &ParsedDiff{
		Files: []ChangedFile{
			{
				NewPath: "file1.go",
				Hunks: []Hunk{
					{Lines: []ChangedLine{
						{Kind: "+", NewLine: 1, Content: "line1"},
						{Kind: "+", NewLine: 2, Content: "line2"},
					}},
				},
			},
			{
				NewPath: "file2.go",
				Hunks: []Hunk{
					{Lines: []ChangedLine{
						{Kind: "+", NewLine: 1, Content: "line3"},
					}},
				},
			},
		},
	}
	result := AllAddedLines(pd)
	assert.Len(t, result, 2)
	assert.Len(t, result["file1.go"], 2)
	assert.Len(t, result["file2.go"], 1)
}

func TestParseEmptyDiff(t *testing.T) {
	pd, err := Parse("")
	require.NoError(t, err)
	assert.Empty(t, pd.Files)
}

func TestParseRealFixture(t *testing.T) {
	diff := `diff --git a/handler.go b/handler.go
new file mode 100644
--- /dev/null
+++ b/handler.go
@@ -0,0 +1,15 @@
+package main
+
+import (
+	"net/http"
+	"os/exec"
+)
+
+func handleRequest(w http.ResponseWriter, r *http.Request) {
+	cmd := r.URL.Query().Get("cmd")
+	exec.Command("/bin/sh", "-c", cmd).Run()
+}
+
+func init() {
+	apiKey := "sk-1234567890abcdef"
+}`
	pd, err := Parse(diff)
	require.NoError(t, err)
	require.Len(t, pd.Files, 1)
	assert.Equal(t, "handler.go", pd.Files[0].NewPath)
}
