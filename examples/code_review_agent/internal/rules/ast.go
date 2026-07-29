//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func runASTRules(fileSet *token.FileSet, unit *sourceUnit) []findings.Candidate {
	candidates := secretCandidates(fileSet, unit)
	functions := declaredFunctions(unit.parsed)
	candidates = append(candidates, goroutineCandidates(fileSet, unit, functions)...)
	for _, declaration := range unit.parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		locals := localBindings(function)
		types := parameterTypes(function, unit.imports)
		walkStatementLists(function.Body.List, func(statements []ast.Stmt) {
			candidates = append(candidates, cleanupCandidates(
				fileSet,
				unit,
				function,
				locals,
				types,
				statements,
			)...)
		})
		candidates = append(candidates, ignoredErrorCandidates(fileSet, unit, function, locals)...)
		candidates = append(candidates, commandCandidates(fileSet, unit, function, locals)...)
	}
	return candidates
}

func cleanupCandidates(
	fileSet *token.FileSet,
	unit *sourceUnit,
	function *ast.FuncDecl,
	locals map[string]bool,
	types map[string]qualifiedType,
	statements []ast.Stmt,
) []findings.Candidate {
	candidates := make([]findings.Candidate, 0)
	for index, statement := range statements {
		assignment, call, ok := assignmentCall(statement)
		if !ok {
			continue
		}
		if kind := acquiredResource(call, assignment, unit, function, locals, types); kind != "" {
			line, added := positionAdded(fileSet, call.Fun.Pos(), unit.added)
			if added && !hasDirectResourceCleanup(statements, index, assignment, kind) {
				resource, _ := assignment.Lhs[0].(*ast.Ident)
				candidates = append(candidates, newCandidate(
					RuleResourceClose,
					review.SeverityMedium,
					review.ConfidenceHigh,
					"resource",
					unit.changedFile,
					line,
					stableAnchor("res", functionAnchor(function), resource.Name, kind),
					fmt.Sprintf("%s lacks direct deferred cleanup", kind),
					fmt.Sprintf("An added %s acquisition is not followed by an unconditional direct defer in the same lexical block.", kind),
					"Check the acquisition error, then directly defer the matching Close call before other work.",
				))
			}
		}
		if contextCancelName(call, assignment, unit, function, locals) != "" {
			line, added := positionAdded(fileSet, call.Fun.Pos(), unit.added)
			cancel := contextCancelName(call, assignment, unit, function, locals)
			if added && !hasDirectCancel(statements, index, cancel) {
				candidates = append(candidates, newCandidate(
					RuleGoroutineLifetime,
					review.SeverityMedium,
					review.ConfidenceHigh,
					"concurrency",
					unit.changedFile,
					line,
					stableAnchor("ctx", functionAnchor(function), cancel),
					"Derived context cancellation is not directly deferred",
					"An added context constructor is not immediately followed by an unconditional direct defer of its cancellation function in the same lexical block.",
					"Retain the cancellation function and directly defer it immediately after creating the derived context.",
				))
			}
		}
		if transactionName(call, assignment, unit, function, locals, types) != "" {
			line, added := positionAdded(fileSet, call.Fun.Pos(), unit.added)
			transaction := transactionName(call, assignment, unit, function, locals, types)
			if added && !hasTransactionLifecycle(statements, index, assignment, transaction) {
				candidates = append(candidates, newCandidate(
					RuleTransactionLifecycle,
					review.SeverityHigh,
					review.ConfidenceHigh,
					"transaction",
					unit.changedFile,
					line,
					stableAnchor("txn", functionAnchor(function), transaction),
					"SQL transaction lifecycle is incomplete",
					"An added transaction lacks direct deferred rollback after its success check or a credible direct commit after begin.",
					"Check Begin errors, directly defer Rollback, and Commit on the successful path in the same lexical block.",
				))
			}
		}
	}
	return candidates
}

