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

	missingTestsIndex := newMissingTestsRuleIndex(parsed.Files, candidates)
	matches = append(matches, runMissingTestsRule(parsed.Files, missingTestsIndex)...)
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
	return functionProvesBoundCleanup(
		fset,
		parsedFile,
		fn,
		candidate.Line,
		matcher,
	)
}

type resourceFlowState uint8

const (
	resourceUnacquired resourceFlowState = 1 << iota
	resourceActive
	resourceSafe
)

type resourceFlow struct {
	normal    resourceFlowState
	breaks    resourceFlowState
	continues resourceFlowState
}

type resourceAcquisition struct {
	assignment     *ast.AssignStmt
	resourceObject types.Object
	errorObject    types.Object
}

type resourceCleanupAnalyzer struct {
	info               *types.Info
	matcher            cleanupMatcher
	acquisition        resourceAcquisition
	bodyAliases        map[types.Object]bool
	valid              bool
	acquisitionReached bool
}

func functionProvesBoundCleanup(
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

	acquisition := candidateResourceAcquisition(
		fn.Body,
		fset,
		info,
		matcher.variable,
		candidateLine,
	)
	if acquisition.assignment == nil || acquisition.resourceObject == nil ||
		functionHasUnsupportedResourceControlFlow(fn.Body) {
		return false
	}
	analyzer := resourceCleanupAnalyzer{
		info:        info,
		matcher:     matcher,
		acquisition: acquisition,
		bodyAliases: make(map[types.Object]bool),
		valid:       true,
	}
	flow := analyzer.analyzeBlock(fn.Body.List, resourceUnacquired)
	return analyzer.valid && analyzer.acquisitionReached &&
		flow.breaks == 0 && flow.continues == 0 &&
		flow.normal&resourceActive == 0
}

