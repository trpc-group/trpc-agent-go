//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

type parsedHunkAST struct {
	file         *ast.File
	fset         *token.FileSet
	lineMap      map[int]int
	addedNewLine map[int]bool
	textByLine   map[int]string
	source       string
}

type astFacts struct {
	closeTargets            map[string]bool
	conditionalCloseTargets map[string]bool
	bodyCloseTargets        map[string]bool
	commitTargets           map[string]bool
	rollbackTargets         map[string]bool
	conditionalRollback     map[string]bool
	cancelTargets           map[string]bool
	sqlTaintedVars          map[string]int
	sqlBuilderVars          map[string]int
	sqlBuilderFuncs         map[string]bool
	literalStringVars       map[string]bool
}

func astFindings(pd ParsedDiff) []Finding {
	var findings []Finding
	for _, h := range pd.Hunks {
		if !strings.HasSuffix(h.File, ".go") {
			continue
		}
		parsed, ok := parseHunkAST(h)
		if !ok {
			continue
		}
		facts := collectASTFacts(parsed)
		hasClose := len(facts.closeTargets) > 0 || astHasSelector(parsed.file, "Close")
		hasCommit := len(facts.commitTargets) > 0 || astHasSelector(parsed.file, "Commit")
		hasRollback := len(facts.rollbackTargets) > 0 || astHasSelector(parsed.file, "Rollback")
		hasContext := strings.Contains(parsed.source, "ctx") ||
			strings.Contains(parsed.source, "context.") ||
			strings.Contains(parsed.source, ".Done()")
		ast.Inspect(parsed.file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				line := parsed.actualLine(node.Pos())
				if parsed.addedNewLine[line] && !hasContext {
					findings = append(findings, Finding{
						Severity:       SeverityHigh,
						Category:       "concurrency",
						File:           h.File,
						Line:           line,
						Title:          "Goroutine has no AST-visible cancellation path",
						Evidence:       parsed.textByLine[line],
						Recommendation: "Thread context or another bounded shutdown signal into the goroutine and exit on cancellation.",
						Confidence:     0.86,
						Source:         "ast",
						RuleID:         "go/concurrency/goroutine-context",
					})
				}
			case *ast.CallExpr:
				line := parsed.actualLine(node.Pos())
				if !parsed.addedNewLine[line] {
					return true
				}
				call := selectorName(node.Fun)
				if isResourceOpenCall(call, parsed.source) && !callResultLooksClosed(node, facts, false) && !hasClose {
					title, recommendation, ruleID := resourceCloseFindingDetails(call, parsed.source)
					findings = append(findings, Finding{
						Severity:       SeverityHigh,
						Category:       "resource_lifecycle",
						File:           h.File,
						Line:           line,
						Title:          title,
						Evidence:       parsed.textByLine[line],
						Recommendation: recommendation,
						Confidence:     0.84,
						Source:         "ast",
						RuleID:         ruleID,
					})
				}
				if isHTTPWrapperCall(call, parsed.source) && !callResultLooksClosed(node, facts, true) {
					findings = append(findings, Finding{
						Severity:       SeverityMedium,
						Category:       "resource_lifecycle",
						File:           h.File,
						Line:           line,
						Title:          "HTTP-like wrapper response is not visibly closed",
						Evidence:       parsed.textByLine[line],
						Recommendation: "If this call returns an HTTP response, close resp.Body with defer after checking errors and nil responses.",
						Confidence:     0.69,
						Source:         "ast",
						RuleID:         "go/resource/http-body-close",
					})
				}
				if isSQLExecutionCall(call) && sqlExecutionArgsLookTainted(node.Args, facts) {
					findings = append(findings, Finding{
						Severity:       SeverityHigh,
						Category:       "security",
						File:           h.File,
						Line:           line,
						Title:          "SQL query is built through concatenation or a query helper",
						Evidence:       parsed.textByLine[line],
						Recommendation: "Use parameterized queries and avoid helper functions that splice user-controlled values into SQL text.",
						Confidence:     0.78,
						Source:         "ast",
						RuleID:         "go/security/sql-concat",
					})
				}
				if isTransactionBeginCall(call) && !transactionResultIsClosed(node, facts) && !hasCommit && !hasRollback {
					findings = append(findings, Finding{
						Severity:       SeverityHigh,
						Category:       "database_lifecycle",
						File:           h.File,
						Line:           line,
						Title:          "Transaction begin has no parsed commit or rollback",
						Evidence:       parsed.textByLine[line],
						Recommendation: "Defer tx.Rollback() after Begin/BeginTx and commit only after all operations succeed.",
						Confidence:     0.86,
						Source:         "ast",
						RuleID:         "go/db/transaction-lifecycle",
					})
				}
			case *ast.AssignStmt:
				line := parsed.actualLine(node.Pos())
				if parsed.addedNewLine[line] && assignmentIgnoresError(node) {
					findings = append(findings, Finding{
						Severity:       SeverityMedium,
						Category:       "error_handling",
						File:           h.File,
						Line:           line,
						Title:          "Error result is assigned to blank identifier",
						Evidence:       parsed.textByLine[line],
						Recommendation: "Handle the error explicitly, return it with context, or document why it is safe to ignore.",
						Confidence:     0.80,
						Source:         "ast",
						RuleID:         "go/error/ignored-error",
					})
				}
				findings = append(findings, assignmentLifecycleFindings(h.File, parsed, facts, node)...)
			}
			return true
		})
	}
	return findings
}

