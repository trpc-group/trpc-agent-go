//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// Rule 定义一条代码评审规则。
type Rule struct {
	ID          string   `json:"id"`
	Category    Category `json:"category"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Patterns    []string `json:"patterns"`
	Suggestion  string   `json:"suggestion"`
	// Confidence 是规则产出 finding 的默认置信度；0 表示使用默认 1.0。
	// 启发式规则（正则无法完全证明）应下调置信度，使低置信度问题落入
	// warnings / needs_human_review，不混入高置信 findings（验收标准 2/7）。
	Confidence float64 `json:"confidence,omitempty"`
	compiledRe []*regexp.Regexp
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
					if rule.Confidence > 0 {
						f.Confidence = rule.Confidence
					}
					findings = append(findings, f)
				}
			}
		}
	}

	// 资源/DB 启发式二次验证：文件新增行中已有 defer X.Close()，
	// 则跳过对 X 的"打开未关闭"命中，降低启发式规则的误报率。
	findings = rs.filterPairedClose(df, findings)

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
// 这些规则在单行粒度上无法可靠判定（正则只看一行，看不到文件其余部分），
// 因此在文件粒度用组合信号判定，避免逐行误报（验收标准 2：误报率 ≤15%）。
func (rs *RuleScanner) checkFileLevel(df DiffFile) []Finding {
	var out []Finding
	added := df.AddedLines()

	// 并发组合信号：文件同时存在 goroutine 启动 + 裸 for/select 阻塞循环，
	// 且没有任何 ctx.Done() 取消信号时，才报一条"无退出机制"。
	hasGo := lineMatchesAny(added, `go\s+func\s*\(`) || lineMatchesAny(added, `go\s+\w+\(`)
	hasBlockLoop := lineMatchesAny(added, `for\s*\{`) || lineMatchesAny(added, `select\s*\{`)
	hasCtxDone := lineMatchesAny(added, `ctx\.Done\(`)
	if hasGo && hasBlockLoop && !hasCtxDone {
		if ln := firstLineMatching(added, `go\s+(?:func\s*\(|\w+\()`); ln != nil {
			f := NewFinding(
				SeverityHigh,
				CategoryConcurrency,
				df.NewPath,
				ln.NewNo,
				"goroutine 内存在无退出机制的阻塞循环",
				strings.TrimSpace(ln.Content),
				"在 select 中加入 case <-ctx.Done() 分支，或在 goroutine 中使用可取消的 context / channel 控制生命周期。",
				"rule",
				"concur_context_not_checked_001",
			)
			f.Confidence = 0.7
			out = append(out, f)
		}
	}

	// 数据库连接生命周期：新增行有 sql.Open 但全文件无 .Ping 调用。
	if lineMatchesAny(added, `sql\.(?:Open|OpenDB)\s*\(`) && !lineMatchesAny(added, `\.Ping\s*\(`) {
		if ln := firstLineMatching(added, `sql\.(?:Open|OpenDB)\s*\(`); ln != nil {
			f := NewFinding(
				SeverityMedium,
				CategoryDBLifecycle,
				df.NewPath,
				ln.NewNo,
				"数据库连接后未进行 Ping 验证",
				strings.TrimSpace(ln.Content),
				"在 sql.Open 后立即调用 db.Ping() 验证数据库连接。",
				"rule",
				"db_no_ping_001",
			)
			f.Confidence = 0.5
			out = append(out, f)
		}
	}

	return out
}

// reDeferClose 匹配 defer X.Close() / defer resp.Body.Close() 这类语句。
var reDeferClose = regexp.MustCompile(`defer\s+([a-zA-Z_][\w.]*)\s*\.\s*Close\s*\(`)

// filterPairedClose 对"打开未关闭"类启发式 finding 做文件级二次验证：
// 若文件新增行内已出现 defer <var>.Close()，则说明资源有释放路径，
// 跳过对同一变量的未关闭命中，降低误报（验收标准 2）。
func (rs *RuleScanner) filterPairedClose(df DiffFile, findings []Finding) []Finding {
	deferred := map[string]bool{}
	for _, ln := range df.AddedLines() {
		if m := reDeferClose.FindStringSubmatch(ln.Content); m != nil {
			deferred[m[1]] = true
		}
	}
	if len(deferred) == 0 {
		return findings
	}

	out := findings[:0]
	for _, f := range findings {
		switch f.RuleID {
		case "res_open_without_close_001", "res_http_body_not_closed_001", "db_no_close_001":
			if v := extractOpenVar(f.Evidence); v != "" && deferredPrefix(deferred, v) {
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

// extractOpenVar 从"打开资源"语句中提取被赋值的变量名。
// 例如 `f, err := os.Open(...)` -> "f"；`db, _ := sql.Open(...)` -> "db"。
func extractOpenVar(evidence string) string {
	m := regexp.MustCompile(`^\s*([a-zA-Z_]\w*)`).FindStringSubmatch(evidence)
	if m == nil {
		return ""
	}
	return m[1]
}

// deferredPrefix 判断 deferred 集合中是否存在 v 或其属性（如 resp.Body）。
func deferredPrefix(deferred map[string]bool, v string) bool {
	if deferred[v] {
		return true
	}
	for k := range deferred {
		if strings.HasPrefix(k, v+".") {
			return true
		}
	}
	return false
}

// lineMatchesAny 判断新增行中是否存在匹配指定正则的行。
func lineMatchesAny(lines []Line, pattern string) bool {
	re := regexp.MustCompile(pattern)
	for _, ln := range lines {
		if re.MatchString(ln.Content) {
			return true
		}
	}
	return false
}

// firstLineMatching 返回新增行中第一个匹配指定正则的行，不存在时返回 nil。
func firstLineMatching(lines []Line, pattern string) *Line {
	re := regexp.MustCompile(pattern)
	for i := range lines {
		if re.MatchString(lines[i].Content) {
			return &lines[i]
		}
	}
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
		// 只对新增文件（OldPath == /dev/null）检查测试缺失，
		// 避免修改已有文件时对历史文件误报。
		if df.GoFile() && !hasTest[df.NewPath] && df.OldPath == "/dev/null" {
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
			Severity:    SeverityCritical,
			Title:       "硬编码的 API Key 或密钥",
			Description: "检测到代码中包含硬编码的 API Key、token 或密钥字符串。",
			// Patterns 由 sensitivePatterns 注册表派生，保证检出侧与脱敏侧
			// （MaskSensitive）大小写/格式一致，消除检出命中但脱敏不到的漂移。
			Patterns:   credentialPatterns(),
			Suggestion: "将密钥存储在环境变量中，使用 os.Getenv() 读取。",
		},
		{
			ID: "sec_sql_injection_001", Category: CategorySecurity,
			Severity:    SeverityCritical,
			Title:       "潜在的 SQL 注入风险",
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
			Severity:    SeverityCritical,
			Title:       "命令注入风险",
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
		// 注：concur_context_not_checked_001 已改为文件级组合信号（见 checkFileLevel）：
		// 行级 `for\s+\{`/`select\s+\{` 会误报一切 for/select，是此前的误报主源。
		{
			ID: "concur_goroutine_leak_001", Category: CategoryConcurrency,
			Severity:    SeverityHigh,
			Title:       "goroutine 可能泄漏（缺少 context 取消）",
			Description: "启动的匿名 goroutine 没有句柄可 join / cancel，可能泄漏。",
			// 只匹配匿名 goroutine `go func(...){}`（无句柄，是可靠的泄漏信号）；
			// 命名调用 `go worker()` 通常自带 channel/context 关闭机制，不再报。
			Patterns: []string{
				`go\s+func\s*\(`,
			},
			Confidence: 0.8,
			Suggestion: "使用 context.Context 或 channel 来控制 goroutine 的生命周期。",
		},

		// ===== 资源管理 =====
		{
			ID: "res_open_without_close_001", Category: CategoryResource,
			Severity:    SeverityHigh,
			Title:       "打开的资源可能未关闭",
			Description: "os.Open/创建的资源未看到对应的 defer Close()。",
			Patterns: []string{
				`os\.Open\s*\(`,
				`os\.Create\s*\(`,
				`os\.OpenFile\s*\(`,
				`net\.Listen\s*\(`,
				`net\.Dial\s*\(`,
			},
			// 启发式：行级无法确认是否另有 defer Close()，
			// 由 filterPairedClose 做文件级二次验证。
			Confidence: 0.8,
			Suggestion: "在获取资源后立即使用 defer x.Close() 确保资源被释放。",
		},
		{
			ID: "res_http_body_not_closed_001", Category: CategoryResource,
			Severity:    SeverityMedium,
			Title:       "HTTP 响应 Body 可能未关闭",
			Description: "http.Get/Post 的 resp.Body 需要关闭。",
			Patterns: []string{
				`http\.Get\s*\(`,
				`http\.Post\s*\(`,
				`http\.PostForm\s*\(`,
				`client\.Do\s*\(`,
			},
			// 启发式：filterPairedClose 会跳过已有 defer resp.Body.Close() 的情况。
			Confidence: 0.8,
			Suggestion: "使用 defer resp.Body.Close() 确保 HTTP 响应体被关闭。",
		},

		// ===== 错误处理 =====
		{
			ID: "err_unchecked_001", Category: CategoryErrorHandling,
			Severity:    SeverityHigh,
			Title:       "未检查的错误返回值",
			Description: "忽略的调用确知返回 error，返回值被赋值给 _。",
			// 白名单式：只匹配确知返回 error 的调用，避免误报
			// `_ = fmt.Sprintf(...)` 这类非 error 调用（此前的误报源）。
			Patterns: []string{
				`_\s*=\s*(?:os|ioutil)\.(?:Open|Create|OpenFile|Remove|RemoveAll|Rename|Mkdir|MkdirAll|MkdirTemp|WriteFile|ReadFile|ReadDir|Stat|Truncate|Chmod|Chown|Symlink)\s*\(`,
				`_\s*=\s*(?:db|conn|rows|stmt|tx)\.(?:Exec|ExecContext|Query|QueryContext|QueryRow|QueryRowContext|Close|Commit|Rollback|Scan|Next)\s*\(`,
				`_\s*=\s*http\.(?:Get|Post|PostForm|Do)\s*\(`,
				`_\s*=\s*(?:json|xml)\.(?:Marshal|Unmarshal)\s*\(`,
				`_\s*=\s*(?:io|os)\.(?:Copy|CopyN)\s*\(`,
				`_\s*=\s*cmd\.(?:Run|Start|Wait|CombinedOutput)\s*\(`,
			},
			Confidence: 0.8,
			Suggestion: "使用 if err != nil 检查错误返回值，不要用 _ 忽略。",
		},
		{
			ID: "err_fmt_errorf_without_verbfmt_001", Category: CategoryErrorHandling,
			Severity:    SeverityLow,
			Title:       "错误信息中缺少上下文",
			Description: "fmt.Errorf 只包含 %w 或 %v 格式，未提供足够的上下文信息。",
			Patterns: []string{
				`fmt\.Errorf\s*\(\s*"%w"`,
			},
			Suggestion: "在错误信息中添加操作上下文：fmt.Errorf(\"failed to X: %w\", err)。",
		},

		// ===== 数据库生命周期 =====
		// 注：db_no_ping_001 已改为文件级信号（新增行 sql.Open 且全文件无 .Ping），
		// 见 checkFileLevel —— 行级 `sql.Open(` 会把随后调用 db.Ping() 的正常代码一起误报。
		{
			ID: "db_no_close_001", Category: CategoryDBLifecycle,
			Severity:    SeverityMedium,
			Title:       "数据库连接可能未关闭",
			Description: "检测到 sql.Open 但未看到对应的 defer db.Close()。",
			Patterns: []string{
				`sql\.Open\s*\(\s*"`,
			},
			// 启发式：行级无法确认是否另有 defer db.Close()，
			// 由 filterPairedClose 做文件级二次验证，命中已配 defer 则跳过。
			Confidence: 0.5,
			Suggestion: "创建数据库连接后使用 defer db.Close() 确保资源被释放。",
		},

		// ===== 敏感信息 =====
		{
			ID: "sens_credit_card_001", Category: CategorySensitive,
			Severity:    SeverityCritical,
			Title:       "可能泄露信用卡号",
			Description: "检测到类似信用卡号的数字序列。",
			Patterns: []string{
				`\b\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`,
			},
			Suggestion: "移除或脱敏处理信用卡号，使用 tokenization 替代。",
		},
		{
			ID: "sens_private_key_001", Category: CategorySensitive,
			Severity:    SeverityCritical,
			Title:       "可能泄露私钥",
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

// Check 检查命令是否可以安全执行。
func (sg *SafetyGate) Check(command string) *safety.ScanReport {
	report := sg.scanner.Scan(context.Background(), safety.ScanRequest{
		ToolName: "code_review_sandbox",
		Command:  command,
		Backend:  "container",
	})
	return &report
}
