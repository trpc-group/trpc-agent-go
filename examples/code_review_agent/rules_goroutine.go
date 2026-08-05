//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

type goroutineAnalysisStats struct {
	ParsedSourceUnits    int
	SourceLinesVisited   int
	ASTNodesVisited      int
	CandidatesClassified int
}

type goroutineSourceCandidate struct {
	index int
	line  int
}

type goroutineContextValue uint8

const (
	goroutineContextUnknown goroutineContextValue = iota
	goroutineContextCancellable
	goroutineContextNonCancellable
)

type goroutineContextBindings struct {
	info              *types.Info
	contextType       types.Type
	objects           map[types.Object]goroutineContextValue
	allowNameFallback bool
}

type goroutineBindingEdge struct {
	from types.Object
	to   types.Object
}

func analyzeGoroutineCandidates(
	files []changedFile,
	repoRoot string,
	candidates []candidateLine,
) ([]bool, goroutineAnalysisStats) {
	leaks := make([]bool, len(candidates))
	matched := make([]bool, len(candidates))
	var indexes []int
	var stats goroutineAnalysisStats
	for index, candidate := range candidates {
		if candidate.FileIndex < 0 || candidate.FileIndex >= len(files) ||
			!files[candidate.FileIndex].isGoFile() ||
			!lineContainsCodeToken(candidate.Text, token.GO) {
			continue
		}
		leaks[index] = true
		indexes = append(indexes, index)
	}
	stats.CandidatesClassified = len(indexes)
	if len(indexes) == 0 {
		return leaks, stats
	}
	if repoRoot != "" {
		analyzeRepositoryGoroutineCandidates(
			files,
			repoRoot,
			candidates,
			indexes,
			leaks,
			matched,
			&stats,
		)
		return leaks, stats
	}
	analyzeDiffGoroutineCandidates(
		files,
		candidates,
		indexes,
		leaks,
		matched,
		&stats,
	)
	return leaks, stats
}

func analyzeRepositoryGoroutineCandidates(
	files []changedFile,
	repoRoot string,
	candidates []candidateLine,
	indexes []int,
	leaks []bool,
	matched []bool,
	stats *goroutineAnalysisStats,
) {
	type fileGroup struct {
		fileIndex  int
		candidates []int
	}
	groups := make(map[int]*fileGroup)
	var ordered []*fileGroup
	for _, candidateIndex := range indexes {
		fileIndex := candidates[candidateIndex].FileIndex
		group := groups[fileIndex]
		if group == nil {
			group = &fileGroup{fileIndex: fileIndex}
			groups[fileIndex] = group
			ordered = append(ordered, group)
		}
		group.candidates = append(group.candidates, candidateIndex)
	}

	for _, group := range ordered {
		filePath := files[group.fileIndex].reviewPath()
		if filePath == "" {
			continue
		}
		fset := token.NewFileSet()
		stats.ParsedSourceUnits++
		parsedFile, err := parser.ParseFile(
			fset,
			filepath.Join(repoRoot, filepath.FromSlash(filePath)),
			nil,
			parser.AllErrors,
		)
		if err != nil || parsedFile == nil {
			continue
		}
		sourceCandidates := make([]goroutineSourceCandidate, 0, len(group.candidates))
		for _, candidateIndex := range group.candidates {
			sourceCandidates = append(sourceCandidates, goroutineSourceCandidate{
				index: candidateIndex,
				line:  candidates[candidateIndex].Line,
			})
		}
		analyzeParsedGoroutineSource(
			fset,
			parsedFile,
			sourceCandidates,
			leaks,
			matched,
			false,
			stats,
		)
		markUnmatchedGoroutineCandidatesSafe(group.candidates, leaks, matched)
	}
}

