//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	ruleSecretHardcoded       = "secret.hardcoded"
	ruleShellCommandInjection = "security.shell_command_injection"
	ruleInsecureTLS           = "security.insecure_tls"
	ruleGoroutineContextLeak  = "concurrency.goroutine_context_leak"
	ruleUnclosedFile          = "resource.unclosed_file"
	ruleUnclosedHTTPBody      = "resource.unclosed_http_body"
	ruleUnclosedSQLRows       = "resource.unclosed_sql_rows"
	ruleIgnoredReturn         = "error.ignored_return"
	ruleDatabaseTxLifecycle   = "database.tx_lifecycle"
	ruleDatabaseOpenLifecycle = "database.sql_open_lifecycle"
	ruleMissingTests          = "tests.missing_tests"

	confidenceStrong    = 0.90
	confidenceLifecycle = 0.85
	confidenceWarning   = 0.70
	confidenceBoundary  = 0.65
)

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|passwd|private[_-]?key)\s*[:=]\s*["'][^"']{8,}["']`)
	awsAccessKeyPattern     = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	githubTokenPattern      = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`)
	openAITokenPattern      = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)
	bearerTokenPattern      = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}\b`)
	insecureTLSPattern      = regexp.MustCompile(`\bInsecureSkipVerify\s*(?::|=)\s*true\b`)

	fileOpenPattern   = regexp.MustCompile(`^\s*(\w+)\s*(?:,\s*\w+)?\s*:=\s*os\.(Open|Create|OpenFile)\(`)
	httpGetPattern    = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*http\.(Get|Head|Post|PostForm)\(`)
	sqlRowsPattern    = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*[^=]*\.(Query|QueryContext)\(`)
	sqlTxPattern      = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*[^=]*\.(Begin|BeginTx)\(`)
	sqlOpenPattern    = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*sql\.Open\(`)
	exportedFuncRegex = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Z][A-Za-z0-9_]*)\s*\(`)
)

type ruleMatch struct {
	Severity       string
	Category       string
	File           string
	Line           int
	Title          string
	Evidence       string
	Recommendation string
	Confidence     float64
	Source         string
	RuleID         string
}

func runRules(parsed parsedDiff, repoRoot string) []ruleMatch {
	var matches []ruleMatch
	candidates := parsed.candidateLines()
	for _, candidate := range candidates {
		file := parsed.Files[candidate.FileIndex]
		hunk := file.Hunks[candidate.HunkIndex]
		matches = append(matches, runCandidateRules(candidate, file, hunk, repoRoot)...)
	}

	matches = append(matches, runMissingTestsRule(parsed)...)
	return matches
}

func runCandidateRules(candidate candidateLine, file changedFile, hunk diffHunk, repoRoot string) []ruleMatch {
	trimmed := strings.TrimSpace(candidate.Text)
	var matches []ruleMatch
	matches = append(matches, securityRuleMatches(candidate, trimmed)...)
	matches = append(matches, concurrencyRuleMatches(candidate, file, hunk, trimmed)...)
	matches = append(matches, resourceRuleMatches(candidate, file, repoRoot, trimmed)...)
	matches = append(matches, errorRuleMatches(candidate, trimmed)...)
	matches = append(matches, databaseRuleMatches(candidate, file, repoRoot, trimmed)...)
	return matches
}

func securityRuleMatches(candidate candidateLine, line string) []ruleMatch {
	var matches []ruleMatch
	if isHardcodedSecret(line) {
		matches = append(matches, newRuleMatch(candidate, ruleSecretHardcoded, "high", "security",
			"Hardcoded secret-like value",
			"Move secrets to a managed secret store or environment-provided configuration.",
			confidenceStrong))
	}
	if isShellCommandInjection(line) {
		matches = append(matches, newRuleMatch(candidate, ruleShellCommandInjection, "high", "security",
			"Shell command uses an interpolated command string",
			"Use exec.Command with a fixed executable and argument array instead of sh -c.",
			confidenceStrong))
	}
	if insecureTLSPattern.MatchString(line) {
		matches = append(matches, newRuleMatch(candidate, ruleInsecureTLS, "high", "security",
			"TLS certificate verification is disabled",
			"Keep certificate verification enabled or pin a trusted CA explicitly.",
			confidenceStrong))
	}
	return matches
}