func collectASTFacts(parsed parsedHunkAST) astFacts {
	facts := astFacts{
		closeTargets:            map[string]bool{},
		conditionalCloseTargets: map[string]bool{},
		bodyCloseTargets:        map[string]bool{},
		commitTargets:           map[string]bool{},
		rollbackTargets:         map[string]bool{},
		conditionalRollback:     map[string]bool{},
		cancelTargets:           map[string]bool{},
		sqlTaintedVars:          map[string]int{},
		sqlBuilderVars:          map[string]int{},
		sqlBuilderFuncs:         map[string]bool{},
		literalStringVars:       map[string]bool{},
	}
	for _, decl := range parsed.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name == nil {
			continue
		}
		if functionReturnsTaintedSQL(fn, facts) {
			facts.sqlBuilderFuncs[fn.Name.Name] = true
		}
	}
	ast.Inspect(parsed.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.IfStmt:
			recordConditionalLifecycleFacts(facts, node.Body)
			recordConditionalLifecycleFacts(facts, node.Else)
		case *ast.SwitchStmt:
			recordConditionalLifecycleFacts(facts, node.Body)
		case *ast.CallExpr:
			call := selectorName(node.Fun)
			receiver := selectorReceiver(node.Fun)
			if _, tracked := facts.cancelTargets[call]; tracked {
				facts.cancelTargets[call] = true
			}
			switch {
			case call == "Close" || strings.HasSuffix(call, ".Close"):
				facts.closeTargets[receiver] = true
				if strings.HasSuffix(receiver, ".Body") {
					facts.bodyCloseTargets[strings.TrimSuffix(receiver, ".Body")] = true
				}
			case call == "Commit" || strings.HasSuffix(call, ".Commit"):
				facts.commitTargets[receiver] = true
			case call == "Rollback" || strings.HasSuffix(call, ".Rollback"):
				facts.rollbackTargets[receiver] = true
			case call == "WriteString" || strings.HasSuffix(call, ".WriteString"):
				if receiver != "" && len(node.Args) > 0 && !exprIsStringLiteral(node.Args[0]) {
					facts.sqlBuilderVars[receiver] = parsed.actualLine(node.Pos())
				}
			}
		case *ast.AssignStmt:
			recordAssignmentFacts(parsed, facts, node)
		case *ast.ValueSpec:
			recordValueSpecFacts(parsed, facts, node)
		}
		return true
	})
	return facts
}

func recordConditionalLifecycleFacts(facts astFacts, node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		call := selectorName(callExpr.Fun)
		receiver := selectorReceiver(callExpr.Fun)
		switch {
		case call == "Close" || strings.HasSuffix(call, ".Close"):
			facts.conditionalCloseTargets[receiver] = true
			if strings.HasSuffix(receiver, ".Body") {
				facts.conditionalCloseTargets[strings.TrimSuffix(receiver, ".Body")] = true
			}
		case call == "Rollback" || strings.HasSuffix(call, ".Rollback"):
			facts.conditionalRollback[receiver] = true
		}
		return true
	})
}

