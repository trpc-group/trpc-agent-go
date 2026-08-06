// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package rules

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
)

func TestLoadDSLRulesFromBytes_BasicRule(t *testing.T) {
	yaml := `
rules:
  - id: TEST-001
    name: "测试规则"
    severity: medium
    category: security
    match:
      line_contains: ["password"]
    message: "检测到密码"
    recommendation: "使用环境变量"
`
	rules, err := LoadDSLRulesFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("加载规则失败: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("规则数量 = %d, 期望 1", len(rules))
	}

	r := rules[0]
	if r.ID() != "TEST-001" {
		t.Errorf("ID = %q, 期望 %q", r.ID(), "TEST-001")
	}
	if r.Name() != "测试规则" {
		t.Errorf("Name = %q, 期望 %q", r.Name(), "测试规则")
	}
}

func TestLoadDSLRulesFromBytes_TokenFactMatch(t *testing.T) {
	yaml := `
rules:
  - id: TEST-002
    name: "Token 匹配测试"
    severity: high
    category: security
    match:
      token_facts:
        - kind: identifier
          value_contains: ["password"]
        - kind: string_literal
    message: "检测到硬编码密码"
    recommendation: "使用环境变量"
`
	rules, err := LoadDSLRulesFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("加载规则失败: %v", err)
	}

	// 测试匹配
	input := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	password := "secret123456"
 }
`
	files, _ := diff.Parse(strings.NewReader(input))
	results, _ := rules[0].Check(files[0])

	if len(results) == 0 {
		t.Fatal("应检测到 password 赋值")
	}
	if results[0].Confidence != 0.80 {
		t.Errorf("Confidence = %.2f, 期望 0.80", results[0].Confidence)
	}
}

func TestLoadDSLRulesFromBytes_ExcludeCondition(t *testing.T) {
	yaml := `
rules:
  - id: TEST-003
    name: "排除测试"
    severity: medium
    category: security
    match:
      line_contains: ["password"]
    exclude:
      line_contains: ["test", "example"]
    message: "检测到密码"
    recommendation: "使用环境变量"
`
	rules, err := LoadDSLRulesFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("加载规则失败: %v", err)
	}

	// 应匹配
	input1 := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	password = "realpassword123"
 }
`
	files1, _ := diff.Parse(strings.NewReader(input1))
	results1, _ := rules[0].Check(files1[0])
	if len(results1) == 0 {
		t.Fatal("应检测到密码")
	}

	// 应排除（包含 test）
	input2 := `--- a/config.go
+++ b/config.go
@@ -5,2 +5,3 @@
 func init() {
+	testPassword = "testvalue123"
 }
`
	files2, _ := diff.Parse(strings.NewReader(input2))
	results2, _ := rules[0].Check(files2[0])
	if len(results2) != 0 {
		t.Errorf("包含 test 的行应被排除，发现 %d 个", len(results2))
	}
}

func TestLoadDSLRulesFromBytes_FileExtension(t *testing.T) {
	yaml := `
rules:
  - id: TEST-004
    name: "Go 文件规则"
    severity: low
    category: testing
    match:
      file_extension: [".go"]
      line_contains: ["TODO"]
    message: "Go 文件中的 TODO"
    recommendation: "处理 TODO"
`
	rules, err := LoadDSLRulesFromBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("加载规则失败: %v", err)
	}

	// Go 文件应匹配
	inputGo := `--- a/main.go
+++ b/main.go
@@ -5,2 +5,3 @@
 func main() {
+	// TODO: implement
 }
`
	filesGo, _ := diff.Parse(strings.NewReader(inputGo))
	resultsGo, _ := rules[0].Check(filesGo[0])
	if len(resultsGo) == 0 {
		t.Fatal("Go 文件中的 TODO 应被检测")
	}

	// 非 Go 文件不应匹配
	inputMd := `--- a/README.md
+++ b/README.md
@@ -1,2 +1,3 @@
 # Hello

+TODO: add docs
`
	filesMd, _ := diff.Parse(strings.NewReader(inputMd))
	resultsMd, _ := rules[0].Check(filesMd[0])
	if len(resultsMd) != 0 {
		t.Errorf("非 Go 文件不应匹配，发现 %d 个", len(resultsMd))
	}
}

func TestLoadDSLRulesFromBytes_InvalidYAML(t *testing.T) {
	yaml := `not valid yaml: [`
	_, err := LoadDSLRulesFromBytes([]byte(yaml))
	if err == nil {
		t.Error("无效 YAML 应返回错误")
	}
}

func TestLoadDSLRulesFromBytes_MissingID(t *testing.T) {
	yaml := `
rules:
  - name: "无 ID 规则"
    severity: medium
`
	_, err := LoadDSLRulesFromBytes([]byte(yaml))
	if err == nil {
		t.Error("缺少 ID 应返回错误")
	}
}

func TestLoadDSLRulesFromFile(t *testing.T) {
	rules, err := LoadDSLRulesFromFile("custom/security.yaml")
	if err != nil {
		t.Fatalf("加载规则文件失败: %v", err)
	}
	if len(rules) < 3 {
		t.Errorf("规则数量 = %d, 期望 >= 3", len(rules))
	}

	// 验证规则 ID
	ids := make(map[string]bool)
	for _, r := range rules {
		ids[r.ID()] = true
	}
	if !ids["SEC-CUSTOM-001"] {
		t.Error("缺少规则 SEC-CUSTOM-001")
	}
	if !ids["SEC-CUSTOM-002"] {
		t.Error("缺少规则 SEC-CUSTOM-002")
	}
}

func TestLoadDSLRules(t *testing.T) {
	rules, err := LoadDSLRules("custom")
	if err != nil {
		t.Fatalf("加载规则目录失败: %v", err)
	}
	if len(rules) < 3 {
		t.Errorf("规则数量 = %d, 期望 >= 3", len(rules))
	}
}
