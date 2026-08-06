// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package rules

import (
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// ========== 模拟规则（用于测试） ==========

// mockRule 是一个模拟规则，用于测试引擎本身。
// 它会检查文件里是否有 "TODO" 字样，如果有就报告一个 finding。
type mockRule struct {
	id       string
	name     string
	severity findings.Severity
	category findings.Category
}

func (r *mockRule) ID() string                  { return r.id }
func (r *mockRule) Name() string                { return r.name }
func (r *mockRule) Severity() findings.Severity { return r.severity }
func (r *mockRule) Category() findings.Category { return r.category }

func (r *mockRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	var result []findings.Finding
	for _, line := range fd.AddedLines() {
		if strings.Contains(line.Content, "TODO") {
			result = append(result, *findings.NewFinding(
				r.severity, r.category, r.id, "发现 TODO",
				fd.NewPath, line.NewLine,
				line.Content, "请处理 TODO",
				0.8, "rule:mock",
			))
		}
	}
	return result, nil
}

// errorRule 总是返回错误，用于测试错误处理。
type errorRule struct{}

func (r *errorRule) ID() string                  { return "ERR-001" }
func (r *errorRule) Name() string                { return "错误规则" }
func (r *errorRule) Severity() findings.Severity { return findings.SeverityLow }
func (r *errorRule) Category() findings.Category { return findings.CategoryTesting }

func (r *errorRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	return nil, fmt.Errorf("模拟规则执行错误")
}

// ========== 测试 ==========

func TestNewEngine(t *testing.T) {
	engine := NewEngine()
	if len(engine.Rules()) != 0 {
		t.Errorf("新引擎应该没有规则，得到 %d", len(engine.Rules()))
	}
}

func TestRegister(t *testing.T) {
	engine := NewEngine()
	engine.Register(&mockRule{id: "M-001", name: "规则1"})
	engine.Register(&mockRule{id: "M-002", name: "规则2"})

	if len(engine.Rules()) != 2 {
		t.Errorf("期望 2 条规则，得到 %d", len(engine.Rules()))
	}
}

func TestRegisterAll(t *testing.T) {
	engine := NewEngine()
	engine.RegisterAll(
		&mockRule{id: "M-001", name: "规则1"},
		&mockRule{id: "M-002", name: "规则2"},
		&mockRule{id: "M-003", name: "规则3"},
	)

	if len(engine.Rules()) != 3 {
		t.Errorf("期望 3 条规则，得到 %d", len(engine.Rules()))
	}
}

func TestRun_Findings(t *testing.T) {
	// 创建一个 diff，新增行里包含 "TODO"
	input := `--- a/main.go
+++ b/main.go
@@ -1,3 +1,5 @@
 package main

+// TODO: 实现这个函数
+func hello() {}
 import "fmt"
`

	files, _ := diff.Parse(strings.NewReader(input))

	engine := NewEngine()
	engine.Register(&mockRule{id: "M-001", name: "TODO检测"})

	findings, err := engine.Run(files)
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	if len(findings) != 1 {
		t.Fatalf("期望 1 个 finding，得到 %d", len(findings))
	}
	if findings[0].File != "main.go" {
		t.Errorf("文件 = %q, 期望 main.go", findings[0].File)
	}
	if findings[0].RuleID != "M-001" {
		t.Errorf("RuleID = %q, 期望 M-001", findings[0].RuleID)
	}
}

func TestRun_NoFindings(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main

+import "fmt"
`

	files, _ := diff.Parse(strings.NewReader(input))

	engine := NewEngine()
	engine.Register(&mockRule{id: "M-001", name: "TODO检测"})

	findings, err := engine.Run(files)
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	if len(findings) != 0 {
		t.Errorf("期望 0 个 findings，得到 %d", len(findings))
	}
}

func TestRun_ErrorRule(t *testing.T) {
	// 规则执行出错不应导致整个引擎崩溃
	input := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main

+import "fmt"
`

	files, _ := diff.Parse(strings.NewReader(input))

	engine := NewEngine()
	engine.Register(&errorRule{})

	findings, err := engine.Run(files)
	if err != nil {
		t.Fatalf("Run 不应返回错误，得到: %v", err)
	}

	// 出错的规则不产生 findings
	if len(findings) != 0 {
		t.Errorf("期望 0 个 findings，得到 %d", len(findings))
	}
}

func TestRun_MultipleRules(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,4 @@
 package main

+// TODO: fix this
+// TODO: and this
`

	files, _ := diff.Parse(strings.NewReader(input))

	engine := NewEngine()
	// 注册两条规则，都会检查 TODO
	engine.Register(&mockRule{id: "M-001", name: "TODO检测1"})
	engine.Register(&mockRule{id: "M-002", name: "TODO检测2"})

	findings, err := engine.Run(files)
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	// 2 条规则 × 2 个 TODO = 4 个 findings
	if len(findings) != 4 {
		t.Errorf("期望 4 个 findings，得到 %d", len(findings))
	}
}

func TestRunOnSingleFile(t *testing.T) {
	input := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,3 @@
 package main

+// TODO: hello
`

	files, _ := diff.Parse(strings.NewReader(input))

	engine := NewEngine()
	engine.Register(&mockRule{id: "M-001", name: "TODO检测"})

	findings, err := engine.RunOnSingleFile(files[0])
	if err != nil {
		t.Fatalf("RunOnSingleFile 失败: %v", err)
	}

	if len(findings) != 1 {
		t.Errorf("期望 1 个 finding，得到 %d", len(findings))
	}
}

func TestSummary(t *testing.T) {
	engine := NewEngine()
	engine.Register(&mockRule{id: "M-001", name: "规则A", severity: findings.SeverityHigh, category: findings.CategorySecurity})
	engine.Register(&mockRule{id: "M-002", name: "规则B", severity: findings.SeverityLow, category: findings.CategoryTesting})

	summary := engine.Summary()

	if !strings.Contains(summary, "2 条规则") {
		t.Errorf("Summary 应包含规则数量，实际: %s", summary)
	}
	if !strings.Contains(summary, "M-001") {
		t.Errorf("Summary 应包含规则 ID，实际: %s", summary)
	}
}
