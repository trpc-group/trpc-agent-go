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
	"go/parser"
	"go/scanner"
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

	hardcodedSecretQuotedValueMin = 8
	redactedSecretQuotedValueMin  = 6
)

var (
	secretAssignmentPattern = regexp.MustCompile(
		`(?i)(api[_-]?key|secret|token|password|passwd|private[_-]?key)\s*[:=]\s*` +
			quotedSensitiveValuePattern(hardcodedSecretQuotedValueMin),
	)
	awsAccessKeyPattern = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	githubTokenPattern  = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`)
	openAITokenPattern  = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}\b`)
	insecureTLSPattern  = regexp.MustCompile(`\bInsecureSkipVerify\s*(?::|=)\s*true\b`)

	fileOpenPattern   = regexp.MustCompile(`^\s*(\w+)\s*(?:,\s*\w+)?\s*:=\s*os\.(Open|Create|OpenFile)\(`)
	httpGetPattern    = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*http\.(Get|Head|Post|PostForm)\(`)
	sqlRowsPattern    = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*[^=]*\.(Query|QueryContext)\(`)
	sqlTxPattern      = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*[^=]*\.(Begin|BeginTx)\(`)
	sqlOpenPattern    = regexp.MustCompile(`^\s*(\w+)\s*,\s*\w+\s*:=\s*sql\.Open\(`)
	exportedFuncRegex = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Z][A-Za-z0-9_]*)\s*\(`)
)

func quotedSensitiveValuePattern(minLength int) string {
	repetition := "{" + strconv.Itoa(minLength) + ",}"
	// Backslash escapes are consumed before ordinary characters so quote
	// termination follows the parity of consecutive backslashes.
	return `(?:"(?:\\[^\r\n]|[^"\\\r\n])` + repetition +
		`"|'(?:\\[^\r\n]|[^'\\\r\n])` + repetition + `')`
}

func assignmentSensitiveValuePattern(quotedMin int, unquotedMin int) string {
	return `(?:` + quotedSensitiveValuePattern(quotedMin) +
		`|[^"'\s,;]{` + strconv.Itoa(unquotedMin) + `,})`
}

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

type ruleEvaluation struct {
	match              ruleMatch
	lifecycleCandidate int
}

type lifecycleCandidate struct {
	candidate candidateLine
	matcher   cleanupMatcher
}

type lifecycleAnalysisStats struct {
	ParsedSourceUnits        int
	TypeCheckedSourceUnits   int
	AnalyzedFunctions        int
	AnalyzedStatements       int
	CandidateStateOperations int
}

func runRules(parsed parsedDiff, repoRoot string) []ruleMatch {
	matches, _ := runRulesWithLifecycleStats(parsed, repoRoot)
	return matches
}

func runRulesWithLifecycleStats(
	parsed parsedDiff,
	repoRoot string,
) ([]ruleMatch, lifecycleAnalysisStats) {
	candidates := parsed.candidateLines()
	goroutineLeaks, _ := analyzeGoroutineCandidates(parsed.Files, repoRoot, candidates)
	evaluations := make([][]ruleEvaluation, len(candidates))
	var lifecycleCandidates []lifecycleCandidate
	for candidateIndex, candidate := range candidates {
		file := parsed.Files[candidate.FileIndex]
		trimmed := strings.TrimSpace(candidate.Text)
		evaluations[candidateIndex] = appendImmediateRuleMatches(
			evaluations[candidateIndex],
			securityRuleMatches(candidate, trimmed),
		)
		evaluations[candidateIndex] = appendImmediateRuleMatches(
			evaluations[candidateIndex],
			concurrencyRuleMatches(candidate, goroutineLeaks[candidateIndex]),
		)
		evaluations[candidateIndex] = appendLifecycleRuleEvaluations(
			evaluations[candidateIndex],
			&lifecycleCandidates,
			candidate,
			resourceRuleCandidates(candidate, file, trimmed),
		)
		evaluations[candidateIndex] = appendImmediateRuleMatches(
			evaluations[candidateIndex],
			errorRuleMatches(candidate, trimmed),
		)
		evaluations[candidateIndex] = appendLifecycleRuleEvaluations(
			evaluations[candidateIndex],
			&lifecycleCandidates,
			candidate,
			databaseRuleCandidates(candidate, file, trimmed),
		)
	}

	cleanupProven, stats := analyzeLifecycleCandidates(parsed.Files, repoRoot, lifecycleCandidates)
	var matches []ruleMatch
	for _, candidateEvaluations := range evaluations {
		for _, evaluation := range candidateEvaluations {
			if evaluation.lifecycleCandidate >= 0 &&
				cleanupProven[evaluation.lifecycleCandidate] {
				continue
			}
			matches = append(matches, evaluation.match)
		}
	}

	missingTestsIndex := newMissingTestsRuleIndex(parsed.Files, repoRoot, candidates)
	matches = append(matches, runMissingTestsRule(parsed.Files, missingTestsIndex)...)
	return matches, stats
}