func analyzeDiffGoroutineCandidates(
	files []changedFile,
	candidates []candidateLine,
	indexes []int,
	leaks []bool,
	matched []bool,
	stats *goroutineAnalysisStats,
) {
	type hunkKey struct {
		file int
		hunk int
	}
	type hunkGroup struct {
		key        hunkKey
		candidates []int
	}
	groups := make(map[hunkKey]*hunkGroup)
	var ordered []*hunkGroup
	for _, candidateIndex := range indexes {
		candidate := candidates[candidateIndex]
		key := hunkKey{file: candidate.FileIndex, hunk: candidate.HunkIndex}
		if key.file < 0 || key.file >= len(files) ||
			key.hunk < 0 || key.hunk >= len(files[key.file].Hunks) {
			continue
		}
		group := groups[key]
		if group == nil {
			group = &hunkGroup{key: key}
			groups[key] = group
			ordered = append(ordered, group)
		}
		group.candidates = append(group.candidates, candidateIndex)
	}

	for _, group := range ordered {
		hunk := files[group.key.file].Hunks[group.key.hunk]
		clearLexicallyExcludedGoroutineCandidates(
			hunk,
			group.candidates,
			candidates,
			leaks,
			matched,
		)
		var functionStarts []int
		for lineIndex, line := range hunk.Lines {
			stats.SourceLinesVisited++
			if isFunctionStartLine(line) {
				functionStarts = append(functionStarts, lineIndex)
			}
		}

		windowCandidates := make(map[int][]int)
		var orderedWindows []int
		for _, candidateIndex := range group.candidates {
			lineIndex := candidates[candidateIndex].HunkLineIndex
			position := sort.Search(len(functionStarts), func(index int) bool {
				return functionStarts[index] > lineIndex
			}) - 1
			if position < 0 {
				continue
			}
			start := functionStarts[position]
			if _, ok := windowCandidates[start]; !ok {
				orderedWindows = append(orderedWindows, start)
			}
			windowCandidates[start] = append(windowCandidates[start], candidateIndex)
		}

		for _, start := range orderedWindows {
			position := sort.SearchInts(functionStarts, start)
			end := len(hunk.Lines)
			if position+1 < len(functionStarts) {
				end = functionStarts[position+1]
			}
			var source strings.Builder
			source.WriteString("package review\n")
			sourceLine := 1
			lineMap := make(map[int]int, end-start)
			for lineIndex := start; lineIndex < end; lineIndex++ {
				stats.SourceLinesVisited++
				line := hunk.Lines[lineIndex]
				if line.Kind != diffLineAdded && line.Kind != diffLineContext {
					continue
				}
				sourceLine++
				lineMap[lineIndex] = sourceLine
				source.WriteString(line.Text)
				source.WriteByte('\n')
			}

			sourceCandidates := make([]goroutineSourceCandidate, 0, len(windowCandidates[start]))
			for _, candidateIndex := range windowCandidates[start] {
				line, ok := lineMap[candidates[candidateIndex].HunkLineIndex]
				if !ok {
					continue
				}
				sourceCandidates = append(sourceCandidates, goroutineSourceCandidate{
					index: candidateIndex,
					line:  line,
				})
			}
			if len(sourceCandidates) == 0 {
				continue
			}
			fset := token.NewFileSet()
			stats.ParsedSourceUnits++
			parsedFile, err := parser.ParseFile(
				fset,
				"review_hunk.go",
				source.String(),
				parser.AllErrors,
			)
			if err != nil || parsedFile == nil {
				continue
			}
			analyzeParsedGoroutineSource(
				fset,
				parsedFile,
				sourceCandidates,
				leaks,
				matched,
				false,
				stats,
			)
			windowIndexes := make([]int, 0, len(sourceCandidates))
			for _, candidate := range sourceCandidates {
				windowIndexes = append(windowIndexes, candidate.index)
			}
			markUnmatchedGoroutineCandidatesSafe(windowIndexes, leaks, matched)
		}

		var fallbackSource strings.Builder
		fallbackSource.WriteString("package review\nfunc reviewFallback() {\n")
		fallbackLine := 2
		var fallbackCandidates []goroutineSourceCandidate
		for _, candidateIndex := range group.candidates {
			if matched[candidateIndex] {
				continue
			}
			line := strings.TrimSpace(candidates[candidateIndex].Text)
			if !isCompleteSingleLineGoStatement(line) {
				continue
			}
			stats.SourceLinesVisited++
			fallbackLine++
			fallbackCandidates = append(fallbackCandidates, goroutineSourceCandidate{
				index: candidateIndex,
				line:  fallbackLine,
			})
			fallbackSource.WriteByte('\t')
			fallbackSource.WriteString(line)
			fallbackSource.WriteByte('\n')
		}
		if len(fallbackCandidates) == 0 {
			continue
		}
		fallbackSource.WriteString("}\n")
		fset := token.NewFileSet()
		stats.ParsedSourceUnits++
		parsedFile, _ := parser.ParseFile(
			fset,
			"review_hunk_fallback.go",
			fallbackSource.String(),
			parser.AllErrors,
		)
		if parsedFile == nil {
			continue
		}
		analyzeParsedGoroutineSource(
			fset,
			parsedFile,
			fallbackCandidates,
			leaks,
			matched,
			true,
			stats,
		)
	}
}

