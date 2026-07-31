//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rules 提供审查规则引擎
package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// Rule 审查规则接口
type Rule interface {
	ID() string
	Name() string
	Category() string
	Severity() string
	Check(file *input.DiffFile, changes []input.Change) []store.Finding
}

// RuleEngine 规则引擎
type RuleEngine struct {
	rules []Rule
}

// NewRuleEngine 创建规则引擎
func NewRuleEngine() *RuleEngine {
	engine := &RuleEngine{
		rules: make([]Rule, 0),
	}

	// Register built-in rules (with pre-compiled regex patterns)
	// Note: ErrorHandlingRule, NilCheckRule, and DeferInLoopRule are disabled
	// because they require AST analysis for accurate detection
	engine.Register(NewSQLInjectionRule())
	engine.Register(NewSensitiveInfoRule())
	engine.Register(&GoroutineLeakRule{})
	engine.Register(NewContextLeakRule())
	engine.Register(NewResourceLeakRule())
	engine.Register(NewMissingTestRule())
	engine.Register(NewDatabaseTransactionRule())
	engine.Register(NewErrorWrapRule())

	return engine
}

// Register 注册规则
func (e *RuleEngine) Register(rule Rule) {
	e.rules = append(e.rules, rule)
}

// CheckDiffResult 检查整个 diff 结果
func (e *RuleEngine) CheckDiffResult(taskID string, result *input.DiffParseResult) []store.Finding {
	findings := make([]store.Finding, 0)

	for _, file := range result.Files {
		for _, rule := range e.rules {
			ruleFindings := rule.Check(&file, getAllChanges(file))
			for i := range ruleFindings {
				ruleFindings[i].TaskID = taskID
			}
			findings = append(findings, ruleFindings...)
		}
	}

	return findings
}

func getAllChanges(file input.DiffFile) []input.Change {
	changes := make([]input.Change, 0)
	for _, hunk := range file.Hunks {
		changes = append(changes, hunk.Changes...)
	}
	return changes
}

// DeduplicateFindings 去重降噪
func DeduplicateFindings(findings []store.Finding) ([]store.Finding, []store.Finding) {
	seen := make(map[string]bool)
	unique := make([]store.Finding, 0)
	warnings := make([]store.Finding, 0)

	for _, f := range findings {
		// 统一使用小写路径，避免 API.go 和 api.go 被认为是不同文件
		filePath := strings.ToLower(f.File)
		key := filePath + ":" + itoa(f.Line) + ":" + strings.ToLower(strings.TrimSpace(f.Category))
		if seen[key] {
			continue
		}
		seen[key] = true

		if f.Confidence < 0.7 {
			warnings = append(warnings, f)
		} else {
			unique = append(unique, f)
		}
	}

	return unique, warnings
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

// ============ 内置规则实现 ============

// SQLInjectionRule SQL 注入规则
type SQLInjectionRule struct {
	patterns []*regexp.Regexp
}

// NewSQLInjectionRule 创建 SQL 注入规则（预编译正则）
func NewSQLInjectionRule() *SQLInjectionRule {
	return &SQLInjectionRule{
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)fmt\.Sprintf\s*\(\s*".*SELECT.*%s`),
			regexp.MustCompile(`(?i)fmt\.Sprintf\s*\(\s*".*INSERT.*%s`),
			regexp.MustCompile(`(?i)fmt\.Sprintf\s*\(\s*".*UPDATE.*%s`),
			regexp.MustCompile(`(?i)fmt\.Sprintf\s*\(\s*".*DELETE.*%s`),
		},
	}
}

func (r *SQLInjectionRule) ID() string       { return "SEC001" }
func (r *SQLInjectionRule) Name() string     { return "SQL Injection Risk" }
func (r *SQLInjectionRule) Category() string { return "security" }
func (r *SQLInjectionRule) Severity() string { return "critical" }