func appendImmediateRuleMatches(
	evaluations []ruleEvaluation,
	matches []ruleMatch,
) []ruleEvaluation {
	for _, match := range matches {
		evaluations = append(evaluations, ruleEvaluation{
			match:              match,
			lifecycleCandidate: -1,
		})
	}
	return evaluations
}

type pendingLifecycleRule struct {
	match   ruleMatch
	matcher cleanupMatcher
}

func appendLifecycleRuleEvaluations(
	evaluations []ruleEvaluation,
	candidates *[]lifecycleCandidate,
	candidate candidateLine,
	pending []pendingLifecycleRule,
) []ruleEvaluation {
	for _, item := range pending {
		index := len(*candidates)
		*candidates = append(*candidates, lifecycleCandidate{
			candidate: candidate,
			matcher:   item.matcher,
		})
		evaluations = append(evaluations, ruleEvaluation{
			match:              item.match,
			lifecycleCandidate: index,
		})
	}
	return evaluations
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

func concurrencyRuleMatches(candidate candidateLine, leaks bool) []ruleMatch {
	if !leaks {
		return nil
	}
	return []ruleMatch{newRuleMatch(candidate, ruleGoroutineContextLeak, "medium", "concurrency",
		"Goroutine is not tied to request cancellation",
		"Pass context into the goroutine and exit when the context is cancelled.",
		confidenceLifecycle)}
}

func resourceRuleCandidates(
	candidate candidateLine,
	file changedFile,
	line string,
) []pendingLifecycleRule {
	var matches []pendingLifecycleRule
	confidence := lifecycleConfidence(file)
	if variable := firstCapture(fileOpenPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}}
		matches = append(matches, pendingLifecycleRule{
			match: newRuleMatch(candidate, ruleUnclosedFile, "medium", "resource",
				"Opened file is not closed",
				"Close the file with defer after checking the open error.",
				confidence),
			matcher: matcher,
		})
	}
	if variable := firstCapture(httpGetPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}, body: true}
		matches = append(matches, pendingLifecycleRule{
			match: newRuleMatch(candidate, ruleUnclosedHTTPBody, "medium", "resource",
				"HTTP response body is not closed",
				"Close response bodies with defer resp.Body.Close() after checking errors.",
				confidence),
			matcher: matcher,
		})
	}
	if variable := firstCapture(sqlRowsPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}}
		matches = append(matches, pendingLifecycleRule{
			match: newRuleMatch(candidate, ruleUnclosedSQLRows, "medium", "resource",
				"SQL rows are not closed",
				"Close rows with defer rows.Close() and check rows.Err().",
				confidence),
			matcher: matcher,
		})
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