func clearLexicallyExcludedGoroutineCandidates(
	hunk diffHunk,
	indexes []int,
	candidates []candidateLine,
	leaks []bool,
	matched []bool,
) {
	source := make([]byte, 0, len(hunk.Lines)*32)
	sourceLineToHunkLine := make([]int, 0, len(hunk.Lines))
	visibleHunkLines := make([]bool, len(hunk.Lines))
	for lineIndex, line := range hunk.Lines {
		if line.Kind != diffLineAdded && line.Kind != diffLineContext {
			continue
		}
		visibleHunkLines[lineIndex] = true
		sourceLineToHunkLine = append(sourceLineToHunkLine, lineIndex)
		source = append(source, line.Text...)
		source = append(source, '\n')
	}

	fset := token.NewFileSet()
	file := fset.AddFile("review_hunk_tokens.go", fset.Base(), len(source))
	var lexical scanner.Scanner
	lexical.Init(file, source, nil, scanner.ScanComments)
	goHunkLines := make([]bool, len(hunk.Lines))
	for {
		position, scanned, _ := lexical.Scan()
		if scanned == token.EOF {
			break
		}
		if scanned == token.GO {
			sourceLine := fset.Position(position).Line
			if sourceLine >= 1 && sourceLine <= len(sourceLineToHunkLine) {
				goHunkLines[sourceLineToHunkLine[sourceLine-1]] = true
			}
		}
	}
	if lexical.ErrorCount != 0 {
		return
	}
	for _, candidateIndex := range indexes {
		hunkLine := candidates[candidateIndex].HunkLineIndex
		if hunkLine < 0 || hunkLine >= len(hunk.Lines) ||
			!visibleHunkLines[hunkLine] || goHunkLines[hunkLine] {
			continue
		}
		matched[candidateIndex] = true
		leaks[candidateIndex] = false
	}
}

func markUnmatchedGoroutineCandidatesSafe(indexes []int, leaks []bool, matched []bool) {
	for _, candidateIndex := range indexes {
		if matched[candidateIndex] {
			continue
		}
		matched[candidateIndex] = true
		leaks[candidateIndex] = false
	}
}

func analyzeParsedGoroutineSource(
	fset *token.FileSet,
	parsedFile *ast.File,
	sourceCandidates []goroutineSourceCandidate,
	leaks []bool,
	matched []bool,
	allowNameFallback bool,
	stats *goroutineAnalysisStats,
) {
	info, contextType := goroutineTypeInfo(fset, parsedFile)
	bindings := newGoroutineContextBindings(
		parsedFile,
		info,
		contextType,
		allowNameFallback,
		stats,
	)
	byLine := make(map[int][]int)
	for _, candidate := range sourceCandidates {
		byLine[candidate.line] = append(byLine[candidate.line], candidate.index)
	}

	var statements []*ast.GoStmt
	ast.Inspect(parsedFile, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		stats.ASTNodesVisited++
		if statement, ok := node.(*ast.GoStmt); ok {
			statements = append(statements, statement)
		}
		return true
	})
	for _, statement := range statements {
		indexes := byLine[fset.Position(statement.Go).Line]
		if len(indexes) == 0 {
			continue
		}
		safe := goroutineCallUsesCancellation(statement.Call, bindings, stats)
		for _, candidateIndex := range indexes {
			if !matched[candidateIndex] {
				matched[candidateIndex] = true
				leaks[candidateIndex] = !safe
				continue
			}
			if !safe {
				leaks[candidateIndex] = true
			}
		}
	}
}

func goroutineTypeInfo(fset *token.FileSet, parsedFile *ast.File) (*types.Info, types.Type) {
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	packageImporter := importer.Default()
	var contextType types.Type
	if contextPackage, err := packageImporter.Import("context"); err == nil {
		if object := contextPackage.Scope().Lookup("Context"); object != nil {
			contextType = object.Type()
		}
	}
	config := &types.Config{
		Importer: packageImporter,
		Error:    func(error) {},
	}
	_, _ = config.Check(parsedFile.Name.Name, fset, []*ast.File{parsedFile}, info)
	return info, contextType
}