func functionReturnsTaintedSQL(fn *ast.FuncDecl, facts astFacts) bool {
	local := facts
	local.sqlTaintedVars = copyIntMap(facts.sqlTaintedVars)
	local.sqlBuilderVars = copyIntMap(facts.sqlBuilderVars)
	local.literalStringVars = copyBoolMap(facts.literalStringVars)
	var tainted bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			recordAssignmentFacts(parsedHunkAST{}, local, node)
		case *ast.ReturnStmt:
			for _, expr := range node.Results {
				if expressionIsSQLTainted(expr, local) {
					tainted = true
					return false
				}
			}
		}
		return !tainted
	})
	return tainted
}

func recordAssignmentFacts(parsed parsedHunkAST, facts astFacts, assign *ast.AssignStmt) {
	for i, rhs := range assign.Rhs {
		line := parsed.actualLine(rhs.Pos())
		if line == 0 && len(assign.Lhs) > 0 {
			line = parsed.actualLine(assign.Lhs[0].Pos())
		}
		for _, name := range lhsNamesAt(assign.Lhs, i) {
			if exprIsStringLiteral(rhs) {
				facts.literalStringVars[name] = true
			}
			if expressionIsSQLTainted(rhs, facts) {
				facts.sqlTaintedVars[name] = line
			}
			if exprIsStringBuilder(rhs) {
				facts.sqlBuilderVars[name] = line
			}
			if call, ok := rhs.(*ast.CallExpr); ok && isContextDeriveCall(selectorName(call.Fun)) {
				// The cancel function is conventionally the second result.
				if i == 0 && len(assign.Lhs) > 1 {
					if id, ok := assign.Lhs[1].(*ast.Ident); ok && id.Name != "_" {
						facts.cancelTargets[id.Name] = false
					}
				}
			}
		}
	}
	for _, lhs := range assign.Lhs {
		if id, ok := lhs.(*ast.Ident); ok {
			if _, tracked := facts.cancelTargets[id.Name]; tracked {
				facts.cancelTargets[id.Name] = false
			}
		}
	}
}

func recordValueSpecFacts(parsed parsedHunkAST, facts astFacts, spec *ast.ValueSpec) {
	for i, value := range spec.Values {
		if i >= len(spec.Names) || spec.Names[i] == nil {
			continue
		}
		name := spec.Names[i].Name
		if exprIsStringLiteral(value) {
			facts.literalStringVars[name] = true
		}
		if expressionIsSQLTainted(value, facts) {
			facts.sqlTaintedVars[name] = parsed.actualLine(value.Pos())
		}
		if exprIsStringBuilder(value) || typeExprString(spec.Type) == "strings.Builder" {
			facts.sqlBuilderVars[name] = parsed.actualLine(value.Pos())
		}
	}
}

func assignmentLifecycleFindings(file string, parsed parsedHunkAST, facts astFacts, assign *ast.AssignStmt) []Finding {
	var findings []Finding
	for i, rhs := range assign.Rhs {
		call, ok := rhs.(*ast.CallExpr)
		if !ok {
			continue
		}
		line := parsed.actualLine(call.Pos())
		if line == 0 || !parsed.addedNewLine[line] {
			continue
		}
		callName := selectorName(call.Fun)
		names := lhsNamesAt(assign.Lhs, i)
		firstName := firstName(names)
		if firstName == "" && i == 0 && len(assign.Lhs) > 0 {
			firstName = exprString(assign.Lhs[0])
		}
		switch {
		case isContextDeriveCall(callName) && !contextCancelIsCalled(assign, facts):
			findings = append(findings, Finding{
				Severity:       SeverityMedium,
				Category:       "context",
				File:           file,
				Line:           line,
				Title:          "Derived context cancel function is not called",
				Evidence:       parsed.textByLine[line],
				Recommendation: "Capture and call the cancel function, usually with defer cancel(), to release timers and child context resources.",
				Confidence:     0.82,
				Source:         "ast",
				RuleID:         "go/context/missing-cancel",
			})
		case isRowsQueryCall(callName) && firstName != "" &&
			(!facts.closeTargets[firstName] || facts.conditionalCloseTargets[firstName]):
			findings = append(findings, Finding{
				Severity:       SeverityHigh,
				Category:       "database_lifecycle",
				File:           file,
				Line:           line,
				Title:          "SQL rows are not closed in the parsed change",
				Evidence:       parsed.textByLine[line],
				Recommendation: "Defer rows.Close() after checking the query error and inspect rows.Err() after iteration.",
				Confidence:     0.84,
				Source:         "ast",
				RuleID:         "go/db/rows-close",
			})
		case resourceResultNeedsClose(callName, firstName, facts) && !isHTTPWrapperCall(callName, parsed.source):
			findings = append(findings, Finding{
				Severity:       SeverityMedium,
				Category:       "resource_lifecycle",
				File:           file,
				Line:           line,
				Title:          "Resource-like wrapper result is not visibly closed",
				Evidence:       parsed.textByLine[line],
				Recommendation: "If this wrapper returns a file, connection, listener, or body, close it with defer after checking the error.",
				Confidence:     0.69,
				Source:         "ast",
				RuleID:         "go/resource/missing-close",
			})
		case isTransactionBeginCall(callName) && firstName != "" &&
			(!facts.rollbackTargets[firstName] || facts.conditionalRollback[firstName]):
			findings = append(findings, Finding{
				Severity:       SeverityHigh,
				Category:       "database_lifecycle",
				File:           file,
				Line:           line,
				Title:          "Transaction rollback is missing or conditional",
				Evidence:       parsed.textByLine[line],
				Recommendation: "Defer tx.Rollback() unconditionally after Begin/BeginTx, then commit only after all operations succeed.",
				Confidence:     0.82,
				Source:         "ast",
				RuleID:         "go/db/transaction-lifecycle",
			})
		}
	}
	return findings
}

