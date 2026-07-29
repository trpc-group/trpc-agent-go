// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package report

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

func newTestReport() *ReviewReport {
	r := NewReport("test-001", "diff_file", "testdata/security.diff")

	// 模拟审查结果
	f1 := findings.NewFinding(
		findings.SeverityHigh, findings.CategorySecurity, "SEC-001",
		"Hardcoded API key", "config.go", 10,
		`APIKey = "sk-abc123"`, "Use environment variable",
		0.95, "rule:hardcoded_secret",
	)
	f2 := findings.NewFinding(
		findings.SeverityMedium, findings.CategoryResource, "RES-001",
		"Missing defer close", "handler.go", 25,
		`conn, err := db.Conn(ctx)`, "Add defer conn.Close()",
		0.8, "rule:resource_leak",
	)
	f3 := findings.NewFinding(
		findings.SeverityLow, findings.CategoryTesting, "TST-001",
		"Missing test", "util.go", 5,
		`func Helper() string`, "Add TestHelper",
		0.5, "rule:missing_test", // 低置信度 → warnings
	)

	result := findings.Deduplicate([]findings.Finding{*f1, *f2, *f3})
	r.SetResult(result, 3, 3)
	r.Finalize(time.Now().Add(-2 * time.Second))

	return r
}

func TestNewReport(t *testing.T) {
	r := NewReport("task-123", "diff_file", "test.diff")

	if r.TaskID != "task-123" {
		t.Errorf("TaskID = %q, 期望 %q", r.TaskID, "task-123")
	}
	if r.InputType != "diff_file" {
		t.Errorf("InputType = %q, 期望 %q", r.InputType, "diff_file")
	}
	if r.StartTime == "" {
		t.Error("StartTime 不应为空")
	}
}

func TestSetResult(t *testing.T) {
	r := newTestReport()

	if r.Summary.TotalFindings != 2 {
		t.Errorf("TotalFindings = %d, 期望 2", r.Summary.TotalFindings)
	}
	if r.Summary.TotalWarnings != 1 {
		t.Errorf("TotalWarnings = %d, 期望 1", r.Summary.TotalWarnings)
	}
	if r.Summary.BySeverity["high"] != 1 {
		t.Errorf("BySeverity[high] = %d, 期望 1", r.Summary.BySeverity["high"])
	}
	if r.Summary.ByCategory["security"] != 1 {
		t.Errorf("ByCategory[security] = %d, 期望 1", r.Summary.ByCategory["security"])
	}
}

func TestWriteJSON(t *testing.T) {
	r := newTestReport()

	tmpDir := t.TempDir()
	path := tmpDir + "/report.json"

	if err := r.WriteJSON(path); err != nil {
		t.Fatalf("WriteJSON 失败: %v", err)
	}

	// 验证文件存在且是合法 JSON
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 JSON 失败: %v", err)
	}

	var parsed ReviewReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("JSON 解析失败: %v", err)
	}

	if parsed.TaskID != "test-001" {
		t.Errorf("JSON TaskID = %q, 期望 %q", parsed.TaskID, "test-001")
	}
	if len(parsed.Findings) != 2 {
		t.Errorf("JSON Findings 数量 = %d, 期望 2", len(parsed.Findings))
	}
}

func TestWriteMarkdown(t *testing.T) {
	r := newTestReport()

	tmpDir := t.TempDir()
	path := tmpDir + "/report.md"

	if err := r.WriteMarkdown(path); err != nil {
		t.Fatalf("WriteMarkdown 失败: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 Markdown 失败: %v", err)
	}

	md := string(data)

	// 验证关键内容
	checks := []string{
		"# 代码审查报告",
		"test-001",
		"Hardcoded API key",
		"SEC-001",
		"config.go",
		"需人工复核",
		"修复建议汇总",
	}

	for _, check := range checks {
		if !strings.Contains(md, check) {
			t.Errorf("Markdown 缺少 %q", check)
		}
	}
}

func TestToMarkdown_NoFindings(t *testing.T) {
	r := NewReport("empty-001", "diff_file", "test.diff")
	r.SetResult(findings.DedupResult{}, 0, 0)
	r.Finalize(time.Now())

	md := r.ToMarkdown()
	if !strings.Contains(md, "未发现高置信度问题") {
		t.Error("无 findings 时应显示 '未发现高置信度问题'")
	}
}

func TestSeverityIcon(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"high", "🔴 high"},
		{"medium", "🟡 medium"},
		{"low", "🔵 low"},
		{"info", "⚪ info"},
		{"other", "other"},
	}

	for _, tt := range tests {
		got := severityIcon(tt.input)
		if got != tt.want {
			t.Errorf("severityIcon(%q) = %q, 期望 %q", tt.input, got, tt.want)
		}
	}
}