func walkStatementLists(statements []ast.Stmt, visit func([]ast.Stmt)) {
	visit(statements)
	for _, statement := range statements {
		switch value := statement.(type) {
		case *ast.BlockStmt:
			walkStatementLists(value.List, visit)
		case *ast.IfStmt:
			walkStatementLists(value.Body.List, visit)
			if value.Else != nil {
				walkNestedStatement(value.Else, visit)
			}
		case *ast.ForStmt:
			walkStatementLists(value.Body.List, visit)
		case *ast.RangeStmt:
			walkStatementLists(value.Body.List, visit)
		case *ast.SwitchStmt:
			walkClauseLists(value.Body.List, visit)
		case *ast.TypeSwitchStmt:
			walkClauseLists(value.Body.List, visit)
		case *ast.SelectStmt:
			walkClauseLists(value.Body.List, visit)
		}
	}
}

func walkNestedStatement(statement ast.Stmt, visit func([]ast.Stmt)) {
	switch value := statement.(type) {
	case *ast.BlockStmt:
		walkStatementLists(value.List, visit)
	case *ast.IfStmt:
		walkStatementLists([]ast.Stmt{value}, visit)
	}
}

func walkClauseLists(clauses []ast.Stmt, visit func([]ast.Stmt)) {
	for _, statement := range clauses {
		switch clause := statement.(type) {
		case *ast.CaseClause:
			walkStatementLists(clause.Body, visit)
		case *ast.CommClause:
			walkStatementLists(clause.Body, visit)
		}
	}
}

func assignmentCall(statement ast.Stmt) (*ast.AssignStmt, *ast.CallExpr, bool) {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Rhs) != 1 {
		return nil, nil, false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	return assignment, call, ok
}

func acquiredResource(
	call *ast.CallExpr,
	assignment *ast.AssignStmt,
	unit *sourceUnit,
	function *ast.FuncDecl,
	locals map[string]bool,
	types map[string]qualifiedType,
) string {
	if len(assignment.Lhs) < 2 {
		return ""
	}
	resource, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok || resource.Name == "_" {
		return ""
	}
	if packageFunction(call, unit.imports, locals, "os", "Open", "OpenFile", "Create", "CreateTemp") {
		return "file"
	}
	if packageFunction(call, unit.imports, locals, "net/http", "Get", "Post", "PostForm") {
		return "HTTP response body"
	}
	if typedMethod(call, types, "database/sql", "DB", "Query", "QueryContext") {
		return "SQL rows"
	}
	if typedMethod(call, types, "net/http", "Client", "Do") {
		return "HTTP response body"
	}
	_ = function
	return ""
}

func hasDirectResourceCleanup(
	statements []ast.Stmt,
	acquireIndex int,
	assignment *ast.AssignStmt,
	kind string,
) bool {
	resource, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	errorName := assignedIdentifier(assignment, 1)
	guardIndex := acquireIndex + 1
	if errorName == "" || guardIndex >= len(statements) ||
		!successfulErrorGuard(statements[guardIndex], errorName) {
		return false
	}
	cleanupIndex := guardIndex + 1
	if cleanupIndex >= len(statements) {
		return false
	}
	if kind == "HTTP response body" {
		return directDeferredMethod(statements[cleanupIndex], resource.Name, "Body", "Close")
	}
	return directDeferredMethod(statements[cleanupIndex], resource.Name, "Close")
}

func contextCancelName(
	call *ast.CallExpr,
	assignment *ast.AssignStmt,
	unit *sourceUnit,
	function *ast.FuncDecl,
	locals map[string]bool,
) string {
	if len(assignment.Lhs) < 2 || !packageFunction(
		call,
		unit.imports,
		locals,
		"context",
		"WithCancel",
		"WithCancelCause",
		"WithDeadline",
		"WithTimeout",
	) {
		return ""
	}
	_ = function
	return assignedIdentifier(assignment, 1)
}

func hasDirectCancel(statements []ast.Stmt, acquireIndex int, cancel string) bool {
	if cancel == "" || cancel == "_" || acquireIndex+1 >= len(statements) {
		return false
	}
	deferStatement, ok := statements[acquireIndex+1].(*ast.DeferStmt)
	if !ok || len(deferStatement.Call.Args) != 0 {
		return false
	}
	identifier, ok := deferStatement.Call.Fun.(*ast.Ident)
	return ok && identifier.Name == cancel
}