func parseHunkAST(h DiffHunk) (parsedHunkAST, bool) {
	lines := make([]string, 0, len(h.Lines))
	lineMap := map[int]int{}
	added := map[int]bool{}
	textByLine := map[int]string{}
	for _, l := range h.Lines {
		if l.Kind == '-' {
			continue
		}
		lines = append(lines, l.Text)
		sourceLine := len(lines)
		lineMap[sourceLine] = l.NewLine
		if l.Kind == '+' {
			added[l.NewLine] = true
			textByLine[l.NewLine] = redactSecrets(strings.TrimSpace(l.Text))
		}
	}
	raw := strings.Join(lines, "\n")
	variants := []struct {
		source string
		offset int
	}{
		{source: raw, offset: 0},
		{source: "package review\n" + raw, offset: 1},
		{source: "package review\nfunc _() {\n" + raw + "\n}", offset: 2},
	}
	for _, variant := range variants {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, h.File, variant.source, parser.AllErrors)
		if err != nil || file == nil {
			continue
		}
		adjusted := map[int]int{}
		for sourceLine, newLine := range lineMap {
			adjusted[sourceLine+variant.offset] = newLine
		}
		return parsedHunkAST{
			file:         file,
			fset:         fset,
			lineMap:      adjusted,
			addedNewLine: added,
			textByLine:   textByLine,
			source:       raw,
		}, true
	}
	return parsedHunkAST{}, false
}

func (p parsedHunkAST) actualLine(pos token.Pos) int {
	if p.fset == nil || pos == token.NoPos {
		return 0
	}
	line := p.fset.Position(pos).Line
	if mapped, ok := p.lineMap[line]; ok {
		return mapped
	}
	return 0
}

func astHasSelector(file *ast.File, name string) bool {
	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func selectorName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			return id.Name + "." + v.Sel.Name
		}
		return v.Sel.Name
	case *ast.Ident:
		return v.Name
	default:
		return ""
	}
}

func isResourceOpenCall(call string, source string) bool {
	switch call {
	case "os.Open", "os.OpenFile", "os.Create", "os.CreateTemp", "ioutil.TempFile", "sql.Open", "sql.OpenDB", "http.Get", "http.Post", "net.Dial", "net.Listen", "template.ParseFiles":
		return true
	default:
		return (call == "Do" || strings.HasSuffix(call, ".Do")) && hunkMentionsHTTP(source)
	}
}

func resourceCloseFindingDetails(call string, source string) (string, string, string) {
	if (call == "Do" || strings.HasSuffix(call, ".Do")) && hunkMentionsHTTP(source) {
		return "HTTP response body is not closed in the parsed change",
			"Close resp.Body with defer after checking the request error and nil response.",
			"go/resource/http-body-close"
	}
	return "Opened resource is not closed in the parsed change",
		"Close files, response bodies, rows, or DB handles with defer after checking the open error.",
		"go/resource/missing-close"
}

func isTransactionBeginCall(call string) bool {
	return strings.HasSuffix(call, ".Begin") ||
		strings.HasSuffix(call, ".BeginTx") ||
		call == "Begin" ||
		call == "BeginTx"
}

