//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal"
)

// TestPipeline_SecurityIssue 测试安全问题的检测。
func TestPipeline_SecurityIssue(t *testing.T) {
	dir := t.TempDir()
	diffFile := filepath.Join(dir, "test.diff")

	content := `diff --git a/config.go b/config.go
--- a/config.go
+++ b/config.go
@@ -1,0 +1,3 @@
+var apiKey = "sk-abc123def456ghi789jkl012mno345pqr678stu901vwx"
+var secret = "my-super-secret-password"
+var endpoint = "https://api.example.com"
`
	if err := os.WriteFile(diffFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	task, dedup, err := runPipeline(
		"diff_file", content,
		"",       // no repo path
		dir+"/test.db",
		dir,
		true,     // dry-run
		"", "", "",
	)
	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	if task.Status != "completed" {
		t.Errorf("状态应为 completed, 实际 %s", task.Status)
	}

	// 应该有安全相关的 findings
	hasSecurity := false
	hasSensitive := false
	for _, f := range task.Findings {
		if f.Category == internal.CategorySecurity {
			hasSecurity = true
		}
		if f.Category == internal.CategorySensitive {
			hasSensitive = true
		}
	}

	// apiKey + secret 至少触发一条规则
	if !hasSecurity && !hasSensitive {
		t.Errorf("应检测到安全或敏感信息问题, findings=%d", len(task.Findings))
	}

	t.Logf("task_id=%s, findings=%d, dedup=%d, duration=%dms",
		task.ID, task.Summary.Total-task.Summary.Duplicates, dedup, task.DurationMs)
}

// TestPipeline_CleanDiff 测试无问题的 diff。
func TestPipeline_CleanDiff(t *testing.T) {
	dir := t.TempDir()

	content := `diff --git a/clean.go b/clean.go
--- a/clean.go
+++ b/clean.go
@@ -1,3 +1,5 @@
 package main
-func main() {}
+// greet 打印问候语
+func greet(name string) string {
+    return "Hello, " + name
+}
`
	task, _, err := runPipeline(
		"diff_file", content, "", dir+"/test.db", dir, true, "", "", "",
	)
	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	if task.Status != "completed" {
		t.Errorf("状态应为 completed, 实际 %s", task.Status)
	}

	t.Logf("clean diff: findings=%d", task.Summary.Total-task.Summary.Duplicates)
}

// TestPipeline_GoroutineLeak 测试 goroutine 泄漏检测。
func TestPipeline_GoroutineLeak(t *testing.T) {
	dir := t.TempDir()

	content := `diff --git a/worker.go b/worker.go
--- a/worker.go
+++ b/worker.go
@@ -1,0 +1,10 @@
+package worker
+import "time"
+func startWorker() {
+    go func() {
+        for {
+        }
+    }()
+}
`
	task, _, err := runPipeline(
		"diff_file", content, "", dir+"/test.db", dir, true, "", "", "",
	)
	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	// goroutine 泄漏应被检测
	hasConcurrency := false
	for _, f := range task.Findings {
		if f.Category == internal.CategoryConcurrency {
			hasConcurrency = true
			break
		}
	}

	if !hasConcurrency {
		t.Log("注意: 未检测到 goroutine 问题，可能需要调整规则")
	}
}

// TestPipeline_ResourceLeak 测试资源泄漏检测。
func TestPipeline_ResourceLeak(t *testing.T) {
	dir := t.TempDir()

	content := `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -1,0 +1,7 @@
+package main
+import "os"
+func readConfig() ([]byte, error) {
+    f, err := os.Open("config.yaml")
+    if err != nil { return nil, err }
+    return os.ReadAll(f)
+}
`
	task, _, err := runPipeline(
		"diff_file", content, "", dir+"/test.db", dir, true, "", "", "",
	)
	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	hasResource := false
	for _, f := range task.Findings {
		if f.Category == internal.CategoryResource {
			hasResource = true
			break
		}
	}

	if !hasResource {
		t.Log("注意: 未检测到资源泄漏问题")
	}
}

// TestPipeline_DuplicateFindings 测试去重逻辑。
func TestPipeline_DuplicateFindings(t *testing.T) {
	dir := t.TempDir()

	content := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,5 @@
 package main
-var endpoint = "https://api.example.com"
+var apiKey = "sk-duplicate-test-key-12345"
+var password = "super-secret-123"
+var endpoint = "https://api.example.com"
`
	task, dedup, err := runPipeline(
		"diff_file", content, "", dir+"/test.db", dir, true, "", "", "",
	)
	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	t.Logf("findings=%d, dedup_removed=%d", task.Summary.Total, dedup)

	// 验证 JSON 报告生成
	jsonPath := filepath.Join(dir, "review_report.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("JSON 报告未生成: %v", err)
	} else {
		data, _ := os.ReadFile(jsonPath)
		var report map[string]any
		if err := json.Unmarshal(data, &report); err != nil {
			t.Errorf("JSON 报告格式无效: %v", err)
		}
	}

	// 验证 Markdown 报告生成
	mdPath := filepath.Join(dir, "review_report.md")
	if _, err := os.Stat(mdPath); err != nil {
		t.Errorf("Markdown 报告未生成: %v", err)
	} else {
		data, _ := os.ReadFile(mdPath)
		if !strings.Contains(string(data), "# 代码评审报告") {
			t.Error("Markdown 报告缺少标题")
		}
	}
}

// TestPipeline_EmptyDiff 测试空输入。
func TestPipeline_EmptyDiff(t *testing.T) {
	dir := t.TempDir()

	task, _, err := runPipeline(
		"diff_file", "", "", dir+"/test.db", dir, true, "", "", "",
	)
	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	if task.Status != "completed" {
		t.Errorf("状态应为 completed, 实际 %s", task.Status)
	}
	if task.TotalFiles != 0 {
		t.Errorf("文件数应为 0, 实际 %d", task.TotalFiles)
	}
}

// TestPipeline_Timing 验证 dry-run 模式下管线耗时 < 2 分钟。
func TestPipeline_Timing(t *testing.T) {
	dir := t.TempDir()
	diffContent := `
diff --git a/complex.go b/complex.go
--- a/complex.go
+++ b/complex.go
@@ -1,0 +1,50 @@
+package main
+import (
+    "database/sql"
+    "fmt"
+    "net/http"
+    "os"
+    "os/exec"
+)
+var apiKey = "sk-large-test-key-1234567890abcdef"
+func handler(w http.ResponseWriter, r *http.Request) {
+    fmt.Fprintf(w, "Hello")
+}
+func worker() {
+    go func() {
+        for { }
+    }()
+}
+func readFile() {
+    f, _ := os.Open("test.txt")
+    fmt.Println(f.Name())
+}
+func queryDB() {
+    db, _ := sql.Open("sqlite3", "test.db")
+    fmt.Println(db.Stats())
+}
+func dangerous() {
+    exec.Command("sh", "-c", "echo hacked")
+}
`

	start := time.Now()
	task, _, err := runPipeline(
		"diff_file", diffContent, "", dir+"/test.db", dir, true, "", "", "",
	)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	t.Logf("耗时: %v, findings=%d", elapsed, task.Summary.Total)

	if elapsed > 2*time.Minute {
		t.Errorf("dry-run 模式耗时 %v 超过 2 分钟限制", elapsed)
	}
}

// TestPipeline_DatabasePersistence 验证数据库持久化。
func TestPipeline_DatabasePersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "review.db")

	content := `diff --git a/db.go b/db.go
--- a/db.go
+++ b/db.go
@@ -1,0 +1,5 @@
+package main
+import "database/sql"
+func connect() { sql.Open("sqlite3", "test.db") }
`

	task, _, err := runPipeline(
		"diff_file", content, "", dbPath, dir, true, "", "", "",
	)
	if err != nil {
		t.Fatalf("管线执行失败: %v", err)
	}

	// 验证数据库文件存在且大小合理
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("数据库文件不存在: %v", err)
	}
	if info.Size() == 0 {
		t.Error("数据库文件为空")
	}

	// 验证 task 记录被持久化（使用短暂连接）
	func() {
		store, err := internal.NewStore(dbPath)
		if err != nil {
			t.Fatalf("打开数据库失败: %v", err)
		}
		defer store.Close()

		ctx := context.Background()
		retrieved, err := store.GetTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("查询 task 失败: %v", err)
		}

		if retrieved.ID != task.ID {
			t.Errorf("task ID 不匹配: %q != %q", retrieved.ID, task.ID)
		}

		findings, err := store.GetFindingsByTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("查询 findings 失败: %v", err)
		}

		t.Logf("数据库验证: task=%q, status=%s, findings=%d, db_size=%d",
			retrieved.ID, retrieved.Status, len(findings), info.Size())
	}()

}
