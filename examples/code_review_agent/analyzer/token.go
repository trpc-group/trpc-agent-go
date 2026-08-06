// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package analyzer

import (
	"go/scanner"
	"go/token"
	"strings"
)

// TokenFact 是从词法分析中提取的结构化事实。
//
// 与正则匹配不同，TokenFact 是语法感知的：
//   - 知道 "password" 是一个标识符（IDENT），不是普通文本
//   - 知道 "secret123" 是一个字符串字面量（STRING），不是普通文本
//   - 知道 ":=" 是赋值操作（DEFINE），不是普通文本
type TokenFact struct {
	Kind    TokenFactKind `json:"kind"`    // 事实类型
	Value   string        `json:"value"`   // 原始值
	Line    int           `json:"line"`    // 行号
	Column  int           `json:"column"`  // 列号
	Context string        `json:"context"` // 上下文（所在行的内容）
}

// TokenFactKind 事实类型。
type TokenFactKind string

const (
	FactIdentifier    TokenFactKind = "identifier"     // 标识符：password, apiKey, Config
	FactStringLiteral TokenFactKind = "string_literal" // 字符串字面量："secret123"
	FactIntLiteral    TokenFactKind = "int_literal"    // 整数字面量：42, 0xff
	FactAssignment    TokenFactKind = "assignment"     // 赋值：= 或 :=
	FactDefer         TokenFactKind = "defer"          // defer 语句
	FactGo            TokenFactKind = "go"             // go 语句
	FactReturn        TokenFactKind = "return"         // return 语句
	FactIf            TokenFactKind = "if"             // if 语句
	FactFor           TokenFactKind = "for"            // for 循环
	FactSelect        TokenFactKind = "select"         // select 语句
	FactChan          TokenFactKind = "chan"           // chan 操作
	FactPackage       TokenFactKind = "package"        // package 声明
	FactImport        TokenFactKind = "import"         // import 语句
	FactComment       TokenFactKind = "comment"        // 注释
)

// TokenAnalysis 是一行代码的词法分析结果。
type TokenAnalysis struct {
	Line    int         `json:"line"`
	Content string      `json:"content"`
	Facts   []TokenFact `json:"facts"`
}

// TokenAnalyzer 是基于 go/scanner 的词法分析器。
//
// 与 AST 分析器不同，TokenAnalyzer 不需要完整的 Go 文件，
// 可以分析 diff 中的单行代码或不完整的代码片段。
//
// 适用场景：
//   - 只有 diff，没有完整文件
//   - 代码片段不完整（缺少函数头、括号等）
//   - 需要快速扫描大量代码行
type TokenAnalyzer struct{}

// NewTokenAnalyzer 创建词法分析器。
func NewTokenAnalyzer() *TokenAnalyzer {
	return &TokenAnalyzer{}
}

// AnalyzeLine 分析单行代码，提取 token facts。
func (ta *TokenAnalyzer) AnalyzeLine(line string, lineNum int) TokenAnalysis {
	analysis := TokenAnalysis{
		Line:    lineNum,
		Content: line,
	}

	// 用 go/scanner 做词法分析
	// 包装成一个最小的 Go 文件，让 scanner 能处理
	src := "package _\n" + line

	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, []byte(src), nil, scanner.ScanComments)

	// 跳过第一行的 "package _"
	// 从第二行开始收集 facts
	inTargetLine := false

	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}

		position := fset.Position(pos)

		// 从第二行开始（跳过 package 声明）
		if position.Line >= 2 {
			inTargetLine = true
		}
		if !inTargetLine {
			continue
		}

		fact := ta.classifyToken(tok, lit, lineNum, position.Column)
		if fact != nil {
			analysis.Facts = append(analysis.Facts, *fact)
		}
	}

	return analysis
}

// AnalyzeLines 分析多行代码。
func (ta *TokenAnalyzer) AnalyzeLines(lines []string) []TokenAnalysis {
	var results []TokenAnalysis
	for i, line := range lines {
		results = append(results, ta.AnalyzeLine(line, i+1))
	}
	return results
}

