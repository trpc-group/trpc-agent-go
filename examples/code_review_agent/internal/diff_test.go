//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"testing"
)

func TestParseDiff_EmptyInput(t *testing.T) {
	files, err := ParseDiff("")
	if err != nil {
		t.Fatalf("空输入不应报错: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("空输入期望 0 个文件, 实际 %d", len(files))
	}
}

func TestParseDiff_SingleFile(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
index abc123..def456 100644
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {
-    println("hello")
+    fmt.Println("hello")
 }`

	files, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件, 实际 %d", len(files))
	}

	f := files[0]
	if f.OldPath != "main.go" {
		t.Errorf("OldPath = %q, 期望 main.go", f.OldPath)
	}
	if f.NewPath != "main.go" {
		t.Errorf("NewPath = %q, 期望 main.go", f.NewPath)
	}
	if f.IsBinary {
		t.Error("非 binary 文件")
	}
	if !f.GoFile() {
		t.Error("main.go 应该是 Go 文件")
	}

	if len(f.Hunks) != 1 {
		t.Fatalf("期望 1 个 hunk, 实际 %d", len(f.Hunks))
	}

	h := f.Hunks[0]
	if h.OldStart != 1 || h.NewStart != 1 {
		t.Errorf("hunk 起始位置错误: old=%d, new=%d", h.OldStart, h.NewStart)
	}

	// 验证行类型和行号
	addCount := 0
	delCount := 0
	for _, line := range h.Lines {
		switch line.Type {
		case LineAdd:
			addCount++
		case LineDelete:
			delCount++
		}
	}
	if addCount != 2 {
		t.Errorf("期望 2 行新增 (import + fmt.Println), 实际 %d", addCount)
	}
	if delCount != 1 {
		t.Errorf("期望 1 行删除, 实际 %d", delCount)
	}

	// 验证新增行内容
	added := f.AddedLines()
	if len(added) != 2 {
		t.Fatalf("期望 2 行新增 (import + fmt.Println), 实际 %d", len(added))
	}
	if added[0].Content != `import "fmt"` {
		t.Errorf("新增行内容 = %q, 期望 import \"fmt\"", added[0].Content)
	}
}

func TestParseDiff_MultipleFiles(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,1 +1,1 @@
-old
+new
diff --git a/util.go b/util.go
--- a/util.go
+++ b/util.go
@@ -1,0 +1,2 @@
+package util
+func Helper() {}`

	files, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("期望 2 个文件, 实际 %d", len(files))
	}

	if files[0].NewPath != "main.go" {
		t.Errorf("文件1 = %q, 期望 main.go", files[0].NewPath)
	}
	if files[1].NewPath != "util.go" {
		t.Errorf("文件2 = %q, 期望 util.go", files[1].NewPath)
	}
}

func TestParseDiff_NewFile(t *testing.T) {
	diff := `diff --git a/newfile.go b/newfile.go
new file mode 100644
--- /dev/null
+++ b/newfile.go
@@ -0,0 +1,3 @@
+package main
+func NewFunc() string {
+    return "hello"
+}`

	files, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件, 实际 %d", len(files))
	}

	f := files[0]
	if f.NewPath != "newfile.go" {
		t.Errorf("NewPath = %q", f.NewPath)
	}
	if len(f.AddedLines()) != 4 {
		t.Errorf("期望 4 行新增 (package + func + return + }), 实际 %d", len(f.AddedLines()))
	}
}

func TestParseDiff_BinaryFile(t *testing.T) {
	diff := `diff --git a/image.png b/image.png
index abc..def 100644
Binary files a/image.png and b/image.png differ`

	files, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件, 实际 %d", len(files))
	}
	if !files[0].IsBinary {
		t.Error("应该是 binary 文件")
	}
}

func TestParseDiff_SecurityIssue(t *testing.T) {
	diff := `diff --git a/config.go b/config.go
--- a/config.go
+++ b/config.go
@@ -5,6 +5,7 @@
 var (
-    apiEndpoint = "https://api.example.com"
+    apiKey      = "sk-abc123def456ghi789jkl012"
+    apiEndpoint = "https://api.example.com"
 )`

	files, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("期望 1 个文件, 实际 %d", len(files))
	}
	added := files[0].AddedLines()
	if len(added) != 2 {
		t.Fatalf("期望 2 行新增, 实际 %d", len(added))
	}
}

func TestGoFileDetection(t *testing.T) {
	tests := []struct {
		path     string
		isGoFile bool
		isTest   bool
	}{
		{"main.go", true, false},
		{"main_test.go", false, true},
		{"util_test.go", false, true},
		{"README.md", false, false},
		{"internal/handler.go", true, false},
	}

	for _, tt := range tests {
		df := DiffFile{NewPath: tt.path}
		if df.GoFile() != tt.isGoFile {
			t.Errorf("%s: GoFile() = %v, 期望 %v", tt.path, df.GoFile(), tt.isGoFile)
		}
		if df.TestFile() != tt.isTest {
			t.Errorf("%s: TestFile() = %v, 期望 %v", tt.path, df.TestFile(), tt.isTest)
		}
	}
}

func TestParseDiff_LineNumbers(t *testing.T) {
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -10,5 +10,6 @@
 line 1
+new line
 line 2
-deleted line
+replacement line
 line 3`

	files, err := ParseDiff(diff)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatal("期望 1 个 hunk")
	}

	h := files[0].Hunks[0]
	if h.OldStart != 10 || h.NewStart != 10 {
		t.Errorf("hunk 起始: old=%d new=%d, 期望 10/10", h.OldStart, h.NewStart)
	}

	for _, line := range h.Lines {
		switch {
		case line.Content == "new line":
			if line.Type != LineAdd || line.NewNo != 11 {
				t.Errorf("new line: type=%v newNo=%d", line.Type, line.NewNo)
			}
		case line.Content == "deleted line":
			if line.Type != LineDelete || line.OldNo != 12 {
				t.Errorf("deleted line: type=%v oldNo=%d", line.Type, line.OldNo)
			}
		case line.Content == "replacement line":
			if line.Type != LineAdd || line.NewNo != 13 {
				t.Errorf("replacement line: type=%v newNo=%d", line.Type, line.NewNo)
			}
		}
	}
}
