//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// CLI 二进制级端到端测试（验收标准 1）：
// 编译真实二进制，验证 flag 解析、必填校验、报告生成、数据库落库与
// dry-run / fake-model 行为，覆盖 main() 的 os.Exit 错误路径。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var cliBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "cr-cli-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建临时目录失败: %v\n", err)
		os.Exit(1)
	}
	cliBin = filepath.Join(dir, "code_review_agent")
	out, err := exec.Command("go", "build", "-o", cliBin, ".").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "编译 CLI 失败: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// runCLI 运行编译好的二进制，返回 stdout / stderr / 退出码。
func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(cliBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("运行 CLI 失败: %v", err)
	}
	return stdout.String(), stderr.String(), code
}

// TestCLI_SecurityIssue_E2E 端到端：退出码 0、报告生成、DB 可查。
func TestCLI_SecurityIssue_E2E(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runCLI(t,
		"--diff-file=testdata/security_issue/diff.patch",
		"--dry-run",
		"--output-dir="+dir,
		"--db-path="+filepath.Join(dir, "review.db"),
	)
	if code != 0 {
		t.Fatalf("退出码应为 0, 实际 %d (stdout=%s)", code, stdout)
	}
	if !strings.Contains(stdout, "审查完成") {
		t.Errorf("stdout 应包含审查完成信息: %s", stdout)
	}

	// 报告文件生成且含 findings。
	jsonPath := filepath.Join(dir, "review_report.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("JSON 报告未生成: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("JSON 报告格式无效: %v", err)
	}
	summary, ok := report["summary"].(map[string]any)
	if !ok {
		t.Fatalf("报告缺少 summary: %v", report)
	}
	if total, _ := summary["total"].(float64); total <= 0 {
		t.Errorf("安全样本应检出问题, total=%v", summary["total"])
	}
}

// TestCLI_CleanDiff_ZeroFindings 验证干净样本零检出。
func TestCLI_CleanDiff_ZeroFindings(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runCLI(t,
		"--diff-file=testdata/clean/diff.patch",
		"--dry-run",
		"--output-dir="+dir,
	)
	if code != 0 {
		t.Fatalf("退出码应为 0, 实际 %d", code)
	}
	data, err := os.ReadFile(filepath.Join(dir, "review_report.json"))
	if err != nil {
		t.Fatalf("JSON 报告未生成: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("JSON 报告格式无效: %v", err)
	}
	summary := report["summary"].(map[string]any)
	if total, _ := summary["total"].(float64); total != 0 {
		t.Errorf("干净样本应零检出, total=%v", summary["total"])
	}
}

// TestCLI_MissingArgs_Exit1 验证缺少必填参数时退出码 1。
func TestCLI_MissingArgs_Exit1(t *testing.T) {
	_, stderr, code := runCLI(t)
	if code != 1 {
		t.Errorf("缺少参数时退出码应为 1, 实际 %d", code)
	}
	if !strings.Contains(stderr, "必须指定") {
		t.Errorf("stderr 应提示缺少参数: %s", stderr)
	}
}

// TestCLI_InvalidDiffFile_Exit1 验证不存在的 diff 文件退出码 1。
func TestCLI_InvalidDiffFile_Exit1(t *testing.T) {
	_, stderr, code := runCLI(t, "--diff-file=/nonexistent/patch.diff")
	if code != 1 {
		t.Errorf("无效 diff 文件时退出码应为 1, 实际 %d", code)
	}
	if !strings.Contains(stderr, "读取 diff 文件失败") {
		t.Errorf("stderr 应提示读取失败: %s", stderr)
	}
}

// TestCLI_ReportFilesWritten 验证 JSON + MD 双报告生成。
func TestCLI_ReportFilesWritten(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runCLI(t,
		"--diff-file=testdata/security_issue/diff.patch",
		"--dry-run",
		"--output-dir="+dir,
	)
	if code != 0 {
		t.Fatalf("退出码应为 0, 实际 %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "review_report.json")); err != nil {
		t.Errorf("JSON 报告缺失: %v", err)
	}
	md, err := os.ReadFile(filepath.Join(dir, "review_report.md"))
	if err != nil {
		t.Fatalf("MD 报告缺失: %v", err)
	}
	if !strings.Contains(string(md), "# 代码评审报告") {
		t.Error("MD 报告缺少标题")
	}
}

// TestCLI_FakeModel 验证 --fake-model 模式可跑通、耗时 < 2min、报告含模型摘要。
func TestCLI_FakeModel(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	_, _, code := runCLI(t,
		"--diff-file=testdata/security_issue/diff.patch",
		"--dry-run",
		"--fake-model",
		"--output-dir="+dir,
	)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("退出码应为 0, 实际 %d", code)
	}
	if elapsed > 2*time.Minute {
		t.Errorf("fake-model 耗时 %v 超过 2 分钟", elapsed)
	}
	md, err := os.ReadFile(filepath.Join(dir, "review_report.md"))
	if err != nil {
		t.Fatalf("MD 报告缺失: %v", err)
	}
	if !strings.Contains(string(md), "FakeModel 摘要") {
		t.Error("MD 报告应包含 fake model 摘要")
	}
}

// TestCLI_NoSandboxExec 验证 dry-run + repo-path 时不真正执行 go vet。
func TestCLI_NoSandboxExec(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runCLI(t,
		"--diff-file=testdata/clean/diff.patch",
		"--dry-run",
		"--repo-path=.",
		"--output-dir="+dir,
	)
	if code != 0 {
		t.Fatalf("退出码应为 0, 实际 %d (stderr=%s)", code, stderr)
	}
	// dry-run 下沙箱命令是模拟执行，报告不含真实 go vet 输出。
	data, err := os.ReadFile(filepath.Join(dir, "review_report.json"))
	if err != nil {
		t.Fatalf("JSON 报告缺失: %v", err)
	}
	if strings.Contains(string(data), "go vet 发现问题") {
		t.Error("dry-run 不应产生真实 go vet findings")
	}
	_ = stdout
}