func candidateResourceAcquisition(
	body *ast.BlockStmt,
	fset *token.FileSet,
	info *types.Info,
	variable string,
	line int,
) resourceAcquisition {
	var result resourceAcquisition
	ast.Inspect(body, func(node ast.Node) bool {
		if result.assignment != nil {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		assign, ok := node.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE || fset.Position(assign.Pos()).Line != line {
			return true
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name != variable {
				continue
			}
			result.assignment = assign
			result.resourceObject = bindingObject(info, ident)
			if i+1 < len(assign.Lhs) {
				if errorIdent, ok := assign.Lhs[i+1].(*ast.Ident); ok && errorIdent.Name != "_" {
					result.errorObject = bindingObject(info, errorIdent)
				}
			}
			break
		}
		return result.assignment == nil
	})
	return result
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

func (a *resourceCleanupAnalyzer) analyzeBlock(
	statements []ast.Stmt,
	input resourceFlowState,
) resourceFlow {
	flow := resourceFlow{normal: input}
	for i := 0; i < len(statements) && flow.normal != 0 && a.valid; i++ {
		if assignment, ok := statements[i].(*ast.AssignStmt); ok &&
			assignment == a.acquisition.assignment {
			flow.normal = a.acquire(flow.normal)
			if i+1 < len(statements) && a.isStandardErrorGuard(statements[i+1]) {
				i++
			}
			continue
		}
		statementFlow := a.analyzeStatement(statements[i], flow.normal)
		flow.normal = statementFlow.normal
		flow.breaks |= statementFlow.breaks
		flow.continues |= statementFlow.continues
	}
	return flow
}

func (a *resourceCleanupAnalyzer) acquire(input resourceFlowState) resourceFlowState {
	a.acquisitionReached = true
	if input&resourceActive != 0 {
		a.valid = false
		return 0
	}
	if input&(resourceUnacquired|resourceSafe) == 0 {
		return 0
	}
	return resourceActive
}

func (a *resourceCleanupAnalyzer) analyzeStatement(
	statement ast.Stmt,
	input resourceFlowState,
) resourceFlow {
	if input&resourceActive != 0 && a.statementObscuresActiveBinding(statement) {
		a.valid = false
		return resourceFlow{}
	}
	switch typed := statement.(type) {
	case *ast.BlockStmt:
		return a.analyzeBlock(typed.List, input)
	case *ast.AssignStmt:
		a.observeBodyAliasAssignment(typed, input)
		state := a.applyCleanupExpressions(input, typed.Rhs)
		a.rejectActiveReassignment(typed.Lhs, state)
		return resourceFlow{normal: state}
	case *ast.DeclStmt:
		state := input
		if declaration, ok := typed.Decl.(*ast.GenDecl); ok {
			for _, spec := range declaration.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				a.observeBodyAliasDeclaration(value, state)
				state = a.applyCleanupExpressions(state, value.Values)
			}
		}
		return resourceFlow{normal: state}
	case *ast.ExprStmt:
		a.rejectBodyBindingCall(typed.X, input)
		state := a.applyCleanupExpression(input, typed.X)
		if isDirectPanicCall(typed.X) {
			a.rejectActiveExit(state)
			return resourceFlow{}
		}
		return resourceFlow{normal: state}
	case *ast.DeferStmt:
		if a.isCleanupCall(typed.Call) {
			return resourceFlow{normal: markResourceSafe(input)}
		}
		return resourceFlow{normal: input}
	case *ast.GoStmt:
		a.rejectBodyBindingCall(typed.Call, input)
		return resourceFlow{normal: input}
	case *ast.ReturnStmt:
		state := a.applyCleanupExpressions(input, typed.Results)
		a.rejectActiveExit(state)
		return resourceFlow{}
	case *ast.IfStmt:
		return a.analyzeIf(typed, input)
	case *ast.ForStmt:
		return a.analyzeFor(typed, input)
	case *ast.RangeStmt:
		return a.analyzeRange(typed, input)
	case *ast.SwitchStmt:
		return a.analyzeSwitch(typed, input)
	case *ast.TypeSwitchStmt:
		return a.analyzeTypeSwitch(typed, input)
	case *ast.SelectStmt:
		return a.analyzeSelect(typed, input)
	case *ast.BranchStmt:
		switch typed.Tok {
		case token.BREAK:
			return resourceFlow{breaks: input}
		case token.CONTINUE:
			return resourceFlow{continues: input}
		default:
			a.valid = false
			return resourceFlow{}
		}
	case *ast.IncDecStmt:
		a.rejectActiveReassignment([]ast.Expr{typed.X}, input)
		return resourceFlow{normal: input}
	case *ast.LabeledStmt:
		return a.analyzeStatement(typed.Stmt, input)
	case *ast.SendStmt:
		a.rejectBodyBindingExposure(typed.Value, input)
		return resourceFlow{normal: input}
	case *ast.EmptyStmt:
		return resourceFlow{normal: input}
	default:
		a.valid = false
		return resourceFlow{}
	}
}

func (a *resourceCleanupAnalyzer) statementObscuresActiveBinding(statement ast.Stmt) bool {
	obscured := false
	ast.Inspect(statement, func(node ast.Node) bool {
		if obscured {
			return false
		}
		switch typed := node.(type) {
		case *ast.FuncLit:
			ast.Inspect(typed.Body, func(inner ast.Node) bool {
				ident, ok := inner.(*ast.Ident)
				if ok && a.identifierMayObscureResource(ident) {
					obscured = true
					return false
				}
				return !obscured
			})
			return false
		case *ast.UnaryExpr:
			if typed.Op != token.AND {
				return true
			}
			if a.expressionMayBeCleanupTarget(typed.X) {
				obscured = true
				return false
			}
		}
		return true
	})
	return obscured
}

func (a *resourceCleanupAnalyzer) identifierMayBeResource(ident *ast.Ident) bool {
	if ident == nil || ident.Name != a.matcher.variable {
		return false
	}
	object := bindingObject(a.info, ident)
	return object == nil || object == a.acquisition.resourceObject
}

func (a *resourceCleanupAnalyzer) identifierMayObscureResource(ident *ast.Ident) bool {
	if a.identifierMayBeResource(ident) {
		return true
	}
	return a.matcher.body && a.identifierIsBodyAlias(ident)
}

func (a *resourceCleanupAnalyzer) identifierIsBodyAlias(ident *ast.Ident) bool {
	if ident == nil {
		return false
	}
	object := bindingObject(a.info, ident)
	return object != nil && a.bodyAliases[object]
}

func (a *resourceCleanupAnalyzer) identifierMayBeBodyResponse(ident *ast.Ident) bool {
	return a.identifierMayBeResource(ident) || a.identifierIsBodyAlias(ident)
}

func (a *resourceCleanupAnalyzer) expressionMayBeCleanupTarget(expression ast.Expr) bool {
	expression = unparenthesizedExpression(expression)
	if ident, ok := expression.(*ast.Ident); ok {
		return a.identifierMayBeResource(ident)
	}
	if !a.matcher.body {
		return false
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Body" {
		star, ok := expression.(*ast.StarExpr)
		if !ok {
			return false
		}
		ident, ok := unparenthesizedExpression(star.X).(*ast.Ident)
		return ok && a.identifierMayBeBodyResponse(ident)
	}
	return a.expressionRefersToBodyResponse(selector.X)
}

func (a *resourceCleanupAnalyzer) expressionRefersToBodyResponse(expression ast.Expr) bool {
	expression = unparenthesizedExpression(expression)
	if ident, ok := expression.(*ast.Ident); ok {
		return a.identifierMayBeBodyResponse(ident)
	}
	star, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := unparenthesizedExpression(star.X).(*ast.Ident)
	return ok && a.identifierMayBeBodyResponse(ident)
}

func (a *resourceCleanupAnalyzer) observeBodyAliasAssignment(
	statement *ast.AssignStmt,
	state resourceFlowState,
) {
	if !a.matcher.body || state&resourceActive == 0 {
		return
	}
	a.rejectBodyBindingCalls(statement.Rhs, state)
	if !a.valid {
		return
	}
	if len(statement.Lhs) != len(statement.Rhs) {
		for _, expression := range statement.Rhs {
			if a.expressionContainsStoredBodyResponse(expression) {
				a.valid = false
				return
			}
		}
		return
	}
	for i, expression := range statement.Rhs {
		if a.expressionIsBodyResponseValue(expression) {
			a.bindBodyAlias(statement.Lhs[i])
			continue
		}
		if a.expressionContainsStoredBodyResponse(expression) {
			a.valid = false
			return
		}
	}
}

func (a *resourceCleanupAnalyzer) observeBodyAliasDeclaration(
	spec *ast.ValueSpec,
	state resourceFlowState,
) {
	if !a.matcher.body || state&resourceActive == 0 {
		return
	}
	a.rejectBodyBindingCalls(spec.Values, state)
	if !a.valid {
		return
	}
	if len(spec.Names) != len(spec.Values) {
		for _, expression := range spec.Values {
			if a.expressionContainsStoredBodyResponse(expression) {
				a.valid = false
				return
			}
		}
		return
	}
	for i, expression := range spec.Values {
		if a.expressionIsBodyResponseValue(expression) {
			a.bindBodyAlias(spec.Names[i])
			continue
		}
		if a.expressionContainsStoredBodyResponse(expression) {
			a.valid = false
			return
		}
	}
}

func (a *resourceCleanupAnalyzer) bindBodyAlias(expression ast.Expr) {
	ident, ok := unparenthesizedExpression(expression).(*ast.Ident)
	if !ok {
		a.valid = false
		return
	}
	if ident.Name == "_" {
		return
	}
	object := bindingObject(a.info, ident)
	if object == a.acquisition.resourceObject {
		return
	}
	if object == nil || object.Parent() == nil ||
		object.Pkg() != nil && object.Parent() == object.Pkg().Scope() {
		a.valid = false
		return
	}
	a.bodyAliases[object] = true
}

func (a *resourceCleanupAnalyzer) rejectBodyBindingExposure(
	expression ast.Expr,
	state resourceFlowState,
) {
	if !a.matcher.body || state&resourceActive == 0 {
		return
	}
	if a.expressionContainsStoredBodyResponse(expression) ||
		a.expressionCallsWithBodyResponse(expression) {
		a.valid = false
	}
}

func (a *resourceCleanupAnalyzer) rejectBodyBindingCall(
	expression ast.Expr,
	state resourceFlowState,
) {
	if !a.matcher.body || state&resourceActive == 0 {
		return
	}
	if a.expressionCallsWithBodyResponse(expression) {
		a.valid = false
	}
}

func (a *resourceCleanupAnalyzer) rejectBodyBindingCalls(
	expressions []ast.Expr,
	state resourceFlowState,
) {
	for _, expression := range expressions {
		a.rejectBodyBindingCall(expression, state)
		if !a.valid {
			return
		}
	}
}

func (a *resourceCleanupAnalyzer) expressionIsBodyResponseValue(expression ast.Expr) bool {
	ident, ok := unparenthesizedExpression(expression).(*ast.Ident)
	return ok && a.identifierMayBeBodyResponse(ident)
}

func (a *resourceCleanupAnalyzer) expressionContainsStoredBodyResponse(expression ast.Expr) bool {
	expression = unparenthesizedExpression(expression)
	if ident, ok := expression.(*ast.Ident); ok {
		return a.identifierMayBeBodyResponse(ident)
	}
	switch typed := expression.(type) {
	case *ast.CompositeLit:
		for _, element := range typed.Elts {
			if a.expressionContainsStoredBodyResponse(element) {
				return true
			}
		}
	case *ast.KeyValueExpr:
		return a.expressionContainsStoredBodyResponse(typed.Key) ||
			a.expressionContainsStoredBodyResponse(typed.Value)
	case *ast.UnaryExpr:
		return typed.Op == token.AND &&
			a.expressionContainsStoredBodyResponse(typed.X)
	}
	return false
}

func (a *resourceCleanupAnalyzer) expressionCallsWithBodyResponse(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		if found {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr); ok &&
			a.expressionRefersToBodyResponse(selector.X) {
			found = true
			return false
		}
		for _, argument := range call.Args {
			if a.expressionContainsStoredBodyResponse(argument) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func (a *resourceCleanupAnalyzer) analyzeIf(
	statement *ast.IfStmt,
	input resourceFlowState,
) resourceFlow {
	state := input
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init, state)
		if initFlow.breaks != 0 || initFlow.continues != 0 {
			a.valid = false
			return resourceFlow{}
		}
		state = initFlow.normal
	}
	a.rejectBodyBindingCall(statement.Cond, state)
	state = a.applyCleanupExpression(state, statement.Cond)
	thenFlow := a.analyzeBlock(statement.Body.List, state)
	elseFlow := resourceFlow{normal: state}
	if statement.Else != nil {
		elseFlow = a.analyzeStatement(statement.Else, state)
	}
	return mergeResourceFlows(thenFlow, elseFlow)
}

func (a *resourceCleanupAnalyzer) analyzeFor(
	statement *ast.ForStmt,
	input resourceFlowState,
) resourceFlow {
	state := input
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init, state)
		if initFlow.breaks != 0 || initFlow.continues != 0 {
			a.valid = false
			return resourceFlow{}
		}
		state = initFlow.normal
	}
	if statement.Cond != nil {
		a.rejectBodyBindingCall(statement.Cond, state)
	}
	return a.analyzeLoop(statement.Body, statement.Post, state)
}