func assignmentIgnoresError(assign *ast.AssignStmt) bool {
	if len(assign.Lhs) == 0 {
		return false
	}
	for i, lhs := range assign.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Name != "_" {
			continue
		}
		if i < len(assign.Rhs) && expressionLooksError(assign.Rhs[i]) {
			return true
		}
		if len(assign.Lhs) == 2 && i == 1 {
			return true
		}
	}
	return false
}

func expressionLooksError(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return strings.Contains(strings.ToLower(v.Name), "err")
	case *ast.CallExpr:
		return false
	default:
		return false
	}
}

func callResultLooksClosed(call *ast.CallExpr, facts astFacts, body bool) bool {
	parent := enclosingAssign(call)
	if parent == nil {
		return false
	}
	for i, rhs := range parent.Rhs {
		if rhs != call {
			continue
		}
		for _, name := range lhsNamesAt(parent.Lhs, i) {
			if body && facts.bodyCloseTargets[name] {
				return true
			}
			if !body && facts.closeTargets[name] {
				return true
			}
		}
	}
	return false
}

func transactionResultIsClosed(call *ast.CallExpr, facts astFacts) bool {
	parent := enclosingAssign(call)
	if parent == nil {
		return false
	}
	for i, rhs := range parent.Rhs {
		if rhs != call {
			continue
		}
		for _, name := range lhsNamesAt(parent.Lhs, i) {
			if facts.commitTargets[name] || facts.rollbackTargets[name] {
				return true
			}
		}
	}
	return false
}

// enclosingAssign is intentionally conservative. Parent pointers are not
// available in go/ast, so callers that need precise assignment data use
// assignmentLifecycleFindings. This helper returns nil to avoid pretending
// to know a parent assignment from a raw CallExpr.
func enclosingAssign(*ast.CallExpr) *ast.AssignStmt {
	return nil
}

func contextCancelIsCalled(assign *ast.AssignStmt, facts astFacts) bool {
	if len(assign.Lhs) < 2 {
		return false
	}
	id, ok := assign.Lhs[1].(*ast.Ident)
	if !ok || id.Name == "_" {
		return false
	}
	return facts.cancelTargets[id.Name]
}

func lhsNamesAt(lhs []ast.Expr, rhsIndex int) []string {
	if len(lhs) == 0 {
		return nil
	}
	if len(lhs) == 1 || rhsIndex >= len(lhs) {
		if name := exprString(lhs[0]); name != "" {
			return []string{name}
		}
		return nil
	}
	if name := exprString(lhs[rhsIndex]); name != "" {
		return []string{name}
	}
	return nil
}

func firstName(names []string) string {
	for _, name := range names {
		if name != "" && name != "_" {
			return name
		}
	}
	return ""
}

func selectorReceiver(expr ast.Expr) string {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return exprString(sel.X)
	}
	return ""
}

func exprString(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		left := exprString(v.X)
		if left == "" {
			return v.Sel.Name
		}
		return left + "." + v.Sel.Name
	default:
		return ""
	}
}

func typeExprString(expr ast.Expr) string {
	return exprString(expr)
}

func exprIsStringLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func exprIsStringBuilder(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.CompositeLit:
		return typeExprString(v.Type) == "strings.Builder"
	default:
		return false
	}
}

func expressionIsSQLTainted(expr ast.Expr, facts astFacts) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return facts.sqlTaintedVars[v.Name] > 0
	case *ast.SelectorExpr:
		return facts.sqlTaintedVars[exprString(v)] > 0
	case *ast.BinaryExpr:
		if v.Op.String() == "+" && (expressionLooksSQLRelated(v.X, facts) || expressionLooksSQLRelated(v.Y, facts)) {
			return true
		}
	case *ast.CallExpr:
		call := selectorName(v.Fun)
		if call == "fmt.Sprintf" || strings.HasSuffix(call, ".Sprintf") {
			return len(v.Args) > 0 && expressionLooksSQLRelated(v.Args[0], facts)
		}
		if call == "strings.Join" || strings.HasSuffix(call, ".Join") {
			return len(v.Args) > 0 && expressionLooksSQLRelated(v.Args[0], facts)
		}
		if call == "String" || strings.HasSuffix(call, ".String") {
			return facts.sqlBuilderVars[selectorReceiver(v.Fun)] > 0
		}
		if facts.sqlBuilderFuncs[call] {
			return true
		}
		if callLooksLikeSQLBuilder(call) && callHasDynamicArgs(v.Args, facts) {
			return true
		}
	case *ast.ParenExpr:
		return expressionIsSQLTainted(v.X, facts)
	}
	return false
}