func concurrencyRuleMatches(candidate candidateLine, file changedFile, hunk diffHunk, line string) []ruleMatch {
	if !file.isGoFile() || !isLikelyGoroutineContextLeak(line, hunk) {
		return nil
	}
	return []ruleMatch{newRuleMatch(candidate, ruleGoroutineContextLeak, "medium", "concurrency",
		"Goroutine is not tied to request cancellation",
		"Pass context into the goroutine and exit when the context is cancelled.",
		confidenceLifecycle)}
}

func resourceRuleMatches(candidate candidateLine, file changedFile, repoRoot string, line string) []ruleMatch {
	var matches []ruleMatch
	if variable := firstCapture(fileOpenPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}}
		if confidence, ok := resourceLeakConfidence(file, candidate, repoRoot, matcher); ok {
			matches = append(matches, newRuleMatch(candidate, ruleUnclosedFile, "medium", "resource",
				"Opened file is not closed",
				"Close the file with defer after checking the open error.",
				confidence))
		}
	}
	if variable := firstCapture(httpGetPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}, body: true}
		if confidence, ok := resourceLeakConfidence(file, candidate, repoRoot, matcher); ok {
			matches = append(matches, newRuleMatch(candidate, ruleUnclosedHTTPBody, "medium", "resource",
				"HTTP response body is not closed",
				"Close response bodies with defer resp.Body.Close() after checking errors.",
				confidence))
		}
	}
	if variable := firstCapture(sqlRowsPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}}
		if confidence, ok := resourceLeakConfidence(file, candidate, repoRoot, matcher); ok {
			matches = append(matches, newRuleMatch(candidate, ruleUnclosedSQLRows, "medium", "resource",
				"SQL rows are not closed",
				"Close rows with defer rows.Close() and check rows.Err().",
				confidence))
		}
	}
	return matches
}

func errorRuleMatches(candidate candidateLine, line string) []ruleMatch {
	if !isExplicitIgnoredError(line) {
		return nil
	}
	return []ruleMatch{newRuleMatch(candidate, ruleIgnoredReturn, "medium", "error-handling",
		"Error return value is ignored",
		"Check and handle the returned error instead of assigning it to a blank identifier.",
		confidenceStrong)}
}

func databaseRuleMatches(candidate candidateLine, file changedFile, repoRoot string, line string) []ruleMatch {
	var matches []ruleMatch
	if variable := firstCapture(sqlTxPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Commit": true, "Rollback": true}}
		if confidence, ok := resourceLeakConfidence(file, candidate, repoRoot, matcher); ok {
			matches = append(matches, newRuleMatch(candidate, ruleDatabaseTxLifecycle, "high", "database",
				"Database transaction is opened without commit or rollback",
				"Ensure every transaction path commits or rolls back.",
				confidence))
		}
	}
	if variable := firstCapture(sqlOpenPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}}
		if confidence, ok := resourceLeakConfidence(file, candidate, repoRoot, matcher); ok {
			matches = append(matches, newRuleMatch(candidate, ruleDatabaseOpenLifecycle, "medium", "database",
				"Database handle is opened without a close path",
				"Close database handles owned by this function or document shared ownership.",
				confidence))
		}
	}
	return matches
}

func newRuleMatch(
	candidate candidateLine,
	ruleID string,
	severity string,
	category string,
	title string,
	recommendation string,
	confidence float64,
) ruleMatch {
	return ruleMatch{
		Severity:       severity,
		Category:       category,
		File:           candidate.File,
		Line:           candidate.Line,
		Title:          title,
		Evidence:       strings.TrimSpace(candidate.Text),
		Recommendation: recommendation,
		Confidence:     confidence,
		Source:         "diff",
		RuleID:         ruleID,
	}
}