func (a *resourceCleanupAnalyzer) analyzeRange(
	statement *ast.RangeStmt,
	input resourceFlowState,
) resourceFlow {
	a.rejectBodyBindingExposure(statement.X, input)
	if statement.Tok == token.ASSIGN {
		a.rejectActiveReassignment([]ast.Expr{statement.Key, statement.Value}, input)
	}
	return a.analyzeLoop(statement.Body, nil, input)
}

func (a *resourceCleanupAnalyzer) analyzeLoop(
	body *ast.BlockStmt,
	post ast.Stmt,
	input resourceFlowState,
) resourceFlow {
	entry := input
	exits := input
	var breaks resourceFlowState
	for a.valid {
		bodyFlow := a.analyzeBlock(body.List, entry)
		breaks |= bodyFlow.breaks
		iteration := bodyFlow.normal | bodyFlow.continues
		if post != nil && iteration != 0 {
			postFlow := a.analyzeStatement(post, iteration)
			if postFlow.breaks != 0 || postFlow.continues != 0 {
				a.valid = false
				break
			}
			iteration = postFlow.normal
		}
		exits |= iteration
		nextEntry := entry | iteration
		if nextEntry == entry {
			break
		}
		entry = nextEntry
	}
	return resourceFlow{normal: exits | breaks}
}

