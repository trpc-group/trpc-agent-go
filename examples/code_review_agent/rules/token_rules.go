// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package rules 提供基于 Token Facts 的代码审查规则。
//
// 所有规则使用 go/scanner 提取的语法感知 token facts，
// 不依赖正则表达式，减少误报，提高可解释性。
package rules

import (
	"go/scanner"
	"go/token"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/analyzer"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// ========== SEC-AST-001: Token 感知的硬编码密钥检测 ==========

// TokenSecretRule 使用词法分析检测硬编码密钥。
//
// 检测策略：
//  1. 赋值语句中，左侧是敏感标识符，右侧是字符串字面量
//  2. 函数参数中，敏感标识符伴随可疑字符串
//
// 不会误报的情况：
//   - 注释里的 password
//   - password := os.Getenv("X")
//   - password := "your-password-here"（占位符）
type TokenSecretRule struct {
	analyzer *analyzer.TokenAnalyzer
}

// NewTokenSecretRule 创建 Token 感知的密钥检测规则实例。
func NewTokenSecretRule() *TokenSecretRule {
	return &TokenSecretRule{analyzer: analyzer.NewTokenAnalyzer()}
}

func (r *TokenSecretRule) ID() string                  { return "SEC-AST-001" }
func (r *TokenSecretRule) Name() string                { return "Token 感知的密钥检测" }
func (r *TokenSecretRule) Severity() findings.Severity { return findings.SeverityHigh }
func (r *TokenSecretRule) Category() findings.Category { return findings.CategorySecurity }

func (r *TokenSecretRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	var result []findings.Finding

	for _, line := range fd.AddedLines() {
		content := line.Content
		if content == "" {
			continue
		}

		trimmed := strings.TrimSpace(content)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		analysis := r.analyzer.AnalyzeLine(content, line.NewLine)

		// 检查 1：赋值语句中，左侧敏感标识符，右侧字符串字面量
		if ident, value, ok := analysis.GetAssignedValue(); ok {
			if isSensitiveIdent(ident) && !isLikelyNotSecret(value) {
				f := findings.NewFinding(
					r.Severity(), r.Category(), r.ID(),
					"Token 感知：疑似硬编码密钥",
					fd.NewPath, line.NewLine,
					content,
					"将密钥移至环境变量或密钥管理服务",
					0.90,
					"token:hardcoded_secret",
				)
				result = append(result, *f)
				continue
			}
		}

		// 检查 2：非赋值上下文中的敏感信息传递
		if found, name := analysis.HasSensitiveIdentifier(); found {
			if !analysis.HasAssignment() {
				strs := analysis.FindStringLiterals()
				for _, s := range strs {
					if !isLikelyNotSecret(s) {
						f := findings.NewFinding(
							findings.SeverityMedium, r.Category(), r.ID(),
							"Token 感知：疑似敏感信息传递",
							fd.NewPath, line.NewLine,
							content,
							"检查 "+name+" 是否包含敏感信息",
							0.70,
							"token:sensitive_param",
						)
						result = append(result, *f)
						break
					}
				}
			}
		}
	}

	return result, nil
}

// ========== SEC-AST-002: Token 感知的敏感信息泄漏检测 ==========

// TokenLeakRule 使用词法分析检测代码中泄漏的敏感信息。
//
// 检测策略：扫描新增行中的字符串字面量，检查是否包含：
//   - AWS Access Key 格式
//   - GitHub Token 格式
//   - 私钥头
//   - 数据库连接串（含密码）
//   - JWT Token
//   - 高熵字符串（可能是密钥）
//
// 与正则规则的区别：通过 token 分析确认是字符串字面量，
// 不会误匹配注释或变量名中的模式。
type TokenLeakRule struct {
	analyzer *analyzer.TokenAnalyzer
}

// NewTokenLeakRule 创建 Token 感知的敏感信息泄漏检测规则实例。
func NewTokenLeakRule() *TokenLeakRule {
	return &TokenLeakRule{analyzer: analyzer.NewTokenAnalyzer()}
}

func (r *TokenLeakRule) ID() string                  { return "SEC-AST-002" }
func (r *TokenLeakRule) Name() string                { return "Token 感知的敏感信息泄漏检测" }
func (r *TokenLeakRule) Severity() findings.Severity { return findings.SeverityHigh }
func (r *TokenLeakRule) Category() findings.Category { return findings.CategorySensitiveLeak }

func (r *TokenLeakRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	var result []findings.Finding

	for _, line := range fd.AddedLines() {
		content := line.Content
		if content == "" {
			continue
		}

		trimmed := strings.TrimSpace(content)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		analysis := r.analyzer.AnalyzeLine(content, line.NewLine)
		strs := analysis.FindStringLiterals()

		for _, s := range strs {
			clean := strings.Trim(s, "\"'`")
			if len(clean) < 10 {
				continue
			}

			if match, title, confidence := detectLeakPattern(clean); match {
				f := findings.NewFinding(
					r.Severity(), r.Category(), r.ID(),
					title,
					fd.NewPath, line.NewLine,
					sanitizeTokenEvidence(content, clean),
					"立即轮换密钥，从代码中移除，使用密钥管理服务",
					confidence,
					"token:sensitive_leak",
				)
				result = append(result, *f)
				break // 一行只报一次
			}
		}
	}

	return result, nil
}

// detectLeakPattern 检测字符串是否匹配已知的泄漏模式。
func detectLeakPattern(s string) (bool, string, float64) {
	// AWS Access Key
	if len(s) >= 20 && strings.HasPrefix(s, "AKIA") && isUpperAlphanumeric(s[4:]) {
		return true, "Token 感知：AWS Access Key 泄漏", 0.95
	}
	// GitHub Token
	if strings.HasPrefix(s, "ghp_") && len(s) >= 40 {
		return true, "Token 感知：GitHub Token 泄漏", 0.95
	}
	if strings.HasPrefix(s, "gho_") && len(s) >= 40 {
		return true, "Token 感知：GitHub OAuth Token 泄漏", 0.95
	}
	// Stripe Key
	if (strings.HasPrefix(s, "sk_live_") || strings.HasPrefix(s, "pk_live_")) && len(s) >= 20 {
		return true, "Token 感知：Stripe 密钥泄漏", 0.95
	}
	// Slack Token
	if strings.HasPrefix(s, "xox") && len(s) >= 15 {
		return true, "Token 感知：Slack Token 泄漏", 0.90
	}
	// Private Key
	if strings.Contains(s, "BEGIN") && strings.Contains(s, "PRIVATE KEY") {
		return true, "Token 感知：私钥泄漏", 0.99
	}
	// JWT Token
	if strings.HasPrefix(s, "eyJ") && len(s) >= 20 {
		parts := strings.Split(s, ".")
		if len(parts) == 3 {
			return true, "Token 感知：JWT Token 泄漏", 0.85
		}
	}
	// Database connection string with password
	lower := strings.ToLower(s)
	dbPrefixes := []string{"mysql://", "postgres://", "postgresql://", "mongodb://", "mongodb+srv://", "redis://"}
	for _, prefix := range dbPrefixes {
		if strings.HasPrefix(lower, prefix) && strings.Contains(s, "@") && strings.Contains(s, ":") {
			return true, "Token 感知：数据库连接串泄漏（含密码）", 0.95
		}
	}
	// URL with credentials
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		atIdx := strings.Index(s, "@")
		colonIdx := strings.Index(s, ":")
		if atIdx > 0 && colonIdx > 0 && colonIdx < atIdx {
			// http://user:pass@host
			return true, "Token 感知：URL 中嵌入了凭据", 0.90
		}
	}

	return false, "", 0
}

