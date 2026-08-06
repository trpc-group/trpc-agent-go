// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package analyzer

import (
	"testing"
)

func TestAnalyzeLine_BasicFacts(t *testing.T) {
	ta := NewTokenAnalyzer()

	analysis := ta.AnalyzeLine(`password := "secret123"`, 10)

	if analysis.Line != 10 {
		t.Errorf("Line = %d, 期望 10", analysis.Line)
	}

	// 应该有：password(IDENT), :=(DEFINE), "secret123"(STRING)
	if len(analysis.Facts) < 3 {
		t.Fatalf("Facts 数量 = %d, 期望 >= 3", len(analysis.Facts))
	}

	// 检查标识符
	ids := analysis.FindIdentifiers()
	if len(ids) == 0 {
		t.Fatal("应找到标识符")
	}
	found := false
	for _, id := range ids {
		if id == "password" {
			found = true
		}
	}
	if !found {
		t.Error("应找到标识符 'password'")
	}

	// 检查字符串字面量
	strs := analysis.FindStringLiterals()
	if len(strs) == 0 {
		t.Fatal("应找到字符串字面量")
	}
	if strs[0] != `"secret123"` {
		t.Errorf("字符串字面量 = %q, 期望 %q", strs[0], `"secret123"`)
	}

	// 检查赋值
	if !analysis.HasAssignment() {
		t.Error("应检测到赋值操作")
	}
}

func TestAnalyzeLine_DeferStatement(t *testing.T) {
	ta := NewTokenAnalyzer()

	analysis := ta.AnalyzeLine(`defer f.Close()`, 5)

	if !analysis.HasDefer() {
		t.Error("应检测到 defer 语句")
	}
}

func TestAnalyzeLine_GoStatement(t *testing.T) {
	ta := NewTokenAnalyzer()

	analysis := ta.AnalyzeLine(`go func() { doWork() }()`, 8)

	if !analysis.HasGoStatement() {
		t.Error("应检测到 go 语句")
	}
}

func TestAnalyzeLine_SensitiveIdentifier(t *testing.T) {
	ta := NewTokenAnalyzer()

	tests := []struct {
		line      string
		wantFound bool
		wantName  string
	}{
		{`apiKey := "xxx"`, true, "apiKey"},
		{`password = "xxx"`, true, "password"},
		{`host := "localhost"`, false, ""},
		{`token := "xxx"`, true, "token"},
		{`secret_key := "xxx"`, true, "secret_key"},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			analysis := ta.AnalyzeLine(tt.line, 1)
			found, name := analysis.HasSensitiveIdentifier()
			if found != tt.wantFound {
				t.Errorf("HasSensitiveIdentifier = %v, 期望 %v", found, tt.wantFound)
			}
			if found && name != tt.wantName {
				t.Errorf("敏感标识符 = %q, 期望 %q", name, tt.wantName)
			}
		})
	}
}

func TestAnalyzeLine_AssignedValue(t *testing.T) {
	ta := NewTokenAnalyzer()

	tests := []struct {
		line      string
		wantIdent string
		wantValue string
		wantOk    bool
	}{
		{`password := "secret123"`, "password", `"secret123"`, true},
		{`apiKey = "sk-abc123"`, "apiKey", `"sk-abc123"`, true},
		{`host := "localhost"`, "host", `"localhost"`, true},
		{`x := 42`, "", "", false},           // 整数不是字符串
		{`fmt.Println("hi")`, "", "", false}, // 函数调用不是赋值
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			analysis := ta.AnalyzeLine(tt.line, 1)
			ident, value, ok := analysis.GetAssignedValue()
			if ok != tt.wantOk {
				t.Errorf("GetAssignedValue ok = %v, 期望 %v", ok, tt.wantOk)
			}
			if ok {
				if ident != tt.wantIdent {
					t.Errorf("identifier = %q, 期望 %q", ident, tt.wantIdent)
				}
				if value != tt.wantValue {
					t.Errorf("value = %q, 期望 %q", value, tt.wantValue)
				}
			}
		})
	}
}