func (a *resourceCleanupAnalyzer) analyzeSwitch(
	statement *ast.SwitchStmt,
	input resourceFlowState,
) resourceFlow {
	state := input
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init, state)
		if initFlow.breaks != 0 || initFlow.continues != 0 {
			a.valid = false
			return resourceFlow{}
		}
		state = initFlow.normal
	}
	if statement.Tag != nil {
		a.rejectBodyBindingCall(statement.Tag, state)
	}
	for _, rawClause := range statement.Body.List {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, expression := range clause.List {
			a.rejectBodyBindingCall(expression, state)
		}
	}
	return a.analyzeCaseClauses(statement.Body.List, state)
}

func (a *resourceCleanupAnalyzer) analyzeTypeSwitch(
	statement *ast.TypeSwitchStmt,
	input resourceFlowState,
) resourceFlow {
	state := input
	for _, init := range []ast.Stmt{statement.Init, statement.Assign} {
		if init == nil {
			continue
		}
		initFlow := a.analyzeStatement(init, state)
		if initFlow.breaks != 0 || initFlow.continues != 0 {
			a.valid = false
			return resourceFlow{}
		}
		state = initFlow.normal
	}
	return a.analyzeCaseClauses(statement.Body.List, state)
}

func (a *resourceCleanupAnalyzer) analyzeCaseClauses(
	clauses []ast.Stmt,
	input resourceFlowState,
) resourceFlow {
	result := resourceFlow{}
	hasDefault := false
	for _, rawClause := range clauses {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok {
			a.valid = false
			return resourceFlow{}
		}
		if clause.List == nil {
			hasDefault = true
		}
		clauseFlow := a.analyzeBlock(clause.Body, input)
		result.normal |= clauseFlow.normal | clauseFlow.breaks
		result.continues |= clauseFlow.continues
	}
	if !hasDefault {
		result.normal |= input
	}
	return result
}