// isUpperAlphanumeric 检查字符串是否全是大写字母和数字。
func isUpperAlphanumeric(s string) bool {
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// sanitizeTokenEvidence 脱敏证据中的敏感信息。
func sanitizeTokenEvidence(content, secret string) string {
	if len(secret) <= 8 {
		return content
	}
	masked := secret[:4] + "***REDACTED***"
	return strings.Replace(content, secret, masked, 1)
}

// ========== GOR-AST-001: Token 感知的 goroutine 泄漏检测 ==========

// TokenGoroutineRule 使用词法分析检测 goroutine 泄漏。
type TokenGoroutineRule struct {
	analyzer *analyzer.TokenAnalyzer
}

// NewTokenGoroutineRule 创建 Token 感知的 goroutine 泄漏检测规则实例。
func NewTokenGoroutineRule() *TokenGoroutineRule {
	return &TokenGoroutineRule{analyzer: analyzer.NewTokenAnalyzer()}
}

func (r *TokenGoroutineRule) ID() string                  { return "GOR-AST-001" }
func (r *TokenGoroutineRule) Name() string                { return "Token 感知的 goroutine 泄漏检测" }
func (r *TokenGoroutineRule) Severity() findings.Severity { return findings.SeverityHigh }
func (r *TokenGoroutineRule) Category() findings.Category { return findings.CategoryResource }

func (r *TokenGoroutineRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	var result []findings.Finding

	for _, hunk := range fd.Hunks {
		var allLines []string
		for _, line := range hunk.Lines {
			allLines = append(allLines, line.Content)
		}
		lineAnalyses := r.analyzer.AnalyzeLines(allLines)

		for i, line := range hunk.Lines {
			if line.Type != diff.LineAdded {
				continue
			}
			if i >= len(lineAnalyses) {
				continue
			}

			analysis := lineAnalyses[i]
			if !analysis.HasGoStatement() {
				continue
			}

			if hasExitMechanismToken(lineAnalyses) {
				continue
			}

			if isOneShotGoroutineToken(lineAnalyses, i) {
				continue
			}

			f := findings.NewFinding(
				r.Severity(), r.Category(), r.ID(),
				"Token 感知：goroutine 可能泄漏",
				fd.NewPath, line.NewLine,
				line.Content,
				"为 goroutine 添加退出机制：context.WithCancel + select",
				0.85,
				"token:goroutine_leak",
			)
			result = append(result, *f)
		}
	}

	return result, nil
}

func hasExitMechanismToken(analyses []analyzer.TokenAnalysis) bool {
	exitIdentifiers := map[string]bool{
		"ctx": true, "cancel": true, "done": true,
		"stop": true, "quit": true, "errgroup": true,
	}

	for _, a := range analyses {
		for _, f := range a.Facts {
			if f.Kind == analyzer.FactSelect {
				return true
			}
		}
		for _, id := range a.FindIdentifiers() {
			if exitIdentifiers[strings.ToLower(id)] {
				return true
			}
		}
		if a.HasDefer() {
			return true
		}
	}
	return false
}

func isOneShotGoroutineToken(analyses []analyzer.TokenAnalysis, goIndex int) bool {
	for i := goIndex + 1; i < len(analyses); i++ {
		for _, f := range analyses[i].Facts {
			if f.Kind == analyzer.FactFor {
				return false
			}
		}
	}
	return true
}

// ========== RES-AST-001: Token 感知的资源泄漏检测 ==========

// TokenResourceRule 使用词法分析检测资源泄漏。
type TokenResourceRule struct {
	analyzer *analyzer.TokenAnalyzer
}

// NewTokenResourceRule 创建 Token 感知的资源泄漏检测规则实例。
func NewTokenResourceRule() *TokenResourceRule {
	return &TokenResourceRule{analyzer: analyzer.NewTokenAnalyzer()}
}

func (r *TokenResourceRule) ID() string                  { return "RES-AST-001" }
func (r *TokenResourceRule) Name() string                { return "Token 感知的资源泄漏检测" }
func (r *TokenResourceRule) Severity() findings.Severity { return findings.SeverityMedium }
func (r *TokenResourceRule) Category() findings.Category { return findings.CategoryResource }

var resourceOpenCalls = map[string]string{
	"os.Open(":       ".Close()",
	"os.Create(":     ".Close()",
	"os.OpenFile(":   ".Close()",
	"os.CreateTemp(": ".Close()",
	"http.Get(":      ".Body.Close()",
	"http.Post(":     ".Body.Close()",
	"http.Do(":       ".Body.Close()",
	"sql.Open(":      ".Close()",
	".Conn(":         ".Close()",
	".Query(":        ".Close()",
	".QueryRow(":     ".Close()",
	".Prepare(":      ".Close()",
	"net.Dial(":      ".Close()",
	"net.Listen(":    ".Close()",
}

func (r *TokenResourceRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	var result []findings.Finding

	for _, hunk := range fd.Hunks {
		var addedLines []string
		var addedLineNums []int
		for _, line := range hunk.Lines {
			if line.Type == diff.LineAdded && line.Content != "" {
				addedLines = append(addedLines, line.Content)
				addedLineNums = append(addedLineNums, line.NewLine)
			}
		}

		if len(addedLines) == 0 {
			continue
		}

		var allLines []string
		for _, line := range hunk.Lines {
			allLines = append(allLines, line.Content)
		}

		for i, content := range addedLines {
			for call, closeMethod := range resourceOpenCalls {
				if strings.Contains(content, call) {
					if !hasCloseInLines(allLines, closeMethod) {
						f := findings.NewFinding(
							r.Severity(), r.Category(), r.ID(),
							"Token 感知：资源可能未关闭",
							fd.NewPath, addedLineNums[i],
							content,
							"添加 defer "+extractVarNameToken(content)+closeMethod,
							0.80,
							"token:resource_leak",
						)
						result = append(result, *f)
					}
					break
				}
			}
		}
	}

	return result, nil
}

func hasCloseInLines(lines []string, closeMethod string) bool {
	for _, line := range lines {
		if strings.Contains(line, "defer") && strings.Contains(line, closeMethod) {
			return true
		}
		if strings.Contains(line, closeMethod) && strings.Contains(line, "Close") {
			return true
		}
	}
	return false
}

func extractVarNameToken(line string) string {
	ta := analyzer.NewTokenAnalyzer()
	analysis := ta.AnalyzeLine(line, 1)
	ids := analysis.FindIdentifiers()
	if len(ids) > 0 {
		return ids[0] + "."
	}
	return ""
}

// ========== ERR-AST-001: Token 感知的错误处理检测 ==========

// TokenErrorRule 使用词法分析检测错误处理问题。
//
// 检测策略（基于 token facts）：
//  1. 用 _ 忽略错误：找到 DEFINE/ASSIGN + IDENT "_"
//  2. panic 使用：找到关键字 panic
//  3. log.Fatal 使用：找到标识符 log + 点 + Fatal
//  4. 错误被吞没：if err != nil 块中只有 return nil
type TokenErrorRule struct {
	analyzer *analyzer.TokenAnalyzer
}

// NewTokenErrorRule 创建 Token 感知的错误处理检测规则实例。
func NewTokenErrorRule() *TokenErrorRule {
	return &TokenErrorRule{analyzer: analyzer.NewTokenAnalyzer()}
}

func (r *TokenErrorRule) ID() string                  { return "ERR-AST-001" }
func (r *TokenErrorRule) Name() string                { return "Token 感知的错误处理检测" }
func (r *TokenErrorRule) Severity() findings.Severity { return findings.SeverityMedium }
func (r *TokenErrorRule) Category() findings.Category { return findings.CategoryErrorHandling }

func (r *TokenErrorRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	var result []findings.Finding

	for _, hunk := range fd.Hunks {
		// 跨行检查：错误被吞没
		if finding := checkSwallowedErrorToken(fd.NewPath, hunk); finding != nil {
			result = append(result, *finding)
		}

		for _, line := range hunk.Lines {
			if line.Type != diff.LineAdded {
				continue
			}
			content := line.Content
			if content == "" {
				continue
			}

			trimmed := strings.TrimSpace(content)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}

			analysis := r.analyzer.AnalyzeLine(content, line.NewLine)

			// 检查 1：用 _ 忽略错误
			if isIgnoredErrorToken(analysis, content) {
				f := findings.NewFinding(
					r.Severity(), r.Category(), r.ID(),
					"Token 感知：错误可能被忽略（使用 _ 丢弃）",
					fd.NewPath, line.NewLine,
					content,
					"检查错误返回值并处理：if err != nil { return fmt.Errorf(\"context: %w\", err) }",
					0.80,
					"token:error_ignored",
				)
				result = append(result, *f)
				continue
			}

			// 检查 2：panic 使用
			if isPanicUsage(analysis) && !isTestFile(fd.NewPath) {
				f := findings.NewFinding(
					r.Severity(), r.Category(), r.ID(),
					"Token 感知：使用 panic 代替返回 error",
					fd.NewPath, line.NewLine,
					content,
					"库代码应返回 error 而不是 panic",
					0.85,
					"token:panic_usage",
				)
				result = append(result, *f)
				continue
			}

			// 检查 3：log.Fatal 使用
			if isLogFatalUsage(analysis, content) && !isMainOrTestFile(fd.NewPath) {
				f := findings.NewFinding(
					r.Severity(), r.Category(), r.ID(),
					"Token 感知：库代码使用 log.Fatal（会直接退出进程）",
					fd.NewPath, line.NewLine,
					content,
					"库代码应返回 error 而不是调用 log.Fatal",
					0.80,
					"token:log_fatal",
				)
				result = append(result, *f)
				continue
			}
		}
	}

	return result, nil
}