func transactionName(
	call *ast.CallExpr,
	assignment *ast.AssignStmt,
	unit *sourceUnit,
	function *ast.FuncDecl,
	locals map[string]bool,
	types map[string]qualifiedType,
) string {
	if len(assignment.Lhs) < 2 || !typedMethod(call, types, "database/sql", "DB", "Begin", "BeginTx") {
		return ""
	}
	_ = unit
	_ = function
	_ = locals
	return assignedIdentifier(assignment, 0)
}

func hasTransactionLifecycle(
	statements []ast.Stmt,
	beginIndex int,
	assignment *ast.AssignStmt,
	transaction string,
) bool {
	if transaction == "" || transaction == "_" {
		return false
	}
	errorName := assignedIdentifier(assignment, 1)
	guardIndex := beginIndex + 1
	if errorName == "" || guardIndex >= len(statements) ||
		!successfulErrorGuard(statements[guardIndex], errorName) {
		return false
	}
	rollbackIndex := guardIndex + 1
	if rollbackIndex >= len(statements) ||
		!directDeferredMethod(statements[rollbackIndex], transaction, "Rollback") {
		return false
	}
	for index := rollbackIndex + 1; index < len(statements); index++ {
		statement := statements[index]
		if statementRebinds(statement, transaction) {
			return false
		}
		if credibleCommit(statements, index, transaction) {
			return true
		}
	}
	return false
}

func successfulErrorGuard(statement ast.Stmt, errorName string) bool {
	ifStatement, ok := statement.(*ast.IfStmt)
	if !ok || ifStatement.Init != nil || ifStatement.Else != nil ||
		!isNotNilCondition(ifStatement.Cond, errorName) {
		return false
	}
	return blockTerminates(ifStatement.Body.List)
}

func isNotNilCondition(expression ast.Expr, identifier string) bool {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.NEQ {
		return false
	}
	left, leftOK := binary.X.(*ast.Ident)
	right, rightOK := binary.Y.(*ast.Ident)
	return leftOK && rightOK &&
		((left.Name == identifier && right.Name == "nil") ||
			(right.Name == identifier && left.Name == "nil"))
}

func blockTerminates(statements []ast.Stmt) bool {
	if len(statements) == 0 {
		return false
	}
	switch statements[len(statements)-1].(type) {
	case *ast.ReturnStmt, *ast.BranchStmt:
		return true
	default:
		return false
	}
}

func directDeferredMethod(statement ast.Stmt, root string, selectors ...string) bool {
	deferStatement, ok := statement.(*ast.DeferStmt)
	if !ok || len(deferStatement.Call.Args) != 0 {
		return false
	}
	return selectorChain(deferStatement.Call.Fun, root, selectors...)
}

func selectorChain(expression ast.Expr, root string, selectors ...string) bool {
	for index := len(selectors) - 1; index >= 0; index-- {
		selector, ok := expression.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != selectors[index] {
			return false
		}
		expression = selector.X
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == root
}

func credibleCommit(statements []ast.Stmt, index int, transaction string) bool {
	statement := statements[index]
	switch value := statement.(type) {
	case *ast.ReturnStmt:
		for _, result := range value.Results {
			if callIsMethod(result, transaction, "Commit") {
				return true
			}
		}
	case *ast.AssignStmt:
		if len(value.Lhs) != 1 || len(value.Rhs) != 1 ||
			!callIsMethod(value.Rhs[0], transaction, "Commit") {
			return false
		}
		errorName, ok := value.Lhs[0].(*ast.Ident)
		return ok && errorName.Name != "_" && index+1 < len(statements) &&
			successfulErrorGuard(statements[index+1], errorName.Name)
	case *ast.IfStmt:
		assignment, ok := value.Init.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 ||
			!callIsMethod(assignment.Rhs[0], transaction, "Commit") {
			return false
		}
		errorName, ok := assignment.Lhs[0].(*ast.Ident)
		return ok && errorName.Name != "_" && value.Else == nil &&
			isNotNilCondition(value.Cond, errorName.Name) && blockTerminates(value.Body.List)
	}
	return false
}

func callIsMethod(expression ast.Expr, root, method string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	return selectorChain(call.Fun, root, method)
}

func statementRebinds(statement ast.Stmt, identifier string) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok {
		return false
	}
	for _, expression := range assignment.Lhs {
		if name, ok := expression.(*ast.Ident); ok && name.Name == identifier {
			return true
		}
	}
	return false
}