func (r *SQLInjectionRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}
		for _, pattern := range r.patterns {
			if pattern.MatchString(change.Content) {
				line := change.NewLine
				if line == 0 {
					line = change.OldLine
				}
				findings = append(findings, store.Finding{
					Severity:       r.Severity(),
					Category:       r.Category(),
					File:           file.Path,
					Line:           line,
					Title:          r.Name(),
					Description:    "SQL query constructed using string formatting, which may be vulnerable to SQL injection",
					Evidence:       strings.TrimSpace(change.Content),
					Recommendation: "Use parameterized queries with db.Query(\"SELECT * FROM users WHERE name = ?\", username)",
					Confidence:     0.95,
					Source:         "rule",
					RuleID:         r.ID(),
				})
				break
			}
		}
	}

	return findings
}

// SensitiveInfoRule 敏感信息规则
type SensitiveInfoRule struct {
	patterns []*regexp.Regexp
}

// NewSensitiveInfoRule 创建敏感信息规则（预编译正则）
func NewSensitiveInfoRule() *SensitiveInfoRule {
	return &SensitiveInfoRule{
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*['"]?([A-Za-z0-9_\-]{20,})['"]?`),
			regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*['"]?([^\s'"]{8,})['"]?`),
			regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`),
			regexp.MustCompile(`(?i)-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`),
		},
	}
}

func (r *SensitiveInfoRule) ID() string       { return "SEC002" }
func (r *SensitiveInfoRule) Name() string     { return "Sensitive Information" }
func (r *SensitiveInfoRule) Category() string { return "security" }
func (r *SensitiveInfoRule) Severity() string { return "critical" }

func (r *SensitiveInfoRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}
		for _, pattern := range r.patterns {
			if pattern.MatchString(change.Content) {
				line := change.NewLine
				if line == 0 {
					line = change.OldLine
				}
				findings = append(findings, store.Finding{
					Severity:       r.Severity(),
					Category:       r.Category(),
					File:           file.Path,
					Line:           line,
					Title:          r.Name(),
					Description:    "Code contains sensitive information (API key, token, password, etc.)",
					Evidence:       "<redacted>",
					Recommendation: "Use environment variables or secrets manager",
					Confidence:     0.95,
					Source:         "rule",
					RuleID:         r.ID(),
				})
				break
			}
		}
	}

	return findings
}

// GoroutineLeakRule Goroutine 泄漏规则
type GoroutineLeakRule struct{}

func (r *GoroutineLeakRule) ID() string       { return "GR001" }
func (r *GoroutineLeakRule) Name() string     { return "Goroutine Leak" }
func (r *GoroutineLeakRule) Category() string { return "goroutine" }
func (r *GoroutineLeakRule) Severity() string { return "high" }

func (r *GoroutineLeakRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)
	inGoroutine := false
	goroutineLine := 0

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}

		if strings.Contains(change.Content, "go func()") || strings.Contains(change.Content, "go func(") {
			inGoroutine = true
			goroutineLine = change.NewLine
			if goroutineLine == 0 {
				goroutineLine = change.OldLine
			}
		}

		if inGoroutine && strings.Contains(change.Content, "for {") {
			findings = append(findings, store.Finding{
				Severity:       r.Severity(),
				Category:       r.Category(),
				File:           file.Path,
				Line:           goroutineLine,
				Title:          r.Name(),
				Description:    "Goroutine with infinite loop and no exit mechanism",
				Evidence:       strings.TrimSpace(change.Content),
				Recommendation: "Add a context or channel-based exit mechanism to the goroutine",
				Confidence:     0.80,
				Source:         "rule",
				RuleID:         r.ID(),
			})
			inGoroutine = false
		}
	}

	return findings
}

// ContextLeakRule Context 泄漏规则
type ContextLeakRule struct {
	bgPattern *regexp.Regexp
}