// isIgnoredErrorToken 检查是否用 _ 忽略了错误返回值。
func isIgnoredErrorToken(analysis analyzer.TokenAnalysis, content string) bool {
	// 检查是否有 _ 标识符
	hasUnderscore := false
	hasAssignment := false

	for _, f := range analysis.Facts {
		if f.Kind == analyzer.FactIdentifier && f.Value == "_" {
			hasUnderscore = true
		}
		if f.Kind == analyzer.FactAssignment {
			hasAssignment = true
		}
	}

	if !hasUnderscore || !hasAssignment {
		return false
	}

	// 排除安全的忽略
	safeIgnores := []string{
		"fmt.Print", "fmt.Fprint", "io.Copy", "io.WriteString",
		".Write(", ".Close()", "w.Write", "f.Write",
	}
	for _, safe := range safeIgnores {
		if strings.Contains(content, safe) {
			return false
		}
	}

	return true
}

// isPanicUsage 检查是否使用了 panic。
func isPanicUsage(analysis analyzer.TokenAnalysis) bool {
	for _, f := range analysis.Facts {
		if f.Kind == analyzer.FactIdentifier && f.Value == "panic" {
			return true
		}
	}
	return false
}

// isLogFatalUsage 检查是否使用了 log.Fatal。
func isLogFatalUsage(analysis analyzer.TokenAnalysis, content string) bool {
	ids := analysis.FindIdentifiers()
	hasLog := false
	hasFatal := false
	for _, id := range ids {
		if id == "log" {
			hasLog = true
		}
		if strings.HasPrefix(id, "Fatal") {
			hasFatal = true
		}
	}
	return hasLog && hasFatal && strings.Contains(content, ".Fatal")
}