func assignedIdentifier(assignment *ast.AssignStmt, index int) string {
	if index < 0 || index >= len(assignment.Lhs) {
		return ""
	}
	identifier, ok := assignment.Lhs[index].(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func goroutineCandidates(
	fileSet *token.FileSet,
	unit *sourceUnit,
	functions map[string]*ast.FuncDecl,
) []findings.Candidate {
	candidates := make([]findings.Candidate, 0)
	for _, declaration := range unit.parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		contextNames := contextIdentifiers(function, unit)
		inspectWithoutNestedFunctions(function.Body, func(node ast.Node) bool {
			statement, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}
			targetBody, targetName, targetContexts := goroutineTarget(
				statement,
				functions,
				contextNames,
				unit,
			)
			if targetBody == nil {
				return true
			}
			riskPosition, risky := unsafeGoroutinePosition(targetBody, targetContexts)
			if !risky {
				return true
			}
			line, added := positionAdded(fileSet, statement.Go, unit.added)
			if !added {
				line, added = positionAdded(fileSet, riskPosition, unit.added)
			}
			if !added {
				return true
			}
			candidates = append(candidates, newCandidate(
				RuleGoroutineLifetime,
				review.SeverityMedium,
				review.ConfidenceMedium,
				"concurrency",
				unit.changedFile,
				line,
				stableAnchor("go", functionAnchor(function), targetName),
				"Goroutine has no terminating context cancellation path",
				"An added goroutine can block or loop without receiving from a known context Done channel and returning.",
				"Receive from ctx.Done in the risky loop and terminate the goroutine with return.",
			))
			return true
		})
	}
	return candidates
}

func declaredFunctions(file *ast.File) map[string]*ast.FuncDecl {
	functions := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Body != nil {
			functions[function.Name.Name] = function
		}
	}
	return functions
}

func goroutineTarget(
	statement *ast.GoStmt,
	functions map[string]*ast.FuncDecl,
	contexts map[string]bool,
	unit *sourceUnit,
) (*ast.BlockStmt, string, map[string]bool) {
	switch target := statement.Call.Fun.(type) {
	case *ast.FuncLit:
		return target.Body, "literal", contexts
	case *ast.Ident:
		function := functions[target.Name]
		if function == nil {
			return nil, "", nil
		}
		return function.Body, target.Name, contextIdentifiers(function, unit)
	default:
		return nil, "", nil
	}
}

func unsafeGoroutinePosition(body *ast.BlockStmt, contexts map[string]bool) (token.Pos, bool) {
	var firstSend token.Pos
	var unsafeLoop token.Pos
	ast.Inspect(body, func(node ast.Node) bool {
		if unsafeLoop.IsValid() {
			return false
		}
		switch value := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.SendStmt:
			if !firstSend.IsValid() {
				firstSend = value.Arrow
			}
		case *ast.ForStmt:
			if value.Cond == nil && !hasTerminatingCancellation(value.Body, contexts) {
				unsafeLoop = value.For
				return false
			}
		}
		return true
	})
	if unsafeLoop.IsValid() {
		return unsafeLoop, true
	}
	if firstSend.IsValid() && !hasTerminatingCancellation(body, contexts) {
		return firstSend, true
	}
	return token.NoPos, false
}

func hasTerminatingCancellation(body *ast.BlockStmt, contexts map[string]bool) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		selectStatement, ok := node.(*ast.SelectStmt)
		if !ok {
			if _, nested := node.(*ast.FuncLit); nested {
				return false
			}
			return true
		}
		for _, item := range selectStatement.Body.List {
			clause, ok := item.(*ast.CommClause)
			if !ok || !doneReceive(clause.Comm, contexts) || !containsDirectReturn(clause.Body) {
				continue
			}
			found = true
			return false
		}
		return true
	})
	return found
}