// classifyToken 将 token 分类为 TokenFact。
func (ta *TokenAnalyzer) classifyToken(tok token.Token, lit string, line, col int) *TokenFact {
	fact := &TokenFact{
		Line:   line,
		Column: col,
	}

	switch {
	// 标识符
	case tok == token.IDENT:
		fact.Kind = FactIdentifier
		fact.Value = lit

	// 字符串字面量
	case tok == token.STRING:
		fact.Kind = FactStringLiteral
		fact.Value = lit

	// 整数字面量
	case tok == token.INT:
		fact.Kind = FactIntLiteral
		fact.Value = lit

	// 浮点字面量
	case tok == token.FLOAT:
		fact.Kind = FactIntLiteral
		fact.Value = lit

	// 赋值操作
	case tok == token.ASSIGN || tok == token.DEFINE:
		fact.Kind = FactAssignment
		fact.Value = tok.String()

	// 关键字
	case tok == token.DEFER:
		fact.Kind = FactDefer
		fact.Value = "defer"
	case tok == token.GO:
		fact.Kind = FactGo
		fact.Value = "go"
	case tok == token.RETURN:
		fact.Kind = FactReturn
		fact.Value = "return"
	case tok == token.IF:
		fact.Kind = FactIf
		fact.Value = "if"
	case tok == token.FOR:
		fact.Kind = FactFor
		fact.Value = "for"
	case tok == token.SELECT:
		fact.Kind = FactSelect
		fact.Value = "select"
	case tok == token.CHAN:
		fact.Kind = FactChan
		fact.Value = "chan"
	case tok == token.PACKAGE:
		fact.Kind = FactPackage
		fact.Value = "package"
	case tok == token.IMPORT:
		fact.Kind = FactImport
		fact.Value = "import"

	// 注释
	case tok == token.COMMENT:
		fact.Kind = FactComment
		fact.Value = lit

	default:
		// 其他 token 不生成 fact
		return nil
	}

	return fact
}

// ========== 便捷查询方法 ==========

// FindIdentifiers 查找所有标识符。
func (ta *TokenAnalysis) FindIdentifiers() []string {
	var ids []string
	for _, f := range ta.Facts {
		if f.Kind == FactIdentifier {
			ids = append(ids, f.Value)
		}
	}
	return ids
}

// FindStringLiterals 查找所有字符串字面量。
func (ta *TokenAnalysis) FindStringLiterals() []string {
	var strs []string
	for _, f := range ta.Facts {
		if f.Kind == FactStringLiteral {
			strs = append(strs, f.Value)
		}
	}
	return strs
}

// HasAssignment 检查是否有赋值操作。
func (ta *TokenAnalysis) HasAssignment() bool {
	for _, f := range ta.Facts {
		if f.Kind == FactAssignment {
			return true
		}
	}
	return false
}

// HasDefer 检查是否有 defer 语句。
func (ta *TokenAnalysis) HasDefer() bool {
	for _, f := range ta.Facts {
		if f.Kind == FactDefer {
			return true
		}
	}
	return false
}

// HasGoStatement 检查是否有 go 语句。
func (ta *TokenAnalysis) HasGoStatement() bool {
	for _, f := range ta.Facts {
		if f.Kind == FactGo {
			return true
		}
	}
	return false
}

// HasSensitiveIdentifier 检查是否有敏感标识符。
func (ta *TokenAnalysis) HasSensitiveIdentifier() (bool, string) {
	sensitive := []string{
		"password", "passwd", "pwd",
		"secret", "secretkey", "secret_key",
		"apikey", "api_key", "api-key",
		"token", "accesstoken", "access_token",
		"private_key", "privatekey",
		"credential", "dsn",
	}

	for _, f := range ta.Facts {
		if f.Kind == FactIdentifier {
			lower := strings.ToLower(f.Value)
			for _, s := range sensitive {
				if strings.Contains(lower, s) {
					return true, f.Value
				}
			}
		}
	}
	return false, ""
}

// GetAssignedValue 获取赋值语句右侧的值（如果有字符串字面量）。
//
// 支持多种赋值模式：
//   - password := "secret"     → ("password", "secret", true)
//   - password = "secret"      → ("password", "secret", true)
//   - APIKey string = "secret" → ("APIKey", "secret", true)  // 结构体字段
//   - var password = "secret"  → ("password", "secret", true)
func (ta *TokenAnalysis) GetAssignedValue() (identifier string, value string, ok bool) {
	var prevIdents []string // 收集赋值前的所有标识符
	var lastAssign bool

	for _, f := range ta.Facts {
		switch f.Kind {
		case FactIdentifier:
			prevIdents = append(prevIdents, f.Value)
			lastAssign = false
		case FactAssignment:
			lastAssign = true
		case FactStringLiteral:
			if lastAssign && len(prevIdents) > 0 {
				// 选择最像变量名的标识符（跳过类型名如 string, int, bool）
				ident := pickVariableName(prevIdents)
				if ident != "" {
					return ident, f.Value, true
				}
			}
			lastAssign = false
		default:
			lastAssign = false
		}
	}
	return "", "", false
}

// pickVariableName 从标识符列表中选择最像变量名的那个。
// 跳过 Go 内置类型名（string, int, bool, error 等）。
func pickVariableName(idents []string) string {
	goTypes := map[string]bool{
		"string": true, "int": true, "int8": true, "int16": true,
		"int32": true, "int64": true, "uint": true, "float32": true,
		"float64": true, "bool": true, "byte": true, "rune": true,
		"error": true, "any": true, "complex64": true, "complex128": true,
	}

	// 从后往前找，优先返回最接近赋值号的非类型标识符
	for i := len(idents) - 1; i >= 0; i-- {
		if !goTypes[idents[i]] {
			return idents[i]
		}
	}

	// 如果全是类型名，返回第一个
	if len(idents) > 0 {
		return idents[0]
	}
	return ""
}
