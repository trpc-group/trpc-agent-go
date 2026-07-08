package parser

import (
	"testing"
)

func TestParseDiff(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,10 @@
+package utils
+
+func Add(a, b int) int {
+    return a + b
+}
+
+func Multiply(a, b int) int {
+    return a * b
+}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(result.Files))
	}

	if result.Files[0].NewPath != "pkg/utils/helper.go" {
		t.Errorf("Expected path 'pkg/utils/helper.go', got '%s'", result.Files[0].NewPath)
	}

	if result.Files[0].IsNewFile != true {
		t.Error("Expected file to be new")
	}

	if result.TotalAdded != 9 {
		t.Errorf("Expected 9 added lines, got %d", result.TotalAdded)
	}

	if result.TotalRemoved != 0 {
		t.Errorf("Expected 0 removed lines, got %d", result.TotalRemoved)
	}
}

func TestParseDiffWithHunks(t *testing.T) {
	diffContent := `diff --git a/pkg/worker/worker.go b/pkg/worker/worker.go
index abc1234..def5678 100644
--- a/pkg/worker/worker.go
+++ b/pkg/worker/worker.go
@@ -5,7 +5,10 @@ package worker
 
 func StartWorker() {
     for i := 0; i < 10; i++ {
-        go func() {
+        go func(id int) {
+            fmt.Printf("Worker %d started\n", id)
             time.Sleep(time.Second)
+            fmt.Printf("Worker %d finished\n", id)
         }(i)
     }
 }
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	if len(result.Files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(result.Files))
	}

	file := result.Files[0]
	if len(file.Hunks) != 1 {
		t.Errorf("Expected 1 hunk, got %d", len(file.Hunks))
	}

	hunk := file.Hunks[0]
	if hunk.OldStart != 5 {
		t.Errorf("Expected old start 5, got %d", hunk.OldStart)
	}

	if hunk.NewStart != 5 {
		t.Errorf("Expected new start 5, got %d", hunk.NewStart)
	}

	if len(hunk.AddedLines) != 3 {
		t.Errorf("Expected 3 added lines, got %d", len(hunk.AddedLines))
	}
}

func TestFilterGoFiles(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,5 @@
+package utils
+
diff --git a/README.md b/README.md
index abc1234..def5678 100644
--- a/README.md
+++ b/README.md
@@ -1,3 +1,4 @@
 # Project
+New line
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	goFiles := FilterGoFiles(result.Files)
	if len(goFiles) != 1 {
		t.Errorf("Expected 1 Go file, got %d", len(goFiles))
	}

	if goFiles[0].NewPath != "pkg/utils/helper.go" {
		t.Errorf("Expected 'pkg/utils/helper.go', got '%s'", goFiles[0].NewPath)
	}
}

func TestGetChangedLines(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,5 @@
+package utils
+
+func Add(a, b int) int {
+    return a + b
+}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	changedLines := GetChangedLines(result)
	if len(changedLines) != 1 {
		t.Errorf("Expected 1 file with changed lines, got %d", len(changedLines))
	}

	lines, ok := changedLines["pkg/utils/helper.go"]
	if !ok {
		t.Error("Expected 'pkg/utils/helper.go' in changed lines")
	}

	if len(lines) != 5 {
		t.Errorf("Expected 5 changed lines, got %d", len(lines))
	}
}

func TestFormatDiffForReview(t *testing.T) {
	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,5 @@
+package utils
+
+func Add(a, b int) int {
+    return a + b
+}
`

	result, err := ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	formatted := FormatDiffForReview(result)
	if formatted == "" {
		t.Error("Expected non-empty formatted output")
	}

	if len(formatted) < 10 {
		t.Errorf("Expected formatted output to be at least 10 chars, got %d", len(formatted))
	}
}
