//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package analysis implements deterministic Go code-review rules.
package analysis

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

type addedLine struct {
	file string
	line int
	text string
}
type rule struct {
	id, category, severity, title, recommendation string
	confidence                                    float64
	bucket                                        reviewmodel.Bucket
	match                                         func(addedLine, string) bool
}

// RuleConfig is the validated Skill-owned metadata applied to an implementation.
type RuleConfig struct {
	ID, Category, Severity string
	Confidence             float64
	Modes                  []string
	Enabled                bool
}

var dynamicShell = regexp.MustCompile(`exec\.Command(?:Context)?\([^\n]*(?:"sh"|"bash"|"cmd"|"powershell")[^\n]*(?:"-c"|"/c"|-Command)`)
var ignoredError = regexp.MustCompile(`(?:^|[,{;])\s*(?:_\s*=|[^:=]+,\s*_\s*:=)`)
var placeholderCredential = regexp.MustCompile(`(?i)(?:password|passwd|pwd|token|secret|api[_-]?key)\s*[:=]\s*["']?(?:example|placeholder|dummy|changeme|test)(?:[-_][a-z0-9]+)*(?:["'\s,;#]|$)`)
var rules = []rule{{"GO-SECRET-001", "sensitive_information", "critical", "Hard-coded secret", "Load credential from an approved secret provider and rotate exposed value.", 0.95, "", func(line addedLine, _ string) bool {
	return redact.ContainsSecret(line.text)
}}, {"GO-SEC-001", "security", "high", "Dangerous command or permission", "Use fixed argv without a shell and apply least-privilege file permissions.", 0.90, "", func(line addedLine, _ string) bool {
	return dynamicShell.MatchString(line.text) || strings.Contains(line.text, "0777")
}}, {"GO-CTX-001", "context_leak", "high", "Context or ticker cleanup missing", "Call cancel or Stop with defer immediately after successful creation.", 0.86, "", matchContextLeak}, {"GO-GOR-001", "goroutine_leak", "medium", "Goroutine has no clear exit path", "Add context/done cancellation and prove every blocking path can exit.", 0.70, reviewmodel.BucketHumanReview, matchGoroutineLeak}, {"GO-RES-001", "resource_lifecycle", "high", "Resource may not be closed", "Check creation error, then defer Close on the acquired resource.", 0.86, "", matchResourceLeak}, {"GO-ERR-001", "error_handling", "medium", "Error result discarded", "Handle and wrap the error with operation context.", 0.82, "", func(line addedLine, _ string) bool {
	return ignoredError.MatchString(line.text)
}}, {"GO-DB-001", "database_lifecycle", "high", "Database lifecycle incomplete", "Defer rollback after Begin and close rows; commit only after all operations succeed.", 0.86, "", matchDatabaseLeak}}

// Analyze evaluates deterministic rules against added lines only.
func Analyze(files []diffparse.ChangedFile) []reviewmodel.Finding {
	return AnalyzeSources(files, nil)
}

// AnalyzeSources uses complete post-change files when a trusted input loader supplied them.
func AnalyzeSources(files []diffparse.ChangedFile, sources map[string][]byte) []reviewmodel.Finding {
	var findings []reviewmodel.Finding
	for _, file := range files {
		if ignoredFile(file, sources[file.NewPath]) {
			continue
		}
		context := fileText(file)
		astFindings, parsed := analyzeAST(file, sources[file.NewPath])
		findings = append(findings, astFindings...)
		for _, line := range addedLines(file) {
			for _, candidate := range rules {
				if ignoredRuleLine(line.text, candidate.id) {
					continue
				}
				patchOnly := candidate.id == "GO-SECRET-001" || candidate.id == "GO-GOR-001" || candidate.id == "GO-SEC-001"
				if parsed && !patchOnly {
					continue
				}
				if candidate.match(line, context) {
					findings = append(findings, candidate.finding(line))
				}
			}
		}
	}
	return append(findings, missingTests(files)...)
}