func isHardcodedSecret(line string) bool {
	return secretAssignmentPattern.MatchString(line) ||
		awsAccessKeyPattern.MatchString(line) ||
		githubTokenPattern.MatchString(line) ||
		openAITokenPattern.MatchString(line) ||
		bearerTokenPattern.MatchString(line) ||
		strings.Contains(line, "-----BEGIN PRIVATE KEY-----")
}

func isShellCommandInjection(line string) bool {
	parsed, ok := parseShellCommandLine(line)
	if ok {
		return parsed
	}
	return isShellCommandInjectionFallback(line)
}

func parseShellCommandLine(line string) (bool, bool) {
	source := "package review\nfunc check() {\n" + line + "\n}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "review.go", source, 0)
	if err != nil {
		return false, false
	}
	foundShellCommand := false
	foundInjection := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		payload, ok := shellCommandPayload(call)
		if !ok {
			return true
		}
		foundShellCommand = true
		if _, ok := payload.(*ast.BasicLit); !ok {
			foundInjection = true
		}
		return !foundInjection
	})
	return foundInjection, foundShellCommand
}

func shellCommandPayload(call *ast.CallExpr) (ast.Expr, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "exec" {
		return nil, false
	}
	commandIndex := 0
	switch selector.Sel.Name {
	case "Command":
		commandIndex = 0
	case "CommandContext":
		commandIndex = 1
	default:
		return nil, false
	}
	if len(call.Args) <= commandIndex+2 || !isStringLiteral(call.Args[commandIndex], "sh", "bash") ||
		!isStringLiteral(call.Args[commandIndex+1], "-c") {
		return nil, false
	}
	return call.Args[commandIndex+2], true
}

func isStringLiteral(expr ast.Expr, values ...string) bool {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return false
	}
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func isShellCommandInjectionFallback(line string) bool {
	compact := strings.ReplaceAll(line, " ", "")
	compact = strings.ReplaceAll(compact, "\t", "")
	for _, marker := range []string{
		`("sh","-c",`,
		`("bash","-c",`,
		`,"sh","-c",`,
		`,"bash","-c",`,
	} {
		index := strings.Index(compact, marker)
		if index < 0 {
			continue
		}
		payload := compact[index+len(marker):]
		if end, ok := leadingStringLiteralEnd(payload); ok {
			return strings.HasPrefix(payload[end:], "+")
		}
		return true
	}
	return false
}

func leadingStringLiteralEnd(value string) (int, bool) {
	if value == "" || value[0] != '"' && value[0] != '`' {
		return 0, false
	}
	quote := value[0]
	escaped := false
	for i := 1; i < len(value); i++ {
		if quote == '`' {
			if value[i] == '`' {
				return i + 1, true
			}
			continue
		}
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == '"':
			return i + 1, true
		}
	}
	return 0, false
}

func isLikelyGoroutineContextLeak(line string, hunk diffHunk) bool {
	if !strings.HasPrefix(line, "go ") {
		return false
	}
	combined := hunkText(hunk)
	if strings.Contains(combined, "context.Background()") ||
		strings.Contains(combined, "context.TODO()") {
		return true
	}
	return !strings.Contains(combined, "ctx") && !strings.Contains(combined, "context.Context")
}

func firstCapture(pattern *regexp.Regexp, line string) string {
	matches := pattern.FindStringSubmatch(line)
	if len(matches) < 2 || matches[1] == "_" {
		return ""
	}
	return matches[1]
}

type cleanupMatcher struct {
	variable string
	methods  map[string]bool
	body     bool
}

func resourceLeakConfidence(
	file changedFile,
	candidate candidateLine,
	repoRoot string,
	matcher cleanupMatcher,
) (float64, bool) {
	if repoRoot != "" &&
		repoFunctionContainsCleanup(repoRoot, file.reviewPath(), candidate, matcher) {
		return 0, false
	}
	if repoRoot == "" && hunkFunctionWindowContainsCleanup(file, candidate, matcher) {
		return 0, false
	}
	if len(file.Hunks) > 1 {
		return confidenceBoundary, true
	}
	return confidenceLifecycle, true
}