// NewContextLeakRule 创建 Context 泄漏规则（预编译正则）
func NewContextLeakRule() *ContextLeakRule {
	return &ContextLeakRule{
		bgPattern: regexp.MustCompile(`ctx\s*:=\s*context\.Background\(\)`),
	}
}

func (r *ContextLeakRule) ID() string       { return "GR002" }
func (r *ContextLeakRule) Name() string     { return "Context Leak" }
func (r *ContextLeakRule) Category() string { return "goroutine" }
func (r *ContextLeakRule) Severity() string { return "medium" }

func (r *ContextLeakRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}

		if r.bgPattern.MatchString(change.Content) {
			line := change.NewLine
			if line == 0 {
				line = change.OldLine
			}

			findings = append(findings, store.Finding{
				Severity:       r.Severity(),
				Category:       r.Category(),
				File:           file.Path,
				Line:           line,
				Title:          r.Name(),
				Description:    "Using context.Background() - consider passing context from parent",
				Evidence:       strings.TrimSpace(change.Content),
				Recommendation: "Pass context from parent function or use context.WithTimeout for cancellation",
				Confidence:     0.50,
				Source:         "rule",
				RuleID:         r.ID(),
			})
		}
	}

	return findings
}

// ResourceLeakRule 资源泄漏规则
type ResourceLeakRule struct {
	openPattern  *regexp.Regexp
	closePattern *regexp.Regexp
}

// NewResourceLeakRule 创建资源泄漏规则（预编译正则）
func NewResourceLeakRule() *ResourceLeakRule {
	return &ResourceLeakRule{
		openPattern:  regexp.MustCompile(`os\.Open\s*\(`),
		closePattern: regexp.MustCompile(`defer\s+.*\.Close\(\)`),
	}
}

func (r *ResourceLeakRule) ID() string       { return "RES001" }
func (r *ResourceLeakRule) Name() string     { return "Resource Leak" }
func (r *ResourceLeakRule) Category() string { return "resource" }
func (r *ResourceLeakRule) Severity() string { return "high" }

func (r *ResourceLeakRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)

	// Track open/close state per function scope
	hasOpen := false
	openLine := 0

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}

		// Check for new function declaration - reset state
		if strings.HasPrefix(strings.TrimSpace(change.Content), "func ") {
			hasOpen = false
			openLine = 0
		}

		if r.openPattern.MatchString(change.Content) {
			hasOpen = true
			openLine = change.NewLine
			if openLine == 0 {
				openLine = change.OldLine
			}
		}

		if hasOpen && r.closePattern.MatchString(change.Content) {
			hasOpen = false
			openLine = 0
		}
	}

	if hasOpen {
		findings = append(findings, store.Finding{
			Severity:       r.Severity(),
			Category:       r.Category(),
			File:           file.Path,
			Line:           openLine,
			Title:          r.Name(),
			Description:    "File opened with os.Open but not closed with defer",
			Evidence:       "os.Open(...) without defer .Close()",
			Recommendation: "Add 'defer f.Close()' after opening the file",
			Confidence:     0.90,
			Source:         "rule",
			RuleID:         r.ID(),
		})
	}

	return findings
}

// ErrorHandlingRule 错误处理规则
type ErrorHandlingRule struct {
	// 不返回错误的安全函数白名单
	safeFunctions map[string]bool
}

// NewErrorHandlingRule 创建错误处理规则
func NewErrorHandlingRule() *ErrorHandlingRule {
	return &ErrorHandlingRule{
		safeFunctions: map[string]bool{
			// 不返回错误的常见函数
			"fmt.Sprintf":       true,
			"fmt.Fprintf":       true,
			"fmt.Fprintln":      true,
			"fmt.Printf":        true,
			"fmt.Println":       true,
			"strings.Join":      true,
			"strings.Split":     true,
			"strings.Trim":      true,
			"strings.Replace":   true,
			"strings.Contains":  true,
			"strings.HasPrefix": true,
			"strings.HasSuffix": true,
			"len":               true,
			"cap":               true,
			"append":            true,
			"make":              true,
			"new":               true,
			"copy":              true,
			"delete":            true,
			"close":             true,
			"panic":             true,
			"recover":           true,
			"print":             true,
			"println":           true,
		},
	}
}