func expressionLooksSQLRelated(expr ast.Expr, facts astFacts) bool {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return v.Kind == token.STRING && literalLooksSQL(v.Value)
	case *ast.Ident:
		return facts.sqlTaintedVars[v.Name] > 0 || strings.Contains(strings.ToLower(v.Name), "sql") || strings.Contains(strings.ToLower(v.Name), "query")
	case *ast.SelectorExpr:
		name := strings.ToLower(exprString(v))
		return facts.sqlTaintedVars[exprString(v)] > 0 || strings.Contains(name, "sql") || strings.Contains(name, "query")
	case *ast.BinaryExpr:
		return expressionLooksSQLRelated(v.X, facts) || expressionLooksSQLRelated(v.Y, facts)
	case *ast.CallExpr:
		return expressionIsSQLTainted(v, facts)
	case *ast.CompositeLit:
		for _, elt := range v.Elts {
			if expressionLooksSQLRelated(elt, facts) {
				return true
			}
		}
	}
	return false
}

func literalLooksSQL(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range []string{"select ", "insert ", "update ", "delete ", " from ", " where "} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func callLooksLikeSQLBuilder(call string) bool {
	lower := strings.ToLower(call)
	return strings.Contains(lower, "query") ||
		strings.Contains(lower, "sql") ||
		strings.Contains(lower, "statement")
}

func callHasDynamicArgs(args []ast.Expr, facts astFacts) bool {
	for _, arg := range args {
		switch v := arg.(type) {
		case *ast.BasicLit:
			continue
		case *ast.Ident:
			if facts.literalStringVars[v.Name] {
				continue
			}
		}
		return true
	}
	return false
}

func sqlExecutionArgsLookTainted(args []ast.Expr, facts astFacts) bool {
	for _, arg := range args {
		if expressionIsSQLTainted(arg, facts) {
			return true
		}
		if call, ok := arg.(*ast.CallExpr); ok && facts.sqlBuilderFuncs[selectorName(call.Fun)] {
			return true
		}
	}
	return false
}

func isSQLExecutionCall(call string) bool {
	return strings.HasSuffix(call, ".Query") ||
		strings.HasSuffix(call, ".Exec") ||
		strings.HasSuffix(call, ".QueryRow") ||
		strings.HasSuffix(call, ".QueryContext") ||
		strings.HasSuffix(call, ".ExecContext") ||
		strings.HasSuffix(call, ".QueryRowContext")
}

func isRowsQueryCall(call string) bool {
	return strings.HasSuffix(call, ".Query") || strings.HasSuffix(call, ".QueryContext")
}

func isContextDeriveCall(call string) bool {
	return call == "context.WithCancel" ||
		call == "context.WithTimeout" ||
		call == "context.WithDeadline" ||
		strings.HasSuffix(call, ".WithCancel") ||
		strings.HasSuffix(call, ".WithTimeout") ||
		strings.HasSuffix(call, ".WithDeadline")
}

func isHTTPWrapperCall(call string, source string) bool {
	if !hunkMentionsHTTP(source) {
		return false
	}
	lower := strings.ToLower(call)
	if strings.Contains(lower, "newrequest") || strings.Contains(lower, "requestwithcontext") {
		return false
	}
	for _, token := range []string{"do", "fetch", "request", "execute", "roundtrip"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func isResourceWrapperCall(call string) bool {
	lower := strings.ToLower(call)
	if strings.Contains(lower, ".") {
		parts := strings.Split(lower, ".")
		lower = parts[len(parts)-1]
	}
	for _, token := range []string{"open", "create", "dial", "listen", "connect", "reader", "writer"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func resourceResultNeedsClose(callName, resultName string, facts astFacts) bool {
	if resultName == "" || facts.closeTargets[resultName] && !facts.conditionalCloseTargets[resultName] {
		return false
	}
	return isResourceWrapperCall(callName) || resultNameLooksClosable(resultName)
}

func resultNameLooksClosable(name string) bool {
	lower := strings.ToLower(name)
	for _, token := range []string{
		"file", "conn", "listener", "body", "reader", "writer",
		"rows", "closer", "handle", "stream", "socket", "resp",
	} {
		if lower == token || strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func copyIntMap(in map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyBoolMap(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