// checkSwallowedErrorToken 跨行检查错误被吞没。
func checkSwallowedErrorToken(filePath string, hunk diff.Hunk) *findings.Finding {
	ta := analyzer.NewTokenAnalyzer()

	for i, line := range hunk.Lines {
		if line.Type != diff.LineAdded {
			continue
		}
		if !strings.Contains(line.Content, "if err != nil") {
			continue
		}

		// 检查接下来几行
		for j := i + 1; j < len(hunk.Lines) && j < i+5; j++ {
			nextLine := hunk.Lines[j]
			trimmed := strings.TrimSpace(nextLine.Content)

			if trimmed == "return nil" || trimmed == "return nil," {
				// 确认是吞没：用 token 分析验证
				nextAnalysis := ta.AnalyzeLine(trimmed, j)
				hasReturn := false
				hasNil := false
				for _, f := range nextAnalysis.Facts {
					if f.Kind == analyzer.FactReturn {
						hasReturn = true
					}
					if f.Kind == analyzer.FactIdentifier && f.Value == "nil" {
						hasNil = true
					}
				}
				if hasReturn && hasNil {
					return findings.NewFinding(
						findings.SeverityLow, findings.CategoryErrorHandling, "ERR-AST-001",
						"Token 感知：错误被吞没（返回 nil 而非 err）",
						filePath, line.NewLine,
						line.Content,
						"应返回 err 或使用 fmt.Errorf 添加上下文",
						0.75,
						"token:error_swallowed",
					)
				}
			}

			if strings.HasPrefix(trimmed, "return") {
				break
			}
		}
	}
	return nil
}