func doneReceive(statement ast.Stmt, contexts map[string]bool) bool {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	receive, ok := expression.X.(*ast.UnaryExpr)
	if !ok || receive.Op != token.ARROW {
		return false
	}
	call, ok := receive.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, receiverOK := selector.X.(*ast.Ident)
	return receiverOK && selector.Sel.Name == "Done" && contexts[receiver.Name]
}

func containsDirectReturn(statements []ast.Stmt) bool {
	for _, statement := range statements {
		if _, ok := statement.(*ast.ReturnStmt); ok {
			return true
		}
	}
	return false
}

func contextIdentifiers(function *ast.FuncDecl, unit *sourceUnit) map[string]bool {
	contexts := make(map[string]bool)
	for name, qualified := range parameterTypes(function, unit.imports) {
		if qualified.path == "context" && qualified.name == "Context" {
			contexts[name] = true
		}
	}
	locals := localBindings(function)
	inspectWithoutNestedFunctions(function.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || len(assignment.Lhs) == 0 || len(assignment.Rhs) != 1 {
			return true
		}
		call, ok := assignment.Rhs[0].(*ast.CallExpr)
		if !ok || !packageFunction(
			call,
			unit.imports,
			locals,
			"context",
			"WithCancel",
			"WithCancelCause",
			"WithDeadline",
			"WithTimeout",
		) {
			return true
		}
		if identifier, ok := assignment.Lhs[0].(*ast.Ident); ok {
			contexts[identifier.Name] = true
		}
		return true
	})
	return contexts
}

func ignoredErrorCandidates(
	fileSet *token.FileSet,
	unit *sourceUnit,
	function *ast.FuncDecl,
	locals map[string]bool,
) []findings.Candidate {
	candidates := make([]findings.Candidate, 0)
	inspectWithoutNestedFunctions(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			if len(value.Rhs) != 1 {
				return true
			}
			call, ok := value.Rhs[0].(*ast.CallExpr)
			if !ok || !knownErrorCall(call, unit.imports, locals) {
				return true
			}
			blank := firstBlankIdentifier(value.Lhs)
			if blank == nil {
				return true
			}
			line, added := positionAdded(fileSet, blank.Pos(), unit.added)
			if !added {
				return true
			}
			candidates = append(candidates, ignoredErrorCandidate(unit, function, line, call))
		case *ast.ExprStmt:
			call, ok := value.X.(*ast.CallExpr)
			if !ok || !knownErrorCall(call, unit.imports, locals) {
				return true
			}
			line, added := positionAdded(fileSet, call.Fun.Pos(), unit.added)
			if added {
				candidates = append(candidates, ignoredErrorCandidate(unit, function, line, call))
			}
		}
		return true
	})
	return candidates
}

func ignoredErrorCandidate(
	unit *sourceUnit,
	function *ast.FuncDecl,
	line int,
	call *ast.CallExpr,
) findings.Candidate {
	return newCandidate(
		RuleIgnoredError,
		review.SeverityMedium,
		review.ConfidenceHigh,
		"correctness",
		unit.changedFile,
		line,
		stableAnchor("err", functionAnchor(function), selectorName(call.Fun)),
		"Error result is discarded",
		"An added call to a known error-returning API discards its result through a blank assignment or bare expression statement.",
		"Handle and propagate the error with enough context for the caller to act on it.",
	)
}

func firstBlankIdentifier(expressions []ast.Expr) *ast.Ident {
	for _, expression := range expressions {
		identifier, ok := expression.(*ast.Ident)
		if ok && identifier.Name == "_" {
			return identifier
		}
	}
	return nil
}

func knownErrorCall(call *ast.CallExpr, imports importResolver, locals map[string]bool) bool {
	return packageFunction(call, imports, locals, "encoding/json", "Marshal", "MarshalIndent", "Unmarshal") ||
		packageFunction(call, imports, locals, "os", "ReadFile", "WriteFile", "Readlink") ||
		packageFunction(call, imports, locals, "io", "Copy", "CopyBuffer", "ReadAll") ||
		packageFunction(call, imports, locals, "net/url", "Parse", "ParseRequestURI") ||
		packageFunction(call, imports, locals, "strconv", "Atoi", "ParseBool", "ParseFloat", "ParseInt", "ParseUint")
}