func repoFunctionContainsCleanup(
	repoRoot string,
	filePath string,
	candidate candidateLine,
	matcher cleanupMatcher,
) bool {
	if filePath == "" || matcher.variable == "" || len(matcher.methods) == 0 {
		return false
	}
	fset := token.NewFileSet()
	parsedFile, err := parser.ParseFile(
		fset,
		filepath.Join(repoRoot, filepath.FromSlash(filePath)),
		nil,
		0,
	)
	if err != nil {
		return false
	}
	fn := enclosingFunctionForLine(fset, parsedFile, candidate.Line)
	if fn == nil || fn.Body == nil {
		return false
	}
	return functionContainsBoundCleanup(
		fset,
		parsedFile,
		fn,
		candidate.Line,
		matcher,
	)
}

func functionContainsBoundCleanup(
	fset *token.FileSet,
	parsedFile *ast.File,
	fn *ast.FuncDecl,
	candidateLine int,
	matcher cleanupMatcher,
) bool {
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object),
	}
	config := &types.Config{
		Importer: importer.Default(),
		Error:    func(error) {},
	}
	_, _ = config.Check(parsedFile.Name.Name, fset, []*ast.File{parsedFile}, info)

	candidateObject := candidateBindingObject(
		fn.Body,
		fset,
		info,
		matcher.variable,
		candidateLine,
	)
	if candidateObject == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		receiver := matcher.callReceiver(call.Fun)
		if receiver == nil || info.Uses[receiver] != candidateObject {
			return true
		}
		cleanupLine := fset.Position(call.Pos()).Line
		if cleanupLine <= candidateLine {
			return true
		}
		found = true
		return false
	})
	return found
}

func candidateBindingObject(
	body *ast.BlockStmt,
	fset *token.FileSet,
	info *types.Info,
	variable string,
	line int,
) types.Object {
	var object types.Object
	ast.Inspect(body, func(node ast.Node) bool {
		if object != nil {
			return false
		}
		assign, ok := node.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || fset.Position(assign.Pos()).Line != line {
			return true
		}
		for _, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name != variable {
				continue
			}
			object = info.Defs[ident]
			if object == nil {
				object = info.Uses[ident]
			}
			break
		}
		return object == nil
	})
	return object
}

func enclosingFunctionForLine(
	fset *token.FileSet,
	file *ast.File,
	line int,
) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		if start <= line && line <= end {
			return fn
		}
	}
	return nil
}