func newGoroutineContextBindings(
	parsedFile *ast.File,
	info *types.Info,
	contextType types.Type,
	allowNameFallback bool,
	stats *goroutineAnalysisStats,
) *goroutineContextBindings {
	bindings := &goroutineContextBindings{
		info:              info,
		contextType:       contextType,
		objects:           make(map[types.Object]goroutineContextValue),
		allowNameFallback: allowNameFallback,
	}
	var edges []goroutineBindingEdge
	ast.Inspect(parsedFile, func(node ast.Node) bool {
		if node != nil {
			stats.ASTNodesVisited++
		}
		switch typed := node.(type) {
		case *ast.Field:
			if !isContextTypeSyntax(typed.Type) {
				return true
			}
			for _, name := range typed.Names {
				bindings.setObjectValue(bindingObject(info, name), goroutineContextCancellable)
			}
		case *ast.ValueSpec:
			if isContextTypeSyntax(typed.Type) {
				for _, name := range typed.Names {
					bindings.setObjectValue(bindingObject(info, name), goroutineContextCancellable)
				}
			}
			bindings.collectValueSpecBindings(typed, &edges)
		case *ast.AssignStmt:
			bindings.collectAssignmentBindings(typed, &edges)
		}
		return true
	})
	bindings.propagateBindingEdges(edges)
	return bindings
}

func (b *goroutineContextBindings) collectValueSpecBindings(
	spec *ast.ValueSpec,
	edges *[]goroutineBindingEdge,
) {
	if len(spec.Values) == 1 && len(spec.Names) > 1 {
		if value := bindingExpressionValue(spec.Values[0], b.info, b.contextType); value != goroutineContextUnknown {
			b.setObjectValue(bindingObject(b.info, spec.Names[0]), value)
		}
		return
	}
	for index, expression := range spec.Values {
		if index >= len(spec.Names) {
			break
		}
		b.collectBinding(
			bindingObject(b.info, spec.Names[index]),
			expression,
			edges,
		)
	}
}

func (b *goroutineContextBindings) collectAssignmentBindings(
	assignment *ast.AssignStmt,
	edges *[]goroutineBindingEdge,
) {
	if len(assignment.Rhs) == 1 && len(assignment.Lhs) > 1 {
		identifier, ok := unparenthesizedExpression(assignment.Lhs[0]).(*ast.Ident)
		if !ok {
			return
		}
		if value := bindingExpressionValue(assignment.Rhs[0], b.info, b.contextType); value != goroutineContextUnknown {
			b.setObjectValue(bindingObject(b.info, identifier), value)
		}
		return
	}
	for index, expression := range assignment.Rhs {
		if index >= len(assignment.Lhs) {
			break
		}
		identifier, ok := unparenthesizedExpression(assignment.Lhs[index]).(*ast.Ident)
		if !ok || identifier.Name == "_" {
			continue
		}
		b.collectBinding(bindingObject(b.info, identifier), expression, edges)
	}
}

func (b *goroutineContextBindings) collectBinding(
	target types.Object,
	expression ast.Expr,
	edges *[]goroutineBindingEdge,
) {
	if target == nil {
		return
	}
	if identifier, ok := unparenthesizedExpression(expression).(*ast.Ident); ok {
		if source := bindingObject(b.info, identifier); source != nil {
			*edges = append(*edges, goroutineBindingEdge{from: source, to: target})
			return
		}
	}
	if value := bindingExpressionValue(expression, b.info, b.contextType); value != goroutineContextUnknown {
		b.setObjectValue(target, value)
	}
}

func (b *goroutineContextBindings) propagateBindingEdges(edges []goroutineBindingEdge) {
	adjacent := make(map[types.Object][]types.Object)
	for _, edge := range edges {
		if edge.from == nil || edge.to == nil {
			continue
		}
		adjacent[edge.from] = append(adjacent[edge.from], edge.to)
	}
	queue := make([]types.Object, 0, len(b.objects))
	queued := make(map[types.Object]bool, len(b.objects))
	for object := range b.objects {
		queue = append(queue, object)
		queued[object] = true
	}
	for len(queue) > 0 {
		object := queue[0]
		queue = queue[1:]
		queued[object] = false
		value := b.objects[object]
		for _, target := range adjacent[object] {
			if !b.setObjectValue(target, value) || queued[target] {
				continue
			}
			queue = append(queue, target)
			queued[target] = true
		}
	}
}

