//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// Rule 定义一条代码评审规则。
type Rule struct {
	ID           string    `json:"id"`
	Category     Category  `json:"category"`
	Severity     Severity  `json:"severity"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Patterns     []string  `json:"patterns"`
	Suggestion   string    `json:"suggestion"`
	compiledRe   []*regexp.Regexp
}

// RulesConfig 是规则配置文件的顶层结构。
type RulesConfig struct {
	Rules []Rule `json:"rules"`
}

// RuleScanner 基于正则规则的代码扫描器。
type RuleScanner struct {
	rules []Rule
}

// NewRuleScanner 创建规则扫描器，预编译所有正则。
func NewRuleScanner() *RuleScanner {
	rs := &RuleScanner{
		rules: defaultRules(),
	}
	rs.compile()
	return rs
}

// ScanFile 对 diff 中单个文件的新增行进行规则扫描，返回 findings。
func (rs *RuleScanner) ScanFile(df DiffFile) []Finding {
	var findings []Finding

	for _, hunk := range df.Hunks {
		for _, line := range hunk.Lines {
			if line.Type != LineAdd {
				continue
			}
			for _, rule := range rs.rules {
				if rs.matchRule(rule, line.Content) {
					f := NewFinding(
						rule.Severity,
						rule.Category,
						df.NewPath,
						line.NewNo,
						rule.Title,
						strings.TrimSpace(line.Content),
						rule.Suggestion,
						"rule",
						rule.ID,
					)
					findings = append(findings, f)
				}
			}
		}
	}

	// 文件级别检查
	findings = append(findings, rs.checkFileLevel(df)...)

	return findings
}

// matchRule 检查一行是否匹配某个规则的所有模式。
func (rs *RuleScanner) matchRule(rule Rule, line string) bool {
	for _, re := range rule.compiledRe {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// checkFileLevel 执行文件级别的检查。
func (rs *RuleScanner) checkFileLevel(df DiffFile) []Finding {
	// 文件级别检查由 CheckMissingTests 处理，
	// 需要调用方提供仓库文件列表。
	// 此处不做 inline 检查以保证 ScanFile 的独立可用性。
	return nil
}

// CheckMissingTests 检查是否缺少对某些文件的测试覆盖。
// 需传入仓库中所有 Go 文件列表。
func (rs *RuleScanner) CheckMissingTests(allFiles []string, changedFiles []DiffFile) []Finding {
	var findings []Finding

	hasTest := make(map[string]bool)
	for _, f := range allFiles {
		if strings.HasSuffix(f, "_test.go") {
			baseFile := strings.TrimSuffix(f, "_test.go") + ".go"
			hasTest[baseFile] = true
		}
	}

	for _, df := range changedFiles {
		if df.GoFile() && !hasTest[df.NewPath] {
			f := NewFinding(
				SeverityWarning,
				CategoryTesting,
				df.NewPath,
				0,
				"缺少对应测试文件",
				fmt.Sprintf("新文件 %s 没有对应的 _test.go 文件", df.NewPath),
				fmt.Sprintf("建议创建 %s 并添加单元测试", strings.TrimSuffix(df.NewPath, ".go")+"_test.go"),
				"rule",
				"test_missing_001",
			)
			f.Confidence = 0.6
			findings = append(findings, f)
		}
	}

	return findings
}

// compile 预编译所有规则的 regex pattern。
func (rs *RuleScanner) compile() {
	for i := range rs.rules {
		for _, pattern := range rs.rules[i].Patterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				fmt.Fprintf(os.Stderr,
					"code_review_agent: 规则 %q 的正则 %q 无效: %v (跳过)\n",
					rs.rules[i].ID, pattern, err,
				)
				continue
			}
			rs.rules[i].compiledRe = append(rs.rules[i].compiledRe, re)
		}
	}
}

// defaultRules 返回内置的代码评审规则集。
func defaultRules() []Rule {
	return []Rule{
		// ===== 安全风险 =====
		{
			ID: "sec_hardcoded_key_001", Category: CategorySecurity,
			Severity: SeverityCritical,
			Title:    "硬编码的 API Key 或密钥",
			Description: "检测到代码中包含硬编码的 API Key、token 或密钥字符串。",
			Patterns: []string{
				`api[Kk]ey\s*[:=]\s*"[^"]{20,}"`,
				`api[Kk]ey\s*[:=]\s*'[^']{20,}'`,
				`[Ss]ecret\s*[:=]\s*"[^"]{10,}"`,
				`[Pp]assword\s*[:=]\s*"[^"]{3,}"`,
				`[Tt]oken\s*[:=]\s*"[^"]{10,}"`,
				`sk-[A-Za-z0-9]{20,}`,
				`AKIA[0-9A-Z]{16}`,
				`github_pat_[A-Za-z0-9_]{20,}`,
			},
			Suggestion: "将密钥存储在环境变量中，使用 os.Getenv() 读取。",
		},
		{
			ID: "sec_sql_injection_001", Category: CategorySecurity,
			Severity: SeverityCritical,
			Title:    "潜在的 SQL 注入风险",
			Description: "使用 fmt.Sprintf 拼接 SQL 查询可能导致 SQL 注入。",
			Patterns: []string{
				`fmt\.Sprintf\s*\(\s*".*SELECT\s`,
				`fmt\.Sprintf\s*\(\s*".*INSERT\s`,
				`fmt\.Sprintf\s*\(\s*".*UPDATE\s`,
				`fmt\.Sprintf\s*\(\s*".*DELETE\s`,
				`fmt\.Sprintf\s*\(\s*".*DROP\s`,
			},
			Suggestion: "使用参数化查询（$1, $2...）替代字符串拼接。",
		},
		{
			ID: "sec_command_injection_001", Category: CategorySecurity,
			Severity: SeverityCritical,
			Title:    "命令注入风险",
			Description: "检测到直接使用外部输入拼接 shell 命令。",
			Patterns: []string{
				`exec\.Command\s*\(\s*"sh"\s*,\s*"-c"`,
				`exec\.Command\s*\(\s*"bash"\s*,\s*"-c"`,
				`os\.Exec\s*\(`,
				`syscall\.Exec\s*\(`,
			},
			Suggestion: "避免使用 sh -c 直接执行用户输入；使用 exec.Command 的参数列表形式。",
		},

		// ===== goroutine / context 泄漏 =====
		{
			ID: "concur_goroutine_leak_001", Category: CategoryConcurrency,
			Severity: SeverityHigh,
			Title:    "goroutine 可能泄漏（缺少 context 取消）",
			Description: "启动的 goroutine 没有使用 context 控制生命周期，可能导致泄漏。",
			Patterns: []string{
				`go\s+func\s*\(\s*\)`,
				`go\s+\w+\(`,
			},
			Suggestion: "使用 context.Context 或 channel 来控制 goroutine 的生命周期。",
		},
		{
			ID: "concur_context_not_checked_001", Category: CategoryConcurrency,
			Severity: SeverityHigh,
			Title:    "未检查 context 取消",
			Description: "在循环或阻塞操作中未检查 ctx.Done()。",
			Patterns: []string{
				`for\s+\{`,
				`select\s+\{`,
			},
			Suggestion: "在 select 中添加 case <-ctx.Done() 分支。",
		},

		// ===== 资源管理 =====
		{
			ID: "res_open_without_close_001", Category: CategoryResource,
			Severity: SeverityHigh,
			Title:    "打开的资源可能未关闭",
			Description: "os.Open/创建的资源未看到对应的 defer Close()。",
			Patterns: []string{
				`os\.Open\s*\(`,
				`os\.Create\s*\(`,
				`os\.OpenFile\s*\(`,
				`net\.Listen\s*\(`,
				`net\.Dial\s*\(`,
			},
			Suggestion: "在获取资源后立即使用 defer x.Close() 确保资源被释放。",
		},
		{
			ID: "res_http_body_not_closed_001", Category: CategoryResource,
			Severity: SeverityMedium,
			Title:    "HTTP 响应 Body 可能未关闭",
			Description: "http.Get/Post 的 resp.Body 需要关闭。",
			Patterns: []string{
				`http\.Get\s*\(`,
				`http\.Post\s*\(`,
				`http\.PostForm\s*\(`,
				`client\.Do\s*\(`,
			},
			Suggestion: "使用 defer resp.Body.Close() 确保 HTTP 响应体被关闭。",
		},

		// ===== 错误处理 =====
		{
			ID: "err_unchecked_001", Category: CategoryErrorHandling,
			Severity: SeverityHigh,
			Title:    "未检查的错误返回值",
			Description: "函数返回 error 但调用方未检查（赋值给 _ 或未赋值）。",
			Patterns: []string{
				`_\s*,\s*_\s*:=\s*\w+\(`,
				`_\s*=\s*\w+\(`,
			},
			Suggestion: "使用 if err != nil 检查错误返回值，不要用 _ 忽略。",
		},
		{
			ID: "err_fmt_errorf_without_verbfmt_001", Category: CategoryErrorHandling,
			Severity: SeverityLow,
			Title:    "错误信息中缺少上下文",
			Description: "fmt.Errorf 只包含 %w 或 %v 格式，未提供足够的上下文信息。",
			Patterns: []string{
				`fmt\.Errorf\s*\(\s*"%w"`,
			},
			Suggestion: "在错误信息中添加操作上下文：fmt.Errorf(\"failed to X: %w\", err)。",
		},

		// ===== 数据库生命周期 =====
		{
			ID: "db_no_ping_001", Category: CategoryDBLifecycle,
			Severity: SeverityMedium,
			Title:    "数据库连接后未进行 Ping 验证",
			Description: "sql.Open 后未调用 db.Ping() 验证连接是否有效。",
			Patterns: []string{
				`sql\.Open\s*\(`,
				`sql\.OpenDB\s*\(`,
			},
			Suggestion: "在 sql.Open 后立即调用 db.Ping() 验证数据库连接。",
		},
		{
			ID: "db_no_close_001", Category: CategoryDBLifecycle,
			Severity: SeverityMedium,
			Title:    "数据库连接可能未关闭",
			Description: "检测到 sql.Open 但未看到对应的 defer db.Close()。",
			Patterns: []string{
				`sql\.Open\s*\(\s*"`,
			},
			Suggestion: "创建数据库连接后使用 defer db.Close() 确保资源被释放。",
		},

		// ===== 敏感信息 =====
		{
			ID: "sens_credit_card_001", Category: CategorySensitive,
			Severity: SeverityCritical,
			Title:    "可能泄露信用卡号",
			Description: "检测到类似信用卡号的数字序列。",
			Patterns: []string{
				`\b\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`,
			},
			Suggestion: "移除或脱敏处理信用卡号，使用 tokenization 替代。",
		},
		{
			ID: "sens_private_key_001", Category: CategorySensitive,
			Severity: SeverityCritical,
			Title:    "可能泄露私钥",
			Description: "检测到包含 BEGIN PRIVATE KEY 的内容。",
			Patterns: []string{
				`BEGIN\s+(RSA\s+)?PRIVATE\s+KEY`,
				`BEGIN\s+EC\s+PRIVATE\s+KEY`,
			},
			Suggestion: "不要在代码中硬编码私钥，使用密钥管理服务。",
		},
	}
}

// SafetyScanner 包装器，将 safety.Scanner 适配到代码评审流程。
type SafetyGate struct {
	scanner *safety.Scanner
}

// NewSafetyGate 创建安全门禁。
func NewSafetyGate() *SafetyGate {
	return &SafetyGate{
		scanner: safety.NewScanner(nil),
	}
}

// Check 检查命令是否可以安全执行。返回 nil 表示通过。
func (sg *SafetyGate) Check(command string) *safety.ScanReport {
	report := sg.scanner.Scan(nil, safety.ScanRequest{
		ToolName: "code_review_sandbox",
		Command:  command,
		Backend:  "container",
	})
	return &report
}