func ignoredFile(file diffparse.ChangedFile, source []byte) bool {
	name := "/" + strings.TrimPrefix(strings.ReplaceAll(file.NewPath, `\`, "/"), "/")
	text := string(source)
	if text == "" {
		text = fileText(file)
	}
	return strings.Contains(name, "/vendor/") || generatedSource(text)
}

func ignoredRuleLine(text, ruleID string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	comment := strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "/*") || strings.HasPrefix(lower, "*")
	if comment && ruleID != "GO-SECRET-001" {
		return true
	}
	return ruleID == "GO-SECRET-001" && (strings.Contains(lower, "[redacted") || placeholderCredential.MatchString(text))
}
func generatedSource(source string) bool {
	marker, packageDecl := strings.Index(source, "// Code generated "), strings.Index(source, "package ")
	if marker < 0 || packageDecl >= 0 && marker > packageDecl || marker > 0 && source[marker-1] != '\n' {
		return false
	}
	line := strings.SplitN(source[marker:], "\n", 2)[0]
	return strings.HasSuffix(strings.TrimSpace(line), " DO NOT EDIT.")
}

// AnalyzeConfigured makes the Skill manifest the production metadata and mode source of truth.
func AnalyzeConfigured(files []diffparse.ChangedFile, sources map[string][]byte, configs []RuleConfig) []reviewmodel.Finding {
	return configureFindings(AnalyzeSources(files, sources), configs)
}
func configureFindings(findings []reviewmodel.Finding, configs []RuleConfig) []reviewmodel.Finding {
	byID := make(map[string]RuleConfig, len(configs))
	for _, config := range configs {
		byID[config.ID] = config
	}
	result := make([]reviewmodel.Finding, 0, len(findings))
	for _, finding := range findings {
		config, ok := byID[finding.RuleID]
		if !ok || !config.Enabled || !supportsMode(config.Modes, finding.Source) {
			continue
		}
		finding.Category, finding.Severity = config.Category, config.Severity
		finding.Confidence = config.Confidence
		result = append(result, finding)
	}
	return result
}
func supportsMode(modes []string, source string) bool {
	wanted := "patch"
	if strings.Contains(source, "_ast") {
		wanted = "ast"
	}
	for _, mode := range modes {
		if mode == wanted {
			return true
		}
	}
	return false
}
func (r rule) finding(line addedLine) reviewmodel.Finding {
	evidence := strings.TrimSpace(line.text)
	if r.id == "GO-SECRET-001" {
		evidence = redact.String(evidence)
	}
	return reviewmodel.Finding{Bucket: r.bucket, Severity: r.severity, Category: r.category, File: line.file, Line: line.line, Title: r.title, Evidence: evidence, Recommendation: r.recommendation, Confidence: r.confidence, Source: "deterministic_patch", RuleID: r.id}
}
func addedLines(file diffparse.ChangedFile) []addedLine {
	path := file.NewPath
	if path == "" {
		path = file.OldPath
	}
	var result []addedLine
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == '+' {
				result = append(result, addedLine{path, int(line.NewLine), line.Content})
			}
		}
	}
	return result
}
func fileText(file diffparse.ChangedFile) string {
	var text strings.Builder
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind != '-' {
				text.WriteString(line.Content)
				text.WriteByte('\n')
			}
		}
	}
	return text.String()
}
func matchContextLeak(line addedLine, file string) bool {
	if strings.Contains(line.text, "context.WithCancel(") || strings.Contains(line.text, "context.WithTimeout(") {
		return !strings.Contains(file, "cancel()")
	}
	if strings.Contains(line.text, "time.NewTicker(") {
		return !strings.Contains(file, ".Stop()")
	}
	return false
}
func matchGoroutineLeak(line addedLine, file string) bool {
	if !strings.Contains(line.text, "go func") {
		return false
	}
	return strings.Contains(file, "for {") && !strings.Contains(file, "ctx.Done()") && !strings.Contains(file, "done")
}
func matchResourceLeak(line addedLine, file string) bool {
	constructors := []string{"os.Open(", "os.Create(", "http.Get(", ".Query("}
	for _, constructor := range constructors {
		if strings.Contains(line.text, constructor) {
			return !strings.Contains(file, ".Close()")
		}
	}
	return false
}
func matchDatabaseLeak(line addedLine, file string) bool {
	if strings.Contains(line.text, ".Begin(") || strings.Contains(line.text, ".BeginTx(") {
		return !strings.Contains(file, ".Rollback()") || !strings.Contains(file, ".Commit()")
	}
	if strings.Contains(line.text, "sql.Open(") {
		return !strings.Contains(file, ".Close()")
	}
	return false
}
func missingTests(files []diffparse.ChangedFile) []reviewmodel.Finding {
	tested := make(map[string]bool)
	changed := make(map[string]addedLine)
	for _, file := range files {
		path := file.NewPath
		if ignoredFile(file, nil) {
			continue
		}
		if strings.HasSuffix(path, "_test.go") {
			tested[dir(path)] = true
			continue
		}
		if strings.HasSuffix(path, ".go") {
			if line, ok := behavioralLine(file); ok {
				changed[dir(path)] = line
			}
		}
	}
	var findings []reviewmodel.Finding
	for packageDir, line := range changed {
		if tested[packageDir] {
			continue
		}
		findings = append(findings, reviewmodel.Finding{Bucket: reviewmodel.BucketWarnings, Severity: "medium", Category: "missing_tests", File: line.file, Line: line.line, Title: "Behavior change has no test change", Evidence: "Go behavior changed without a same-package _test.go diff", Recommendation: "Add focused positive, negative, and cleanup-path tests.", Confidence: 0.75, Source: "deterministic_patch", RuleID: "GO-TEST-001"})
	}
	return findings
}
func behavioralLine(file diffparse.ChangedFile) (addedLine, bool) {
	for _, line := range addedLines(file) {
		text := strings.TrimSpace(line.text)
		if text == "" || ignoredRuleLine(text, "GO-ERR-001") || strings.HasPrefix(text, "package ") || strings.HasPrefix(text, "import ") || text == "(" || text == ")" || strings.HasPrefix(text, `"`) {
			continue
		}
		return line, true
	}
	return addedLine{}, false
}
func dir(path string) string {
	index := strings.LastIndex(path, "/")
	if index < 0 {
		return "."
	}
	return path[:index]
}