func (b *goroutineContextBindings) setObjectValue(
	object types.Object,
	value goroutineContextValue,
) bool {
	if object == nil || value == goroutineContextUnknown {
		return false
	}
	current := b.objects[object]
	next := value
	if current == goroutineContextNonCancellable ||
		current == goroutineContextCancellable && value == goroutineContextNonCancellable {
		next = goroutineContextNonCancellable
	} else if current == goroutineContextCancellable {
		next = goroutineContextCancellable
	}
	if current == next {
		return false
	}
	b.objects[object] = next
	return true
}

func bindingExpressionValue(
	expression ast.Expr,
	info *types.Info,
	contextType types.Type,
) goroutineContextValue {
	expression = unparenthesizedExpression(expression)
	call, ok := expression.(*ast.CallExpr)
	if ok {
		if name, isContext := contextPackageCallName(call, info); isContext {
			switch name {
			case "Background", "TODO", "WithoutCancel":
				return goroutineContextNonCancellable
			case "WithCancel", "WithCancelCause", "WithTimeout", "WithTimeoutCause",
				"WithDeadline", "WithDeadlineCause":
				return goroutineContextCancellable
			}
		}
		if selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr); ok &&
			selector.Sel.Name == "Context" && len(call.Args) == 0 {
			return goroutineContextCancellable
		}
	}
	if expressionHasContextType(expression, info, contextType) {
		return goroutineContextCancellable
	}
	return goroutineContextUnknown
}

func (b *goroutineContextBindings) isCancellableContextExpression(
	expression ast.Expr,
	overrides map[types.Object]bool,
) bool {
	expression = unparenthesizedExpression(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		object := bindingObject(b.info, identifier)
		if value, ok := overrides[object]; ok {
			return value
		}
		if value, ok := b.objects[object]; ok {
			return value == goroutineContextCancellable
		}
		if object != nil {
			return typeIsContext(object.Type(), b.contextType)
		}
		return b.allowNameFallback && isContextFallbackName(identifier.Name)
	}
	if call, ok := expression.(*ast.CallExpr); ok {
		if name, isContext := contextPackageCallName(call, b.info); isContext {
			switch name {
			case "Background", "TODO", "WithoutCancel":
				return false
			}
		}
		if selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr); ok &&
			selector.Sel.Name == "Context" && len(call.Args) == 0 {
			return true
		}
	}
	return expressionHasContextType(expression, b.info, b.contextType)
}

func goroutineCallUsesCancellation(
	call *ast.CallExpr,
	bindings *goroutineContextBindings,
	stats *goroutineAnalysisStats,
) bool {
	if call == nil {
		return false
	}
	literal, ok := unparenthesizedExpression(call.Fun).(*ast.FuncLit)
	if !ok {
		for _, argument := range call.Args {
			if bindings.isCancellableContextExpression(argument, nil) {
				return true
			}
		}
		return false
	}
	overrides := functionLiteralContextOverrides(literal, call.Args, bindings)
	return functionLiteralObservesCancellation(literal, bindings, overrides, stats)
}

func functionLiteralContextOverrides(
	literal *ast.FuncLit,
	arguments []ast.Expr,
	bindings *goroutineContextBindings,
) map[types.Object]bool {
	overrides := make(map[types.Object]bool)
	parameterIndex := 0
	if literal.Type.Params == nil {
		return overrides
	}
	for _, field := range literal.Type.Params.List {
		if len(field.Names) == 0 {
			parameterIndex++
			continue
		}
		for _, name := range field.Names {
			object := bindingObject(bindings.info, name)
			active := false
			if parameterIndex < len(arguments) &&
				bindings.objectCanCarryContext(object) {
				active = bindings.isCancellableContextExpression(arguments[parameterIndex], nil)
			}
			if object != nil {
				overrides[object] = active
			}
			parameterIndex++
		}
	}
	return overrides
}

func (b *goroutineContextBindings) objectCanCarryContext(object types.Object) bool {
	if object == nil {
		return false
	}
	if value, ok := b.objects[object]; ok {
		return value == goroutineContextCancellable
	}
	return typeIsContext(object.Type(), b.contextType)
}

