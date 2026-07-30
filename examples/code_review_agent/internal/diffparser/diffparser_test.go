package diffparser

import (
	"testing"
)

func TestParse_SingleFileAddition(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
new file mode 100644
index 0000000..abcdefg
--- /dev/null
+++ b/main.go
@@ -0,0 +1,5 @@
+package main
+
+import "fmt"
+
+func main() {
+	fmt.Println("hello")
+}
`
	changes, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 file change, got %d", len(changes))
	}
	c := changes[0]
	if c.FilePath != "main.go" {
		t.Errorf("FilePath = %q, want \"main.go\"", c.FilePath)
	}
	if c.Language != "go" {
		t.Errorf("Language = %q, want \"go\"", c.Language)
	}
	if len(c.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(c.Hunks))
	}
	h := c.Hunks[0]
	if h.OldStart != 0 || h.NewStart != 1 || h.NewCount != 5 {
		t.Errorf("hunk header: old=%d new=%d count=%d", h.OldStart, h.NewStart, h.NewCount)
	}
	addedCount := 0
	for _, l := range h.Lines {
		if l.Type == "+" {
			addedCount++
		}
	}
	// 7 "+" lines: package, blank, import, blank, func, body, closing brace
	if addedCount != 7 {
		t.Errorf("expected 7 added lines, got %d", addedCount)
	}
}

func TestParse_MultiFileWithHunks(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index 1111111..2222222 100644
--- a/foo.go
+++ b/foo.go
@@ -10,6 +10,8 @@ package foo
 func OldFunc() string {
 	return "old"
 }
+func NewFunc() string {
+	return "new"
+}

diff --git a/bar.go b/bar.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/bar.go
@@ -0,0 +1,3 @@
+package bar
+
+var X = 1
`
	changes, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 file changes, got %d", len(changes))
	}
	if changes[0].FilePath != "foo.go" {
		t.Errorf("file 0 = %q, want foo.go", changes[0].FilePath)
	}
	if changes[1].FilePath != "bar.go" {
		t.Errorf("file 1 = %q, want bar.go", changes[1].FilePath)
	}
}

func TestParse_EmptyDiff(t *testing.T) {
	changes, err := Parse("")
	if err != nil {
		t.Fatalf("Parse empty diff failed: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for empty diff, got %d", len(changes))
	}
}

func TestParse_PackageInference(t *testing.T) {
	diff := `diff --git a/internal/foo/bar.go b/internal/foo/bar.go
new file mode 100644
index 0000000..abcdefg
--- /dev/null
+++ b/internal/foo/bar.go
@@ -0,0 +1,3 @@
+package foo
+
+func DoStuff() {}
`
	changes, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	// PackageName is inferred from the directory containing the .go file.
	// For "internal/foo/bar.go", the directory is "foo".
	if changes[0].PackageName != "foo" {
		t.Errorf("PackageName = %q, want \"foo\"", changes[0].PackageName)
	}
}

func TestParse_LineTypes(t *testing.T) {
	diff := `diff --git a/x.go b/x.go
index 1111111..2222222 100644
--- a/x.go
+++ b/x.go
@@ -1,4 +1,5 @@
 package x

-func Old() {}
+func New() {}
+
 func Keep() {}
`
	changes, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(changes) != 1 || len(changes[0].Hunks) != 1 {
		t.Fatal("expected 1 file with 1 hunk")
	}
	h := changes[0].Hunks[0]
	types := map[string]int{}
	for _, l := range h.Lines {
		types[l.Type]++
	}
	if types["-"] != 1 {
		t.Errorf("expected 1 removed line, got %d", types["-"])
	}
	// 2 "+" lines: func New() and the trailing blank line
	if types["+"] != 2 {
		t.Errorf("expected 2 added lines, got %d", types["+"])
	}
}
