// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package diff

import (
	"os"
	"strings"
	"testing"
)

// ========== 基本解析测试 ==========

func TestParseSingleFileSingleHunk(t *testing.T) {
	input := `--- a/hello.go
+++ b/hello.go
@@ -1,5 +1,6 @@
 package main

 import "fmt"
+import "os"

 func main() {
`

	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("期望 1 个文件，得到 %d", len(files))
	}

	fd := files[0]
	if fd.OldPath != "hello.go" {
		t.Errorf("OldPath = %q, 期望 %q", fd.OldPath, "hello.go")
	}
	if fd.NewPath != "hello.go" {
		t.Errorf("NewPath = %q, 期望 %q", fd.NewPath, "hello.go")
	}
	if len(fd.Hunks) != 1 {
		t.Fatalf("期望 1 个 hunk，得到 %d", len(fd.Hunks))
	}

	hunk := fd.Hunks[0]
	if hunk.OldStart != 1 || hunk.OldLines != 5 {
		t.Errorf("Old range = %d,%d, 期望 1,5", hunk.OldStart, hunk.OldLines)
	}
	if hunk.NewStart != 1 || hunk.NewLines != 6 {
		t.Errorf("New range = %d,%d, 期望 1,6", hunk.NewStart, hunk.NewLines)
	}
}

func TestParseLineTypes(t *testing.T) {
	input := `--- a/calc.go
+++ b/calc.go
@@ -10,4 +10,5 @@
 func add(a, b int) int {
-	return a + b
+	// add 两个数
+	return a + b
 }
`

	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	lines := files[0].Hunks[0].Lines

	// 应该有：1 context + 1 deleted + 2 added + 1 context = 5 行
	if len(lines) != 5 {
		t.Fatalf("期望 5 行，得到 %d", len(lines))
	}

	// 第 1 行：context
	if lines[0].Type != LineContext {
		t.Errorf("第 1 行类型 = %v, 期望 context", lines[0].Type)
	}

	// 第 2 行：deleted
	if lines[1].Type != LineDeleted {
		t.Errorf("第 2 行类型 = %v, 期望 deleted", lines[1].Type)
	}

	// 第 3 行：added（注释）
	if lines[2].Type != LineAdded {
		t.Errorf("第 3 行类型 = %v, 期望 added", lines[2].Type)
	}

	// 第 4 行：added（return）
	if lines[3].Type != LineAdded {
		t.Errorf("第 4 行类型 = %v, 期望 added", lines[3].Type)
	}

	// 第 5 行：context
	if lines[4].Type != LineContext {
		t.Errorf("第 5 行类型 = %v, 期望 context", lines[4].Type)
	}
}

func TestParseLineNumbers(t *testing.T) {
	input := `--- a/pkg/util.go
+++ b/pkg/util.go
@@ -20,3 +20,4 @@
 func Helper() {
+	// helper function
 	fmt.Println("hello")
 	return
 }
`

	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	lines := files[0].Hunks[0].Lines

	// 第 1 行 context：OldLine=20, NewLine=20
	if lines[0].OldLine != 20 || lines[0].NewLine != 20 {
		t.Errorf("第 1 行行号: Old=%d,New=%d, 期望 20,20", lines[0].OldLine, lines[0].NewLine)
	}

	// 第 2 行 added：OldLine=0, NewLine=21
	if lines[1].Type != LineAdded {
		t.Errorf("第 2 行类型 = %v, 期望 added", lines[1].Type)
	}
	if lines[1].NewLine != 21 {
		t.Errorf("第 2 行 NewLine = %d, 期望 21", lines[1].NewLine)
	}

	// 第 3 行 context：OldLine=21, NewLine=22
	if lines[2].OldLine != 21 || lines[2].NewLine != 22 {
		t.Errorf("第 3 行行号: Old=%d,New=%d, 期望 21,22", lines[2].OldLine, lines[2].NewLine)
	}
}

// ========== 多文件测试 ==========