type astState struct {
	file     string
	set      *token.FileSet
	added    map[int]string
	deferred map[string]int
	pending  []lifecycle
	findings []reviewmodel.Finding
	source   string
	types    *types.Info
}
type astFinding struct {
	id, title, recommendation, category, severity string
	confidence                                    float64
}
type lifecycle struct {
	line    addedLine
	finding astFinding
	closed  []string
}

var (
	errorFinding        = astFinding{"GO-ERR-001", "Error result discarded", "Handle and wrap the error with operation context.", "error_handling", "medium", 0.90}
	databaseOpenFinding = astFinding{"GO-DB-001", "Database opened in review scope", "Reuse an injected database handle and close it at application shutdown.", "database_lifecycle", "medium", 0.81}
	permissionFinding   = astFinding{"GO-SEC-001", "World-writable permission", "Use least-privilege file permissions.", "security", "high", 0.95}
	shellFinding        = astFinding{"GO-SEC-001", "Dynamic shell execution", "Use a fixed executable and fixed argv without a shell.", "security", "high", 0.96}
	cancelFinding       = astFinding{"GO-CTX-001", "Context cancel function is not called", "Defer the cancel function immediately after creation.", "context_leak", "high", 0.92}
	tickerFinding       = astFinding{"GO-CTX-001", "Ticker is not stopped", "Defer ticker.Stop immediately after creation.", "context_leak", "high", 0.92}
	resourceFinding     = astFinding{"GO-RES-001", "Resource is not closed", "Check creation error, then defer Close.", "resource_lifecycle", "high", 0.92}
	transactionFinding  = astFinding{"GO-DB-001", "Transaction lacks rollback fallback", "Defer Rollback immediately and Commit only after all operations succeed.", "database_lifecycle", "high", 0.92}
)