func databaseRuleCandidates(
	candidate candidateLine,
	file changedFile,
	line string,
) []pendingLifecycleRule {
	var matches []pendingLifecycleRule
	confidence := lifecycleConfidence(file)
	if variable := firstCapture(sqlTxPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Commit": true, "Rollback": true}}
		matches = append(matches, pendingLifecycleRule{
			match: newRuleMatch(candidate, ruleDatabaseTxLifecycle, "high", "database",
				"Database transaction is opened without commit or rollback",
				"Ensure every transaction path commits or rolls back.",
				confidence),
			matcher: matcher,
		})
	}
	if variable := firstCapture(sqlOpenPattern, line); variable != "" {
		matcher := cleanupMatcher{variable: variable, methods: map[string]bool{"Close": true}}
		matches = append(matches, pendingLifecycleRule{
			match: newRuleMatch(candidate, ruleDatabaseOpenLifecycle, "medium", "database",
				"Database handle is opened without a close path",
				"Close database handles owned by this function or document shared ownership.",
				confidence),
			matcher: matcher,
		})
	}
	return matches
}

func lifecycleConfidence(file changedFile) float64 {
	if len(file.Hunks) > 1 {
		return confidenceBoundary
	}
	return confidenceLifecycle
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

func bindingObject(info *types.Info, ident *ast.Ident) types.Object {
	if ident == nil {
		return nil
	}
	if object := info.Defs[ident]; object != nil {
		return object
	}
	return info.Uses[ident]
}

func functionHasUnsupportedResourceControlFlow(body *ast.BlockStmt) bool {
	unsupported := false
	ast.Inspect(body, func(node ast.Node) bool {
		if unsupported {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		switch typed := node.(type) {
		case *ast.BadStmt:
			unsupported = true
		case *ast.BranchStmt:
			unsupported = typed.Tok == token.GOTO ||
				typed.Tok == token.FALLTHROUGH || typed.Label != nil
		}
		return !unsupported
	})
	return unsupported
}

func unparenthesizedExpression(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

func conditionChecksErrorNonNil(
	expression ast.Expr,
	info *types.Info,
	errorObject types.Object,
) bool {
	binary, ok := unparenthesizedExpression(expression).(*ast.BinaryExpr)
	if !ok || binary.Op != token.NEQ {
		return false
	}
	return identifierMatchesObject(binary.X, info, errorObject) && isNilIdentifier(binary.Y) ||
		isNilIdentifier(binary.X) && identifierMatchesObject(binary.Y, info, errorObject)
}

func identifierMatchesObject(expression ast.Expr, info *types.Info, object types.Object) bool {
	ident, ok := unparenthesizedExpression(expression).(*ast.Ident)
	return ok && bindingObject(info, ident) == object
}

func isNilIdentifier(expression ast.Expr) bool {
	ident, ok := unparenthesizedExpression(expression).(*ast.Ident)
	return ok && ident.Name == "nil"
}

func blockAlwaysTerminates(block *ast.BlockStmt) bool {
	for _, statement := range block.List {
		if statementAlwaysTerminates(statement) {
			return true
		}
	}
	return false
}

func statementAlwaysTerminates(statement ast.Stmt) bool {
	switch typed := statement.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.ExprStmt:
		return isDirectPanicCall(typed.X)
	case *ast.BlockStmt:
		return blockAlwaysTerminates(typed)
	case *ast.IfStmt:
		return typed.Else != nil && blockAlwaysTerminates(typed.Body) &&
			statementAlwaysTerminates(typed.Else)
	case *ast.SwitchStmt:
		return caseClausesAlwaysTerminate(typed.Body.List)
	case *ast.TypeSwitchStmt:
		return caseClausesAlwaysTerminate(typed.Body.List)
	default:
		return false
	}
}

func caseClausesAlwaysTerminate(clauses []ast.Stmt) bool {
	hasDefault := false
	for _, rawClause := range clauses {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok {
			return false
		}
		if clause.List == nil {
			hasDefault = true
		}
		if !blockAlwaysTerminates(&ast.BlockStmt{List: clause.Body}) {
			return false
		}
	}
	return hasDefault
}

func isDirectPanicCall(expression ast.Expr) bool {
	call, ok := unparenthesizedExpression(expression).(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "panic"
}

func isFunctionStartLine(line diffLine) bool {
	if line.Kind != diffLineAdded && line.Kind != diffLineContext {
		return false
	}
	first, ok := firstCodeToken(line.Text)
	return ok && first == token.FUNC
}

func firstCodeToken(line string) (token.Token, bool) {
	fset := token.NewFileSet()
	file := fset.AddFile("review_line.go", fset.Base(), len(line))
	var lexical scanner.Scanner
	lexical.Init(file, []byte(line), nil, scanner.ScanComments)
	for {
		_, scanned, literal := lexical.Scan()
		switch scanned {
		case token.EOF:
			return token.ILLEGAL, false
		case token.COMMENT:
			continue
		case token.SEMICOLON:
			if literal == "\n" {
				continue
			}
			return scanned, true
		case token.ILLEGAL:
			return token.ILLEGAL, false
		default:
			return scanned, true
		}
	}
}

func lineContainsCodeToken(line string, wanted token.Token) bool {
	fset := token.NewFileSet()
	file := fset.AddFile("review_line.go", fset.Base(), len(line))
	var lexical scanner.Scanner
	lexical.Init(file, []byte(line), nil, scanner.ScanComments)
	for {
		_, scanned, _ := lexical.Scan()
		switch scanned {
		case token.EOF:
			return false
		case token.COMMENT, token.SEMICOLON:
			continue
		case wanted:
			return true
		}
	}
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

type missingTestsRuleIndex struct {
	changedTestDirs           map[string]struct{}
	candidatesByFile          map[int][]candidateLine
	firstExportedChangeByFile map[int]candidateLine
}

func newMissingTestsRuleIndex(
	files []changedFile,
	repoRoot string,
	candidates []candidateLine,
) missingTestsRuleIndex {
	index := missingTestsRuleIndex{
		changedTestDirs:           make(map[string]struct{}),
		candidatesByFile:          make(map[int][]candidateLine),
		firstExportedChangeByFile: make(map[int]candidateLine),
	}
	exportedChanges, _ := analyzeExportedBehaviorCandidates(files, repoRoot, candidates)
	for candidateIndex, candidate := range candidates {
		index.candidatesByFile[candidate.FileIndex] = append(
			index.candidatesByFile[candidate.FileIndex], candidate,
		)
		if _, exists := index.firstExportedChangeByFile[candidate.FileIndex]; exists {
			continue
		}
		if exportedChanges[candidateIndex] ||
			exportedFuncRegex.MatchString(strings.TrimSpace(candidate.Text)) {
			index.firstExportedChangeByFile[candidate.FileIndex] = candidate
		}
	}
	for fileIndex, file := range files {
		if file.IsDeleted || file.IsBinary ||
			!strings.HasSuffix(file.NewPath, "_test.go") ||
			len(index.candidatesByFile[fileIndex]) == 0 {
			continue
		}
		index.changedTestDirs[reviewPathDirectory(file.NewPath)] = struct{}{}
	}
	return index
}

func runMissingTestsRule(files []changedFile, index missingTestsRuleIndex) []ruleMatch {
	var matches []ruleMatch
	for fileIndex, file := range files {
		if !file.isGoFile() || file.IsDeleted || strings.HasSuffix(file.reviewPath(), "_test.go") {
			continue
		}
		if _, ok := index.changedTestDirs[reviewPathDirectory(file.reviewPath())]; ok {
			continue
		}
		candidates := index.candidatesByFile[fileIndex]
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
		if candidate, ok := index.firstExportedChangeByFile[fileIndex]; ok {
			matches = append(matches, newRuleMatch(candidate, ruleMissingTests, "low", "testing",
				"Exported Go behavior changed without tests",
				"Add or update tests for the exported behavior.",
				confidenceWarning))
		}
	}
	return matches
}

func reviewPathDirectory(path string) string {
	return filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
}