func TestParseMultipleFiles(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 import "os"
--- a/util.go
+++ b/util.go
@@ -5,2 +5,3 @@
 func hello() {
+	fmt.Println("hi")
 }
`

	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("期望 2 个文件，得到 %d", len(files))
	}

	if files[0].OldPath != "main.go" {
		t.Errorf("文件 1 OldPath = %q, 期望 %q", files[0].OldPath, "main.go")
	}
	if files[1].OldPath != "util.go" {
		t.Errorf("文件 2 OldPath = %q, 期望 %q", files[1].OldPath, "util.go")
	}
}

// ========== 辅助方法测试 ==========

func TestAddedLines(t *testing.T) {
	input := `--- a/hello.go
+++ b/hello.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 import "os"
`

	files, _ := Parse(strings.NewReader(input))
	added := files[0].AddedLines()

	if len(added) != 1 {
		t.Fatalf("AddedLines: 期望 1 行，得到 %d", len(added))
	}
	if added[0].Content != `import "fmt"` {
		t.Errorf("AddedLines 内容 = %q, 期望 %q", added[0].Content, `import "fmt"`)
	}
}

func TestDeletedLines(t *testing.T) {
	input := `--- a/hello.go
+++ b/hello.go
@@ -1,4 +1,3 @@
 package main

 import "os"
-import "fmt"
`

	files, _ := Parse(strings.NewReader(input))
	deleted := files[0].DeletedLines()

	if len(deleted) != 1 {
		t.Fatalf("DeletedLines: 期望 1 行，得到 %d", len(deleted))
	}
	if deleted[0].Content != `import "fmt"` {
		t.Errorf("DeletedLines 内容 = %q, 期望 %q", deleted[0].Content, `import "fmt"`)
	}
}

func TestIsGoFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"pkg/handler.go", true},
		{"README.md", false},
		{"config.yaml", false},
		{"script.py", false},
	}

	for _, tt := range tests {
		fd := &FileDiff{NewPath: tt.path}
		got := fd.IsGoFile()
		if got != tt.want {
			t.Errorf("IsGoFile(%q) = %v, 期望 %v", tt.path, got, tt.want)
		}
	}
}

// ========== 边界情况测试 ==========

func TestParseEmptyDiff(t *testing.T) {
	input := ""
	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析空 diff 失败: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("空 diff 应该返回 0 个文件，得到 %d", len(files))
	}
}

func TestParseHunkContext(t *testing.T) {
	input := `--- a/handler.go
+++ b/handler.go
@@ -10,3 +10,3 @@ func processRequest(w http.ResponseWriter, r *http.Request) {
 	w.WriteHeader(200)
-	w.Write([]byte("old"))
+	w.Write([]byte("new"))
 }
`

	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	hunk := files[0].Hunks[0]
	if hunk.Context != `func processRequest(w http.ResponseWriter, r *http.Request) {` {
		t.Errorf("Hunk Context = %q", hunk.Context)
	}
}

func TestParseNewFile(t *testing.T) {
	// 新建文件：--- /dev/null
	input := `--- /dev/null
+++ b/newfile.go
@@ -0,0 +1,3 @@
+package main
+
+func main() {}
`

	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("期望 1 个文件，得到 %d", len(files))
	}

	fd := files[0]
	if fd.OldPath != "/dev/null" {
		t.Errorf("OldPath = %q, 期望 /dev/null", fd.OldPath)
	}
	if fd.NewPath != "newfile.go" {
		t.Errorf("NewPath = %q, 期望 newfile.go", fd.NewPath)
	}

	added := fd.AddedLines()
	if len(added) != 3 {
		t.Errorf("新增行数 = %d, 期望 3", len(added))
	}
}

func TestParseDeletedFile(t *testing.T) {
	// 删除文件：+++ /dev/null
	input := `--- a/old.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func main() {}
`

	files, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	fd := files[0]
	if fd.NewPath != "/dev/null" {
		t.Errorf("NewPath = %q, 期望 /dev/null", fd.NewPath)
	}

	deleted := fd.DeletedLines()
	if len(deleted) != 3 {
		t.Errorf("删除行数 = %d, 期望 3", len(deleted))
	}
}

// ========== parseFilePath 测试 ==========

func TestParseFilePath(t *testing.T) {
	tests := []struct {
		line   string
		prefix string
		want   string
	}{
		{"--- a/pkg/handler.go", "--- ", "pkg/handler.go"},
		{"+++ b/pkg/handler.go", "+++ ", "pkg/handler.go"},
		{"--- /dev/null", "--- ", "/dev/null"},
	}

	for _, tt := range tests {
		got := parseFilePath(tt.line, tt.prefix)
		if got != tt.want {
			t.Errorf("parseFilePath(%q, %q) = %q, 期望 %q", tt.line, tt.prefix, got, tt.want)
		}
	}
}

// ========== Go 包名提取测试 ==========

func TestGoPackageName(t *testing.T) {
	tests := []struct {
		name string
		diff string
		want string
	}{
		{
			name: "从上下文行提取",
			diff: `--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 import "os"
`,
			want: "main",
		},
		{
			name: "从新增行提取（新文件）",
			diff: `--- /dev/null
+++ b/handler.go
@@ -0,0 +1,3 @@
+package handler
+
+func Handle() {}
`,
			want: "handler",
		},
		{
			name: "包名带空格",
			diff: `--- a/util.go
+++ b/util.go
@@ -1,2 +1,3 @@
 package   utils

+func Hello() {}
`,
			want: "utils",
		},
		{
			name: "非 Go 文件无包名",
			diff: `--- a/README.md
+++ b/README.md
@@ -1,2 +1,3 @@
 # Hello

+World
`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, _ := Parse(strings.NewReader(tt.diff))
			got := files[0].GoPackageName()
			if got != tt.want {
				t.Errorf("GoPackageName() = %q, 期望 %q", got, tt.want)
			}
		})
	}
}

func TestExtractPackageName(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"package main", "main"},
		{"package handler", "handler"},
		{"package utils", "utils"},
		{"\tpackage main", "main"},       // 带缩进
		{"  package models  ", "models"}, // 带空格
		{"// package notthis", ""},       // 注释
		{"import \"fmt\"", ""},           // 非 package 行
		{"", ""},                         // 空行
		{"package", ""},                  // 缺包名
		{"package 123invalid", ""},       // 非法标识符
	}

	for _, tt := range tests {
		got := extractPackageName(tt.line)
		if got != tt.want {
			t.Errorf("extractPackageName(%q) = %q, 期望 %q", tt.line, got, tt.want)
		}
	}
}

// ========== ChangedGoFiles 测试 ==========

func TestChangedGoFiles(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main

+import "fmt"
--- a/README.md
+++ b/README.md
@@ -1,2 +1,3 @@
 # Hello

+World
--- a/pkg/util.go
+++ b/pkg/util.go
@@ -1,2 +1,3 @@
 package util

+func DoStuff() {}
`

	files, _ := Parse(strings.NewReader(input))
	goFiles := ChangedGoFiles(files)

	if len(goFiles) != 2 {
		t.Fatalf("ChangedGoFiles: 期望 2 个 Go 文件，得到 %d", len(goFiles))
	}
	if goFiles[0].NewPath != "main.go" {
		t.Errorf("文件 1 = %q, 期望 main.go", goFiles[0].NewPath)
	}
	if goFiles[1].NewPath != "pkg/util.go" {
		t.Errorf("文件 2 = %q, 期望 pkg/util.go", goFiles[1].NewPath)
	}
}

// ========== ReadFromFile 测试 ==========

func TestReadFromFile(t *testing.T) {
	// 先创建临时 diff 文件
	content := `--- a/hello.go
+++ b/hello.go
@@ -1,3 +1,4 @@
 package main

+import "fmt"
 import "os"
`

	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.diff"
	if err := writeFile(tmpFile, content); err != nil {
		t.Fatalf("写临时文件失败: %v", err)
	}

	files, err := ReadFromFile(tmpFile)
	if err != nil {
		t.Fatalf("ReadFromFile 失败: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("期望 1 个文件，得到 %d", len(files))
	}
	if files[0].NewPath != "hello.go" {
		t.Errorf("NewPath = %q, 期望 hello.go", files[0].NewPath)
	}
}

func TestReadFromFile_NotFound(t *testing.T) {
	_, err := ReadFromFile("/nonexistent/file.diff")
	if err == nil {
		t.Error("期望返回错误，但得到 nil")
	}
}

// writeFile 辅助函数：写文件。
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