// ========== TST-AST-001: Token 感知的测试缺失检测 ==========

// TokenMissingTestRule 使用词法分析检测新增导出函数是否有测试。
//
// 检测策略：
//  1. 用 go/scanner 找到 func 关键字后的大写标识符（导出函数）
//  2. 在同文件和其他测试文件中查找对应的 Test 函数
//  3. 检查测试函数体是否有效（非空、有调用）
type TokenMissingTestRule struct {
	analyzer *analyzer.TokenAnalyzer
}

// NewTokenMissingTestRule 创建 Token 感知的测试缺失检测规则实例。
func NewTokenMissingTestRule() *TokenMissingTestRule {
	return &TokenMissingTestRule{analyzer: analyzer.NewTokenAnalyzer()}
}

func (r *TokenMissingTestRule) ID() string                  { return "TST-AST-001" }
func (r *TokenMissingTestRule) Name() string                { return "Token 感知的测试缺失检测" }
func (r *TokenMissingTestRule) Severity() findings.Severity { return findings.SeverityLow }
func (r *TokenMissingTestRule) Category() findings.Category { return findings.CategoryTesting }

var noTestNeededFuncs = map[string]bool{
	"main": true, "init": true, "String": true, "Error": true,
	"Unwrap": true, "Is": true, "As": true, "ServeHTTP": true,
	"Close": true, "Setup": true, "Teardown": true,
}