func (m cleanupMatcher) callReceiver(expr ast.Expr) *ast.Ident {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || !m.methods[selector.Sel.Name] {
		return nil
	}
	if m.body {
		inner, ok := selector.X.(*ast.SelectorExpr)
		if !ok || inner.Sel.Name != "Body" {
			return nil
		}
		ident, ok := inner.X.(*ast.Ident)
		if !ok || ident.Name != m.variable {
			return nil
		}
		return ident
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok || ident.Name != m.variable {
		return nil
	}
	return ident
}

func hunkFunctionWindowContainsCleanup(
	file changedFile,
	candidate candidateLine,
	matcher cleanupMatcher,
) bool {
	if candidate.HunkIndex < 0 || candidate.HunkIndex >= len(file.Hunks) {
		return false
	}
	hunk := file.Hunks[candidate.HunkIndex]
	if candidate.HunkLineIndex < 0 || candidate.HunkLineIndex >= len(hunk.Lines) {
		return false
	}
	start := -1
	for i := candidate.HunkLineIndex; i >= 0; i-- {
		if isFunctionStartLine(hunk.Lines[i]) {
			start = i
			break
		}
	}
	if start < 0 {
		return false
	}
	end := len(hunk.Lines)
	for i := candidate.HunkLineIndex + 1; i < len(hunk.Lines); i++ {
		if isFunctionStartLine(hunk.Lines[i]) {
			end = i
			break
		}
	}
	var source strings.Builder
	source.WriteString("package review\n")
	sourceLine := 1
	candidateSourceLine := 0
	for i := start; i < end; i++ {
		line := hunk.Lines[i]
		if line.Kind != diffLineAdded && line.Kind != diffLineContext {
			continue
		}
		sourceLine++
		if i == candidate.HunkLineIndex {
			candidateSourceLine = sourceLine
		}
		source.WriteString(line.Text)
		source.WriteByte('\n')
	}
	if candidateSourceLine == 0 {
		return false
	}
	fset := token.NewFileSet()
	parsedFile, err := parser.ParseFile(fset, "review_hunk.go", source.String(), 0)
	if err != nil {
		return false
	}
	fn := enclosingFunctionForLine(fset, parsedFile, candidateSourceLine)
	if fn == nil || fn.Body == nil {
		return false
	}
	return functionContainsBoundCleanup(
		fset,
		parsedFile,
		fn,
		candidateSourceLine,
		matcher,
	)
}

func isFunctionStartLine(line diffLine) bool {
	if line.Kind != diffLineAdded && line.Kind != diffLineContext {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(line.Text), "func ")
}

func hunkText(hunk diffHunk) string {
	var builder strings.Builder
	for _, line := range hunk.Lines {
		if line.Kind != diffLineAdded && line.Kind != diffLineContext {
			continue
		}
		builder.WriteString(line.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func isExplicitIgnoredError(line string) bool {
	if strings.HasPrefix(line, "_, _ = ") && strings.Contains(line, "(") {
		return true
	}
	if !strings.HasPrefix(line, "_ = ") {
		return false
	}
	call := strings.TrimSpace(strings.TrimPrefix(line, "_ = "))
	errorReturningFragments := []string{
		".Close(",
		".Commit(",
		".Rollback(",
		"os.Remove(",
		"os.Mkdir(",
		"os.MkdirAll(",
		"os.WriteFile(",
		"json.Unmarshal(",
		"json.NewEncoder(",
		"json.NewDecoder(",
		"io.Copy(",
	}
	for _, fragment := range errorReturningFragments {
		if strings.Contains(call, fragment) {
			return true
		}
	}
	return false
}

func runMissingTestsRule(parsed parsedDiff) []ruleMatch {
	var matches []ruleMatch
	for fileIndex, file := range parsed.Files {
		if !file.isGoFile() || file.IsDeleted || strings.HasSuffix(file.reviewPath(), "_test.go") {
			continue
		}
		if hasRelatedTestFileChange(parsed, file) {
			continue
		}
		candidates := candidatesForFile(parsed, fileIndex)
		if len(candidates) == 0 {
			continue
		}
		if file.IsNew {
			matches = append(matches, newRuleMatch(candidates[0], ruleMissingTests, "low", "testing",
				"New Go file has no matching test change",
				"Add or update tests that exercise the new behavior.",
				confidenceWarning))
			continue
		}
		for _, candidate := range candidates {
			if exportedFuncRegex.MatchString(strings.TrimSpace(candidate.Text)) {
				matches = append(matches, newRuleMatch(candidate, ruleMissingTests, "low", "testing",
					"Exported Go behavior changed without tests",
					"Add or update tests for the exported behavior.",
					confidenceWarning))
				break
			}
		}
	}
	return matches
}

func hasRelatedTestFileChange(parsed parsedDiff, changed changedFile) bool {
	changedDir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(changed.reviewPath())))
	for _, file := range parsed.Files {
		path := file.reviewPath()
		if strings.HasSuffix(path, "_test.go") &&
			filepath.ToSlash(filepath.Dir(filepath.FromSlash(path))) == changedDir {
			return true
		}
	}
	return false
}

func candidatesForFile(parsed parsedDiff, fileIndex int) []candidateLine {
	allCandidates := parsed.candidateLines()
	candidates := make([]candidateLine, 0, len(allCandidates))
	for _, candidate := range allCandidates {
		if candidate.FileIndex == fileIndex {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}