func TestAnalyzeLine_StructFieldAssignment(t *testing.T) {
	a := NewTokenAnalyzer()

	tests := []struct {
		line      string
		wantIdent string
		wantValue string
		wantOk    bool
	}{
		// 结构体字段：APIKey string = "secret"
		{`APIKey    string = "sk-abc123secretkey2024"`, "APIKey", `"sk-abc123secretkey2024"`, true},
		// 结构体字段：DBPassword string = "admin123"
		{`DBPassword string = "admin123456"`, "DBPassword", `"admin123456"`, true},
		// 普通赋值
		{`password := "secret123"`, "password", `"secret123"`, true},
		// var 声明
		{`var apiKey = "sk-abc123"`, "apiKey", `"sk-abc123"`, true},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			analysis := a.AnalyzeLine(tt.line, 1)
			ident, value, ok := analysis.GetAssignedValue()
			if ok != tt.wantOk {
				t.Errorf("GetAssignedValue ok = %v, 期望 %v", ok, tt.wantOk)
			}
			if ok {
				if ident != tt.wantIdent {
					t.Errorf("identifier = %q, 期望 %q", ident, tt.wantIdent)
				}
				if value != tt.wantValue {
					t.Errorf("value = %q, 期望 %q", value, tt.wantValue)
				}
			}
		})
	}
}

func TestAnalyzeLine_CompleteLine(t *testing.T) {
	ta := NewTokenAnalyzer()

	analysis := ta.AnalyzeLine(`if err != nil { return fmt.Errorf("failed: %w", err) }`, 15)

	// 应该有 if, return, 函数调用等
	hasIf := false
	hasReturn := false
	for _, f := range analysis.Facts {
		if f.Kind == FactIf {
			hasIf = true
		}
		if f.Kind == FactReturn {
			hasReturn = true
		}
	}

	if !hasIf {
		t.Error("应检测到 if 关键字")
	}
	if !hasReturn {
		t.Error("应检测到 return 关键字")
	}
}

func TestAnalyzeLine_IncompleteCode(t *testing.T) {
	ta := NewTokenAnalyzer()

	// 不完整的代码（diff 片段）
	analysis := ta.AnalyzeLine(`+password := "secret`, 1)

	// scanner 应该能处理不完整的字符串
	// 不应该 panic
	if analysis.Line != 1 {
		t.Errorf("Line = %d, 期望 1", analysis.Line)
	}
}

func TestAnalyzeLine_EmptyLine(t *testing.T) {
	ta := NewTokenAnalyzer()

	analysis := ta.AnalyzeLine("", 1)

	if len(analysis.Facts) != 0 {
		t.Errorf("空行应无 facts，得到 %d", len(analysis.Facts))
	}
}

func TestAnalyzeLine_Comment(t *testing.T) {
	ta := NewTokenAnalyzer()

	analysis := ta.AnalyzeLine(`// TODO: fix this`, 1)

	found := false
	for _, f := range analysis.Facts {
		if f.Kind == FactComment {
			found = true
		}
	}
	if !found {
		t.Error("应检测到注释")
	}
}

func TestAnalyzeLines_Multiple(t *testing.T) {
	ta := NewTokenAnalyzer()

	lines := []string{
		`password := "secret123"`,
		`fmt.Println(password)`,
		`defer conn.Close()`,
	}

	results := ta.AnalyzeLines(lines)

	if len(results) != 3 {
		t.Fatalf("结果数量 = %d, 期望 3", len(results))
	}

	// 第 1 行：有敏感标识符和赋值
	found1, _ := results[0].HasSensitiveIdentifier()
	if !found1 {
		t.Error("第 1 行应有敏感标识符")
	}

	// 第 3 行：有 defer
	if !results[2].HasDefer() {
		t.Error("第 3 行应有 defer")
	}
}

func TestAnalyzeLine_FunctionCall(t *testing.T) {
	ta := NewTokenAnalyzer()

	analysis := ta.AnalyzeLine(`result := doSomething(arg1, arg2)`, 1)

	// 应该有标识符和函数调用相关的 token
	hasIdent := false
	for _, f := range analysis.Facts {
		if f.Kind == FactIdentifier && f.Value == "doSomething" {
			hasIdent = true
		}
	}
	if !hasIdent {
		t.Error("应找到函数名 'doSomething'")
	}
}