func commandCandidates(
	fileSet *token.FileSet,
	unit *sourceUnit,
	function *ast.FuncDecl,
	locals map[string]bool,
) []findings.Candidate {
	candidates := make([]findings.Candidate, 0)
	inspectWithoutNestedFunctions(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		offset := -1
		if packageFunction(call, unit.imports, locals, "os/exec", "Command") {
			offset = 0
		} else if packageFunction(call, unit.imports, locals, "os/exec", "CommandContext") {
			offset = 1
		}
		if offset < 0 || len(call.Args) < offset+3 || !shellArguments(call.Args[offset:]) {
			return true
		}
		line, added := positionAdded(fileSet, call.Args[offset+2].Pos(), unit.added)
		if !added {
			return true
		}
		candidates = append(candidates, newCandidate(
			RuleDangerousCommand,
			review.SeverityHigh,
			review.ConfidenceHigh,
			"security",
			unit.changedFile,
			line,
			stableAnchor("cmd", functionAnchor(function)),
			"Dynamic input is executed through a shell",
			"An added non-literal command string is passed to a shell interpreter.",
			"Avoid the shell and pass a fixed executable plus separately validated arguments to exec.Command.",
		))
		return true
	})
	return candidates
}

func shellArguments(arguments []ast.Expr) bool {
	program, ok := stringLiteral(arguments[0])
	if !ok {
		return false
	}
	flag, ok := stringLiteral(arguments[1])
	if !ok {
		return false
	}
	program = strings.ToLower(program)
	if separator := strings.LastIndexAny(program, `/\`); separator >= 0 {
		program = program[separator+1:]
	}
	validShell := program == "sh" || program == "bash" || program == "zsh" ||
		program == "cmd" || program == "cmd.exe" || program == "powershell" || program == "pwsh"
	validFlag := flag == "-c" || strings.EqualFold(flag, "/c") || strings.EqualFold(flag, "-command")
	if !validShell || !validFlag {
		return false
	}
	_, literal := stringLiteral(arguments[2])
	return !literal
}

func secretCandidates(fileSet *token.FileSet, unit *sourceUnit) []findings.Candidate {
	candidates := make([]findings.Candidate, 0)
	anchorCounts := make(map[string]int)
	emit := func(name string, expression ast.Expr) {
		if !sensitiveIdentifier(name) {
			return
		}
		literal, value, ok := secretLiteral(expression)
		if !ok || placeholderSecret(value) {
			return
		}
		line, added := positionAdded(fileSet, literal.Pos(), unit.added)
		if !added {
			return
		}
		base := normalizeAnchor(name)
		anchorCounts[base]++
		anchor := stableAnchor("lit", base, strconv.Itoa(anchorCounts[base]))
		candidates = append(candidates, newCandidate(
			RuleHardcodedSecret,
			review.SeverityHigh,
			review.ConfidenceHigh,
			"security",
			unit.changedFile,
			line,
			anchor,
			"Hard-coded credential in changed code",
			"An added string or byte-slice literal is assigned to a sensitive identifier; the value is omitted from evidence.",
			"Load the credential from an approved secret provider and rotate the exposed value.",
		))
	}
	ast.Inspect(unit.parsed, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.ValueSpec:
			for index, name := range value.Names {
				if index < len(value.Values) {
					emit(name.Name, value.Values[index])
				}
			}
		case *ast.AssignStmt:
			if len(value.Lhs) == len(value.Rhs) {
				for index, left := range value.Lhs {
					if name := assignedName(left); name != "" {
						emit(name, value.Rhs[index])
					}
				}
			}
		case *ast.KeyValueExpr:
			if name := mapKeyName(value.Key); name != "" {
				emit(name, value.Value)
			}
		}
		return true
	})
	return candidates
}

func assignedName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func mapKeyName(expression ast.Expr) string {
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name
	}
	value, ok := stringLiteral(expression)
	if ok {
		return value
	}
	return ""
}

func secretLiteral(expression ast.Expr) (*ast.BasicLit, string, bool) {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return secretLiteral(value.X)
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return nil, "", false
		}
		decoded, err := strconv.Unquote(value.Value)
		return value, decoded, err == nil
	case *ast.CallExpr:
		if len(value.Args) != 1 || !byteSliceConversion(value.Fun) {
			return nil, "", false
		}
		return secretLiteral(value.Args[0])
	default:
		return nil, "", false
	}
}

func byteSliceConversion(expression ast.Expr) bool {
	array, ok := expression.(*ast.ArrayType)
	if !ok || array.Len != nil {
		return false
	}
	element, ok := array.Elt.(*ast.Ident)
	return ok && element.Name == "byte"
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func packageFunction(
	call *ast.CallExpr,
	imports importResolver,
	locals map[string]bool,
	importPath string,
	functions ...string,
) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	root, ok := selector.X.(*ast.Ident)
	if !ok || locals[root.Name] || imports[root.Name] != importPath {
		return false
	}
	for _, function := range functions {
		if selector.Sel.Name == function {
			return true
		}
	}
	return false
}

type qualifiedType struct {
	path string
	name string
}

func parameterTypes(function *ast.FuncDecl, imports importResolver) map[string]qualifiedType {
	types := make(map[string]qualifiedType)
	collectFieldTypes(types, function.Recv, imports)
	collectFieldTypes(types, function.Type.Params, imports)
	return types
}

func collectFieldTypes(types map[string]qualifiedType, fields *ast.FieldList, imports importResolver) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		qualified, ok := resolveQualifiedType(field.Type, imports)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			types[name.Name] = qualified
		}
	}
}

func resolveQualifiedType(expression ast.Expr, imports importResolver) (qualifiedType, bool) {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return qualifiedType{}, false
	}
	root, ok := selector.X.(*ast.Ident)
	if !ok || imports[root.Name] == "" {
		return qualifiedType{}, false
	}
	return qualifiedType{path: imports[root.Name], name: selector.Sel.Name}, true
}

func typedMethod(
	call *ast.CallExpr,
	types map[string]qualifiedType,
	importPath string,
	typeName string,
	methods ...string,
) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, ok := selector.X.(*ast.Ident)
	if !ok || types[receiver.Name] != (qualifiedType{path: importPath, name: typeName}) {
		return false
	}
	for _, method := range methods {
		if selector.Sel.Name == method {
			return true
		}
	}
	return false
}

func localBindings(function *ast.FuncDecl) map[string]bool {
	bindings := make(map[string]bool)
	collectFieldNames(bindings, function.Recv)
	collectFieldNames(bindings, function.Type.Params)
	collectFieldNames(bindings, function.Type.Results)
	inspectWithoutNestedFunctions(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			if value.Tok == token.DEFINE {
				for _, expression := range value.Lhs {
					if identifier, ok := expression.(*ast.Ident); ok {
						bindings[identifier.Name] = true
					}
				}
			}
		case *ast.RangeStmt:
			if value.Tok == token.DEFINE {
				for _, expression := range []ast.Expr{value.Key, value.Value} {
					if identifier, ok := expression.(*ast.Ident); ok {
						bindings[identifier.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				bindings[name.Name] = true
			}
		}
		return true
	})
	delete(bindings, "_")
	return bindings
}

func collectFieldNames(bindings map[string]bool, fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			bindings[name.Name] = true
		}
	}
}

func inspectWithoutNestedFunctions(root ast.Node, visit func(ast.Node) bool) {
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		return visit(node)
	})
}

func positionAdded(fileSet *token.FileSet, position token.Pos, added map[int]string) (int, bool) {
	if !position.IsValid() {
		return 0, false
	}
	line := fileSet.Position(position).Line
	_, ok := added[line]
	return line, ok
}

func functionAnchor(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return stableAnchor("fn", function.Name.Name)
	}
	return stableAnchor("fn", typeAnchor(function.Recv.List[0].Type), function.Name.Name)
}

func typeAnchor(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return typeAnchor(value.X)
	case *ast.IndexExpr:
		return typeAnchor(value.X)
	case *ast.IndexListExpr:
		return typeAnchor(value.X)
	default:
		return "receiver"
	}
}

func selectorName(expression ast.Expr) string {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "call"
	}
	return selector.Sel.Name
}