func (r *ErrorHandlingRule) ID() string       { return "ERR001" }
func (r *ErrorHandlingRule) Name() string     { return "Error Handling" }
func (r *ErrorHandlingRule) Category() string { return "error" }
func (r *ErrorHandlingRule) Severity() string { return "medium" }

func (r *ErrorHandlingRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	// 此规则在当前实现中禁用，因为误报率太高
	// 需要更精确的 AST 分析才能有效检测错误处理问题
	return []store.Finding{}
}

// MissingTestRule 测试缺失规则
type MissingTestRule struct {
	funcPattern *regexp.Regexp
}

// NewMissingTestRule 创建测试缺失规则（预编译正则）
func NewMissingTestRule() *MissingTestRule {
	return &MissingTestRule{
		funcPattern: regexp.MustCompile(`^func\s+([A-Z]\w*)\s*\(`),
	}
}

func (r *MissingTestRule) ID() string       { return "TEST001" }
func (r *MissingTestRule) Name() string     { return "Missing Test" }
func (r *MissingTestRule) Category() string { return "test" }
func (r *MissingTestRule) Severity() string { return "low" }

func (r *MissingTestRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)

	// 只检查 Go 文件
	if !strings.HasSuffix(file.Path, ".go") {
		return findings
	}

	// 跳过测试文件
	if strings.HasSuffix(file.Path, "_test.go") {
		return findings
	}

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}

		matches := r.funcPattern.FindStringSubmatch(change.Content)
		if matches != nil {
			funcName := matches[1]
			line := change.NewLine
			if line == 0 {
				line = change.OldLine
			}

			findings = append(findings, store.Finding{
				Severity:       r.Severity(),
				Category:       r.Category(),
				File:           file.Path,
				Line:           line,
				Title:          fmt.Sprintf("Missing Test for %s", funcName),
				Description:    fmt.Sprintf("Exported function %s does not have a corresponding test", funcName),
				Evidence:       strings.TrimSpace(change.Content),
				Recommendation: fmt.Sprintf("Add a test function Test%s", funcName),
				Confidence:     0.70,
				Source:         "rule",
				RuleID:         r.ID(),
			})
		}
	}

	return findings
}

// DatabaseTransactionRule 数据库事务规则
type DatabaseTransactionRule struct {
	beginPattern    *regexp.Regexp
	commitPattern   *regexp.Regexp
	rollbackPattern *regexp.Regexp
}

// NewDatabaseTransactionRule 创建数据库事务规则（预编译正则）
func NewDatabaseTransactionRule() *DatabaseTransactionRule {
	return &DatabaseTransactionRule{
		beginPattern:    regexp.MustCompile(`\.Begin\(\)`),
		commitPattern:   regexp.MustCompile(`\.Commit\(\)`),
		rollbackPattern: regexp.MustCompile(`defer\s+.*\.Rollback\(\)`),
	}
}

func (r *DatabaseTransactionRule) ID() string       { return "DB001" }
func (r *DatabaseTransactionRule) Name() string     { return "Database Transaction" }
func (r *DatabaseTransactionRule) Category() string { return "database" }
func (r *DatabaseTransactionRule) Severity() string { return "high" }