func functionLiteralObservesCancellation(
	literal *ast.FuncLit,
	bindings *goroutineContextBindings,
	overrides map[types.Object]bool,
	stats *goroutineAnalysisStats,
) bool {
	found := false
	ast.Inspect(literal.Body, func(node ast.Node) bool {
		if node == nil || found {
			return false
		}
		stats.ASTNodesVisited++
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if _, ok := node.(*ast.GoStmt); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr); ok {
			switch selector.Sel.Name {
			case "Done", "Err", "Deadline":
				if bindings.isCancellableContextExpression(selector.X, overrides) {
					found = true
					return false
				}
			}
		}
		if name, isContext := contextPackageCallName(call, bindings.info); isContext &&
			name == "Cause" && len(call.Args) > 0 &&
			bindings.isCancellableContextExpression(call.Args[0], overrides) {
			found = true
			return false
		}
		for _, argument := range call.Args {
			if bindings.isCancellableContextExpression(argument, overrides) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func contextPackageCallName(call *ast.CallExpr, info *types.Info) (string, bool) {
	if call == nil {
		return "", false
	}
	selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if function, ok := info.Uses[selector.Sel].(*types.Func); ok &&
		function.Pkg() != nil && function.Pkg().Path() == "context" {
		return selector.Sel.Name, true
	}
	identifier, ok := unparenthesizedExpression(selector.X).(*ast.Ident)
	if !ok {
		return "", false
	}
	if packageName, ok := info.Uses[identifier].(*types.PkgName); ok {
		return selector.Sel.Name,
			packageName.Imported() != nil && packageName.Imported().Path() == "context"
	}
	return selector.Sel.Name, identifier.Name == "context" ||
		info.Uses[identifier] == nil && isKnownContextFunctionName(selector.Sel.Name)
}

func isKnownContextFunctionName(name string) bool {
	switch name {
	case "Background", "TODO", "WithoutCancel", "Cause", "WithCancel", "WithCancelCause",
		"WithTimeout", "WithTimeoutCause", "WithDeadline", "WithDeadlineCause":
		return true
	default:
		return false
	}
}

func expressionHasContextType(expression ast.Expr, info *types.Info, contextType types.Type) bool {
	if expression == nil || info == nil {
		return false
	}
	return typeIsContext(info.TypeOf(expression), contextType)
}

func typeIsContext(value types.Type, contextType types.Type) bool {
	if value == nil || contextType == nil {
		return false
	}
	return types.AssignableTo(value, contextType)
}

func isContextTypeSyntax(expression ast.Expr) bool {
	if expression == nil {
		return false
	}
	switch typed := unparenthesizedExpression(expression).(type) {
	case *ast.Ident:
		return typed.Name == "Context"
	case *ast.SelectorExpr:
		return typed.Sel.Name == "Context"
	default:
		return false
	}
}

func isContextFallbackName(name string) bool {
	return name == "ctx" || strings.HasSuffix(name, "Ctx")
}

func isCompleteSingleLineGoStatement(line string) bool {
	if strings.ContainsAny(line, "\r\n") || !lineContainsCodeToken(line, token.GO) {
		return false
	}
	fset := token.NewFileSet()
	file := fset.AddFile("review_line.go", fset.Base(), len(line))
	var lexical scanner.Scanner
	lexical.Init(file, []byte(line), nil, scanner.ScanComments)
	seenGo := false
	parentheses := 0
	brackets := 0
	braces := 0
	for {
		_, scanned, _ := lexical.Scan()
		if scanned == token.EOF {
			break
		}
		if scanned == token.ILLEGAL {
			return false
		}
		if scanned == token.COMMENT || scanned == token.SEMICOLON {
			continue
		}
		if scanned == token.GO {
			seenGo = true
		}
		switch scanned {
		case token.LPAREN:
			parentheses++
		case token.RPAREN:
			parentheses--
		case token.LBRACK:
			brackets++
		case token.RBRACK:
			brackets--
		case token.LBRACE:
			braces++
		case token.RBRACE:
			braces--
		}
		if parentheses < 0 || brackets < 0 || braces < 0 {
			return false
		}
	}
	return seenGo && parentheses == 0 && brackets == 0 && braces == 0
}