func analyzeAST(changed diffparse.ChangedFile, fullSource []byte) ([]reviewmodel.Finding, bool) {
	source, added := reconstructedSource(changed)
	if len(fullSource) != 0 {
		source = fullAnalysisSource(fullSource, added)
	}
	set := token.NewFileSet()
	parsed, err := parser.ParseFile(set, changed.NewPath, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	sourceKind := "deterministic_ast"
	typeInfo, checked := typeCheck(set, parsed)
	if checked {
		sourceKind = "deterministic_ast_types"
	}
	state := &astState{file: changed.NewPath, set: set, added: added, deferred: make(map[string]int), source: sourceKind, types: typeInfo}
	state.inspectScopes(parsed)
	return state.findings, true
}
func (s *astState) inspectScopes(file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			s.inspect(declaration)
			continue
		}
		s.inspectFunction(function)
	}
}
func (s *astState) inspectFunction(scope ast.Node) {
	s.resetLifecycle()
	var body *ast.BlockStmt
	switch value := scope.(type) {
	case *ast.FuncDecl:
		body = value.Body
	case *ast.BlockStmt:
		body = value
	}
	if body != nil {
		for _, statement := range body.List {
			if call := unconditionalCall(statement); call != nil {
				s.deferred[calledName(call.Fun)] = s.set.Position(call.Pos()).Line
			}
		}
	}
	s.inspect(scope)
	s.finishLifecycle()
	ast.Inspect(scope, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		s.inspectFunction(literal.Body)
		return false
	})
}
func unconditionalCall(statement ast.Stmt) *ast.CallExpr {
	var expression ast.Expr
	switch value := statement.(type) {
	case *ast.DeferStmt:
		return value.Call
	case *ast.ExprStmt:
		expression = value.X
	case *ast.ReturnStmt:
		if len(value.Results) > 0 {
			expression = value.Results[0]
		}
	case *ast.AssignStmt:
		if len(value.Rhs) > 0 {
			expression = value.Rhs[0]
		}
	case *ast.IfStmt:
		if assign, ok := value.Init.(*ast.AssignStmt); ok && len(assign.Rhs) > 0 {
			expression = assign.Rhs[0]
		}
	}
	call, _ := expression.(*ast.CallExpr)
	return call
}
func (s *astState) resetLifecycle() {
	s.deferred = make(map[string]int)
	s.pending = nil
}
func fullAnalysisSource(fullSource []byte, added map[int]string) string {
	source := string(fullSource)
	expected := strings.TrimSpace(added[1])
	if !strings.HasPrefix(expected, "package ") {
		return source
	}
	lines := strings.Split(source, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == expected {
			return strings.Join(lines[index:], "\n")
		}
	}
	return source
}
func typeCheck(set *token.FileSet, file *ast.File) (*types.Info, bool) {
	config := types.Config{Importer: importer.Default(), Error: func(error) {
	}}
	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue), Selections: make(map[*ast.SelectorExpr]*types.Selection), Uses: make(map[*ast.Ident]types.Object)}
	_, err := config.Check("review.local/package", set, []*ast.File{file}, info)
	return info, err == nil
}
func reconstructedSource(changed diffparse.ChangedFile) (string, map[int]string) {
	lines := make(map[int]string)
	added := make(map[int]string)
	maxLine := 0
	for _, hunk := range changed.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == '-' || line.NewLine <= 0 {
				continue
			}
			number := int(line.NewLine)
			lines[number] = line.Content
			if line.Kind == '+' {
				added[number] = line.Content
			}
			if number > maxLine {
				maxLine = number
			}
		}
	}
	var source strings.Builder
	for line := 1; line <= maxLine; line++ {
		source.WriteString(lines[line])
		source.WriteByte('\n')
	}
	return source.String(), added
}
func (s *astState) inspect(scope ast.Node) {
	ast.Inspect(scope, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		line := s.set.Position(node.Pos()).Line
		if _, changed := s.added[line]; !changed {
			return true
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			s.inspectAssignment(value, line)
		case *ast.CallExpr:
			s.inspectCall(value, line)
		}
		return true
	})
}
func (s *astState) inspectAssignment(assign *ast.AssignStmt, line int) {
	for index, left := range assign.Lhs {
		if ident, ok := left.(*ast.Ident); ok && ident.Name == "_" && s.discardsError(assign, index) {
			s.add(line, errorFinding)
		}
	}
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return
	}
	name := identifier(assign.Lhs[0])
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if name == "" || !ok {
		return
	}
	s.trackAssignment(name, assign, call, addedLine{s.file, line, s.added[line]})
}
func (s *astState) track(line addedLine, finding astFinding, calls ...string) {
	s.pending = append(s.pending, lifecycle{line: line, finding: finding, closed: calls})
}
func (s *astState) discardsError(assign *ast.AssignStmt, index int) bool {
	assigned := assignedType(s.types, assign, index)
	if assigned == nil {
		return false
	}
	errorType := types.Universe.Lookup("error").Type()
	return types.AssignableTo(assigned, errorType)
}
func assignedType(info *types.Info, assign *ast.AssignStmt, index int) types.Type {
	if info == nil || index >= len(assign.Lhs) {
		return nil
	}
	if len(assign.Rhs) == len(assign.Lhs) {
		return info.TypeOf(assign.Rhs[index])
	}
	if len(assign.Rhs) != 1 {
		return nil
	}
	valueType := info.TypeOf(assign.Rhs[0])
	if tuple, ok := valueType.(*types.Tuple); ok && index < tuple.Len() {
		return tuple.At(index).Type()
	}
	if index == 0 {
		return valueType
	}
	return nil
}
func (s *astState) trackAssignment(name string, assign *ast.AssignStmt, call *ast.CallExpr, lineData addedLine) {
	switch calledName(call.Fun) {
	case "context.WithCancel", "context.WithTimeout", "context.WithDeadline":
		if len(assign.Lhs) > 1 {
			s.track(lineData, cancelFinding, identifier(assign.Lhs[1]))
		}
	case "time.NewTicker":
		s.track(lineData, tickerFinding, name+".Stop")
	case "os.Open", "os.Create", "http.Get":
		s.track(lineData, resourceFinding, name+".Close", name+".Body.Close")
	case "sql.Open":
		s.track(lineData, resourceFinding, name+".Close")
		s.addLine(lineData, databaseOpenFinding)
	default:
		called := calledName(call.Fun)
		if strings.HasSuffix(called, ".Query") || strings.HasSuffix(called, ".QueryContext") {
			s.track(lineData, resourceFinding, name+".Close")
		}
		if strings.HasSuffix(called, ".Begin") || strings.HasSuffix(called, ".BeginTx") {
			s.track(lineData, transactionFinding, name+".Rollback")
		}
	}
}
func (s *astState) inspectCall(call *ast.CallExpr, line int) {
	name := calledName(call.Fun)
	if name == "os.Chmod" && len(call.Args) > 1 {
		if literal, ok := call.Args[1].(*ast.BasicLit); ok && strings.Contains(literal.Value, "0777") {
			s.add(line, permissionFinding)
		}
	}
	if (name == "exec.Command" || name == "exec.CommandContext") && isShellCommand(name, call.Args) {
		s.add(line, shellFinding)
	}
}
func isShellCommand(name string, arguments []ast.Expr) bool {
	executableIndex := 0
	if name == "exec.CommandContext" {
		executableIndex = 1
	}
	if len(arguments) <= executableIndex+1 {
		return false
	}
	executable, ok := stringLiteral(arguments[executableIndex])
	if !ok || !isShellExecutable(executable) {
		return false
	}
	flag, ok := stringLiteral(arguments[executableIndex+1])
	return ok && (flag == "-c" || flag == "/c" || strings.EqualFold(flag, "-Command"))
}
func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}
func isShellExecutable(executable string) bool {
	base := strings.TrimSuffix(strings.ToLower(path.Base(strings.ReplaceAll(executable, `\`, "/"))), ".exe")
	switch base {
	case "sh", "bash", "dash", "zsh", "cmd", "powershell", "pwsh":
		return true
	default:
		return false
	}
}
func (s *astState) finishLifecycle() {
	for _, item := range s.pending {
		closed := false
		for _, call := range item.closed {
			line, ok := s.deferred[call]
			closed = closed || ok && line >= item.line.line
		}
		if !closed {
			s.addLine(item.line, item.finding)
		}
	}
}
func (s *astState) add(line int, finding astFinding) {
	s.addLine(addedLine{s.file, line, s.added[line]}, finding)
}
func (s *astState) addLine(line addedLine, finding astFinding) {
	s.findings = append(s.findings, reviewmodel.Finding{Severity: finding.severity, Category: finding.category, File: line.file, Line: line.line, Title: finding.title, Evidence: strings.TrimSpace(line.text), Recommendation: finding.recommendation, Confidence: finding.confidence, Source: s.source, RuleID: finding.id})
}
func identifier(expression ast.Expr) string {
	ident, ok := expression.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}
func calledName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		prefix := calledName(value.X)
		if prefix == "" {
			return value.Sel.Name
		}
		return prefix + "." + value.Sel.Name
	default:
		return ""
	}
}