func (r *TokenMissingTestRule) Check(fd diff.FileDiff) ([]findings.Finding, error) {
	if strings.HasSuffix(fd.NewPath, "_test.go") || !fd.IsGoFile() {
		return nil, nil
	}

	var result []findings.Finding

	// 收集同文件所有行（用于检查测试函数是否存在）
	var allContent []string
	for _, hunk := range fd.Hunks {
		for _, line := range hunk.Lines {
			allContent = append(allContent, line.Content)
		}
	}

	for _, hunk := range fd.Hunks {
		for _, line := range hunk.Lines {
			if line.Type != diff.LineAdded {
				continue
			}

			funcName := extractExportedFuncName(line.Content)
			if funcName == "" {
				continue
			}
			if noTestNeededFuncs[funcName] {
				continue
			}

			// 检查是否有对应的测试
			if hasTestFunction(allContent, funcName) {
				continue
			}

			f := findings.NewFinding(
				r.Severity(), r.Category(), r.ID(),
				"Token 感知：新增导出函数缺少测试",
				fd.NewPath, line.NewLine,
				line.Content,
				"添加测试函数 Test"+funcName+" 覆盖该函数的正常和异常路径",
				0.65,
				"token:missing_test",
			)
			result = append(result, *f)
		}
	}

	return result, nil
}

// CheckFiles 对多个文件执行测试缺失检测。
func (r *TokenMissingTestRule) CheckFiles(files []diff.FileDiff) ([]findings.Finding, error) {
	// 收集所有测试文件内容
	var testContent []string
	for _, fd := range files {
		if strings.HasSuffix(fd.NewPath, "_test.go") {
			for _, hunk := range fd.Hunks {
				for _, line := range hunk.Lines {
					testContent = append(testContent, line.Content)
				}
			}
		}
	}

	var result []findings.Finding
	for _, fd := range files {
		if strings.HasSuffix(fd.NewPath, "_test.go") || !fd.IsGoFile() {
			continue
		}

		var fileContent []string
		for _, hunk := range fd.Hunks {
			for _, line := range hunk.Lines {
				fileContent = append(fileContent, line.Content)
			}
		}

		for _, hunk := range fd.Hunks {
			for _, line := range hunk.Lines {
				if line.Type != diff.LineAdded {
					continue
				}

				funcName := extractExportedFuncName(line.Content)
				if funcName == "" {
					continue
				}
				if noTestNeededFuncs[funcName] {
					continue
				}

				if hasTestFunction(fileContent, funcName) {
					continue
				}
				if hasTestFunction(testContent, funcName) {
					continue
				}

				f := findings.NewFinding(
					r.Severity(), r.Category(), r.ID(),
					"Token 感知：新增导出函数缺少测试",
					fd.NewPath, line.NewLine,
					line.Content,
					"添加测试函数 Test"+funcName+" 覆盖该函数的正常和异常路径",
					0.65,
					"token:missing_test",
				)
				result = append(result, *f)
			}
		}
	}

	return result, nil
}

