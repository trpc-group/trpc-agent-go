//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDiff_Empty(t *testing.T) {
	_, err := ParseUnifiedDiff("")
	assert.Error(t, err)
	_, err = ParseUnifiedDiff("   ")
	assert.Error(t, err)
}

func TestParseUnifiedDiff_SingleFile(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
index abc..def 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@
 package main
 
+import "fmt"
+
 func main() {
-	println("hello")
+	fmt.Println("hello")
 }
`
	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)

	f := files[0]
	assert.Equal(t, "main.go", f.File)
	assert.Equal(t, "modified", f.Status)
	assert.Equal(t, 3, f.Additions) // +import "fmt", + (empty), +fmt.Println
	assert.Equal(t, 1, f.Deletions) // -println
	require.Len(t, f.Hunks, 1)

	h := f.Hunks[0]
	assert.Equal(t, 1, h.OldStart)
	assert.Equal(t, 5, h.OldCount)
	assert.Equal(t, 1, h.NewStart)
	assert.Equal(t, 6, h.NewCount)
	assert.Contains(t, h.Lines[0], "package main")
}

func TestParseUnifiedDiff_MultipleFiles(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
index 1..2 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package a
 // old
+// new
diff --git a/b.go b/b.go
new file mode 100644
index 000..111
--- /dev/null
+++ b/b.go
@@ -0,0 +1,2 @@
+package b
+func B() {}
`
	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 2)

	assert.Equal(t, "a.go", files[0].File)
	assert.Equal(t, "modified", files[0].Status)
	assert.Equal(t, 1, files[0].Additions) // +// new
	assert.Equal(t, 0, files[0].Deletions) // "// old" is context line, not deletion

	assert.Equal(t, "b.go", files[1].File)
	assert.Equal(t, "added", files[1].Status)
	assert.Equal(t, 2, files[1].Additions) // +package b, +func B()
	assert.Equal(t, 0, files[1].Deletions)
}

func TestParseUnifiedDiff_DeletedFile(t *testing.T) {
	diff := `diff --git a/old.go b/old.go
deleted file mode 100644
index abc..000
--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package old
-func Old() bool {
-	return true
-}
`
	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "deleted", files[0].Status)
	assert.Equal(t, 4, files[0].Deletions) // -package old, -func Old() bool {, -\treturn true, -}
}

func TestParseUnifiedDiff_MultiHunk(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+// line 1
 func main() {}
@@ -10,5 +11,6 @@
 func foo() {
 	// old
+	// new
 }
`
	files, err := ParseUnifiedDiff(diff)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Len(t, files[0].Hunks, 2)

	assert.Equal(t, 1, files[0].Hunks[0].OldStart)
	assert.Equal(t, 10, files[0].Hunks[1].OldStart)
	assert.Equal(t, "main.go-hunk-0", files[0].Hunks[0].ID)
	assert.Equal(t, "main.go-hunk-1", files[0].Hunks[1].ID)
}

func TestPackageFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"main.go", ""},
		{"cmd/server/main.go", "cmd.server"},
		{"internal/handler/user.go", "internal.handler"},
		{"pkg/util/string.go", "pkg.util"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.expected, PackageFromPath(tt.path))
		})
	}
}

func TestIsTestFile(t *testing.T) {
	assert.True(t, IsTestFile("handler_test.go"))
	assert.True(t, IsTestFile("internal/handler_test.go"))
	assert.False(t, IsTestFile("handler.go"))
	assert.False(t, IsTestFile("main_test_helper.go"))
}

func TestHunkID(t *testing.T) {
	assert.Equal(t, "main.go-hunk-0", HunkID("main.go", 0))
	assert.Equal(t, "handler.go-hunk-3", HunkID("handler.go", 3))
}