func (r *DatabaseTransactionRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)

	// Track transaction state per function scope
	hasBegin := false
	beginLine := 0

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}

		// Check for new function declaration - reset state
		if strings.HasPrefix(strings.TrimSpace(change.Content), "func ") {
			hasBegin = false
			beginLine = 0
		}

		if r.beginPattern.MatchString(change.Content) {
			hasBegin = true
			beginLine = change.NewLine
			if beginLine == 0 {
				beginLine = change.OldLine
			}
		}

		if hasBegin && (r.commitPattern.MatchString(change.Content) || r.rollbackPattern.MatchString(change.Content)) {
			hasBegin = false
			beginLine = 0
		}
	}

	if hasBegin {
		findings = append(findings, store.Finding{
			Severity:       r.Severity(),
			Category:       r.Category(),
			File:           file.Path,
			Line:           beginLine,
			Title:          r.Name(),
			Description:    "Transaction started with Begin() but no Commit() or defer Rollback() found",
			Evidence:       "tx, err := db.Begin()",
			Recommendation: "Add 'defer tx.Rollback()' after Begin() and call tx.Commit() on success",
			Confidence:     0.85,
			Source:         "rule",
			RuleID:         r.ID(),
		})
	}

	return findings
}

// NilCheckRule nil 检查规则
type NilCheckRule struct{}

func (r *NilCheckRule) ID() string       { return "GO001" }
func (r *NilCheckRule) Name() string     { return "Missing Nil Check" }
func (r *NilCheckRule) Category() string { return "error" }
func (r *NilCheckRule) Severity() string { return "medium" }

func (r *NilCheckRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	// 此规则在当前实现中禁用，因为误报率太高
	// 需要更精确的 AST 分析才能有效检测 nil 指针解引用
	return []store.Finding{}
}

// DeferInLoopRule defer 在循环中的规则
type DeferInLoopRule struct {
	forPattern   *regexp.Regexp
	deferPattern *regexp.Regexp
}

// NewDeferInLoopRule 创建 defer 循环规则（预编译正则）
func NewDeferInLoopRule() *DeferInLoopRule {
	return &DeferInLoopRule{
		forPattern:   regexp.MustCompile(`^\s*for\s`),
		deferPattern: regexp.MustCompile(`^\s*defer\s`),
	}
}

func (r *DeferInLoopRule) ID() string       { return "GO002" }
func (r *DeferInLoopRule) Name() string     { return "Defer In Loop" }
func (r *DeferInLoopRule) Category() string { return "resource" }
func (r *DeferInLoopRule) Severity() string { return "high" }

func (r *DeferInLoopRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	// 此规则在当前实现中禁用，因为字符串匹配无法可靠检测循环嵌套
	// 需要更精确的 AST 分析才能有效检测 defer in loop
	return []store.Finding{}
}

// ErrorWrapRule 错误包装规则
type ErrorWrapRule struct {
	errorVPattern *regexp.Regexp
}

// NewErrorWrapRule 创建错误包装规则（预编译正则）
func NewErrorWrapRule() *ErrorWrapRule {
	return &ErrorWrapRule{
		errorVPattern: regexp.MustCompile(`fmt\.Errorf\(.*%v.*err`),
	}
}

func (r *ErrorWrapRule) ID() string       { return "GO003" }
func (r *ErrorWrapRule) Name() string     { return "Error Wrap" }
func (r *ErrorWrapRule) Category() string { return "error" }
func (r *ErrorWrapRule) Severity() string { return "low" }

func (r *ErrorWrapRule) Check(file *input.DiffFile, changes []input.Change) []store.Finding {
	findings := make([]store.Finding, 0)

	for _, change := range changes {
		if change.Type != "add" {
			continue
		}

		if r.errorVPattern.MatchString(change.Content) {
			line := change.NewLine
			if line == 0 {
				line = change.OldLine
			}

			findings = append(findings, store.Finding{
				Severity:       r.Severity(),
				Category:       r.Category(),
				File:           file.Path,
				Line:           line,
				Title:          r.Name(),
				Description:    "Using %v instead of %w loses error chain",
				Evidence:       strings.TrimSpace(change.Content),
				Recommendation: "Use %w to preserve error chain: fmt.Errorf(\"...: %w\", err)",
				Confidence:     0.70,
				Source:         "rule",
				RuleID:         r.ID(),
			})
		}
	}

	return findings
}