func (a *resourceCleanupAnalyzer) analyzeSelect(
	statement *ast.SelectStmt,
	input resourceFlowState,
) resourceFlow {
	result := resourceFlow{normal: input}
	for _, rawClause := range statement.Body.List {
		clause, ok := rawClause.(*ast.CommClause)
		if !ok {
			a.valid = false
			return resourceFlow{}
		}
		state := input
		if clause.Comm != nil {
			commFlow := a.analyzeStatement(clause.Comm, state)
			if commFlow.breaks != 0 || commFlow.continues != 0 {
				a.valid = false
				return resourceFlow{}
			}
			state = commFlow.normal
		}
		clauseFlow := a.analyzeBlock(clause.Body, state)
		result.normal |= clauseFlow.normal | clauseFlow.breaks
		result.continues |= clauseFlow.continues
	}
	return result
}

func mergeResourceFlows(left resourceFlow, right resourceFlow) resourceFlow {
	return resourceFlow{
		normal:    left.normal | right.normal,
		breaks:    left.breaks | right.breaks,
		continues: left.continues | right.continues,
	}
}

func markResourceSafe(state resourceFlowState) resourceFlowState {
	if state&resourceActive == 0 {
		return state
	}
	return state&^resourceActive | resourceSafe
}

func (a *resourceCleanupAnalyzer) applyCleanupExpressions(
	state resourceFlowState,
	expressions []ast.Expr,
) resourceFlowState {
	for _, expression := range expressions {
		state = a.applyCleanupExpression(state, expression)
	}
	return state
}

func (a *resourceCleanupAnalyzer) applyCleanupExpression(
	state resourceFlowState,
	expression ast.Expr,
) resourceFlowState {
	call, ok := unparenthesizedExpression(expression).(*ast.CallExpr)
	if !ok || !a.isCleanupCall(call) {
		return state
	}
	return markResourceSafe(state)
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

func (a *resourceCleanupAnalyzer) isCleanupCall(call *ast.CallExpr) bool {
	receiver := a.matcher.callReceiver(call.Fun)
	if receiver == nil {
		return false
	}
	object := bindingObject(a.info, receiver)
	if object == nil {
		a.valid = false
		return false
	}
	return object == a.acquisition.resourceObject
}

func (a *resourceCleanupAnalyzer) rejectActiveReassignment(
	expressions []ast.Expr,
	state resourceFlowState,
) {
	if state&resourceActive == 0 {
		return
	}
	for _, expression := range expressions {
		if a.expressionMayBeCleanupTarget(expression) {
			a.valid = false
			return
		}
	}
}

func (a *resourceCleanupAnalyzer) rejectActiveExit(state resourceFlowState) {
	if state&resourceActive != 0 {
		a.valid = false
	}
}

func (a *resourceCleanupAnalyzer) isStandardErrorGuard(statement ast.Stmt) bool {
	if a.acquisition.errorObject == nil {
		return false
	}
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil ||
		!conditionChecksErrorNonNil(guard.Cond, a.info, a.acquisition.errorObject) {
		return false
	}
	return blockAlwaysTerminates(guard.Body)
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
	return functionProvesBoundCleanup(
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

type missingTestsRuleIndex struct {
	changedTestDirs  map[string]struct{}
	candidatesByFile map[int][]candidateLine
}

func newMissingTestsRuleIndex(files []changedFile, candidates []candidateLine) missingTestsRuleIndex {
	index := missingTestsRuleIndex{
		changedTestDirs:  make(map[string]struct{}),
		candidatesByFile: make(map[int][]candidateLine),
	}
	for _, file := range files {
		path := file.reviewPath()
		if strings.HasSuffix(path, "_test.go") {
			index.changedTestDirs[reviewPathDirectory(path)] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		index.candidatesByFile[candidate.FileIndex] = append(
			index.candidatesByFile[candidate.FileIndex], candidate,
		)
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

func reviewPathDirectory(path string) string {
	return filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
}