// extractExportedFuncName 用 go/scanner 从行中提取导出函数名。
func extractExportedFuncName(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "func ") {
		return ""
	}

	// 用 scanner 分析
	src := "package _\n" + line
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	s.Init(file, []byte(src), nil, 0)

	var prevTok token.Token
	var prevLit string
	inTargetLine := false

	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		position := fset.Position(pos)
		if position.Line >= 2 {
			inTargetLine = true
		}
		if !inTargetLine {
			prevTok = tok
			prevLit = lit
			continue
		}

		// func 后面的标识符就是函数名
		if prevTok == token.FUNC && tok == token.IDENT {
			// 检查是否是导出函数（首字母大写）
			if len(lit) > 0 && lit[0] >= 'A' && lit[0] <= 'Z' {
				return lit
			}
		}

		prevTok = tok
		prevLit = lit
	}
	_ = prevLit
	return ""
}

// hasTestFunction 检查代码中是否有对指定函数的测试。
func hasTestFunction(content []string, funcName string) bool {
	testFuncName := "Test" + funcName

	for _, line := range content {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}

		// 精确匹配：func TestGreet(
		if strings.Contains(line, "func "+testFuncName+"(") {
			// 检查函数体是否有效（非空、有调用）
			if hasEffectiveTestBody(content, funcName) {
				return true
			}
		}
		// 前缀匹配：func TestGreet_
		if strings.Contains(line, "func "+testFuncName+"_") {
			return true
		}
		// 调用匹配：Greet( 在测试函数体内
		if containsFunctionCallToken(line, funcName) {
			return true
		}
	}
	return false
}

// hasEffectiveTestBody 检查测试函数体是否有效。
func hasEffectiveTestBody(content []string, targetFuncName string) bool {
	hasExecutableCode := false
	allSkip := true

	for _, line := range content {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if trimmed == "}" || trimmed == "{" || trimmed == "} {" || trimmed == "}{" {
			continue
		}

		hasExecutableCode = true
		if !strings.Contains(trimmed, "t.Skip") {
			allSkip = false
		}
	}

	if !hasExecutableCode || allSkip {
		return false
	}

	// 检查是否调用了目标函数
	for _, line := range content {
		if containsFunctionCallToken(line, targetFuncName) {
			return true
		}
	}

	return false
}

// containsFunctionCallToken 检查一行是否包含函数调用。
func containsFunctionCallToken(line, funcName string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "//") {
		return false
	}

	idx := 0
	for {
		pos := strings.Index(line[idx:], funcName)
		if pos < 0 {
			return false
		}
		afterName := idx + pos + len(funcName)
		if afterName < len(line) && line[afterName] == '(' {
			return true
		}
		idx = afterName
	}
}

// ========== 辅助函数 ==========

func isSensitiveIdent(ident string) bool {
	lower := strings.ToLower(ident)
	sensitive := []string{
		"password", "passwd", "pwd",
		"secret", "secretkey", "secret_key",
		"apikey", "api_key", "api-key",
		"token", "accesstoken", "access_token",
		"private_key", "privatekey",
		"credential", "dsn",
		"signing_key", "encryption_key", "master_key",
	}
	for _, s := range sensitive {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func isLikelyNotSecret(value string) bool {
	value = strings.Trim(value, "\"'`")
	if len(value) < 8 {
		return true
	}
	if strings.TrimSpace(value) == "" {
		return true
	}
	lower := strings.ToLower(value)
	placeholders := []string{
		"your-", "your_", "replace", "changeme", "example",
		"placeholder", "todo", "fixme", "xxx", "dummy",
		"test", "mock", "fake", "sample", "default",
	}
	for _, p := range placeholders {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

func isMainOrTestFile(path string) bool {
	return isTestFile(path) || path == "main.go" || strings.HasSuffix(path, "/main.go") || strings.Contains(path, "cmd/")
}
