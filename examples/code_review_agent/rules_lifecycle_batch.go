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
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

type lifecycleSourceCandidate struct {
	index int
	line  int
}

type lifecycleFunctionSpan struct {
	function *ast.FuncDecl
	start    int
	end      int
}

type lifecycleFunctionCandidate struct {
	globalIndex    int
	line           int
	matcher        cleanupMatcher
	assignment     *ast.AssignStmt
	resourceObject types.Object
	errorObject    types.Object
}

func analyzeLifecycleCandidates(
	files []changedFile,
	repoRoot string,
	candidates []lifecycleCandidate,
) ([]bool, lifecycleAnalysisStats) {
	proofs := make([]bool, len(candidates))
	var stats lifecycleAnalysisStats
	if len(candidates) == 0 {
		return proofs, stats
	}
	if repoRoot != "" {
		analyzeRepositoryLifecycleCandidates(files, repoRoot, candidates, proofs, &stats)
		return proofs, stats
	}
	analyzeDiffLifecycleCandidates(files, candidates, proofs, &stats)
	return proofs, stats
}

func analyzeRepositoryLifecycleCandidates(
	files []changedFile,
	repoRoot string,
	candidates []lifecycleCandidate,
	proofs []bool,
	stats *lifecycleAnalysisStats,
) {
	type fileGroup struct {
		fileIndex  int
		candidates []int
	}
	groups := make(map[int]*fileGroup)
	var ordered []*fileGroup
	for index, candidate := range candidates {
		fileIndex := candidate.candidate.FileIndex
		if fileIndex < 0 || fileIndex >= len(files) {
			continue
		}
		group := groups[fileIndex]
		if group == nil {
			group = &fileGroup{fileIndex: fileIndex}
			groups[fileIndex] = group
			ordered = append(ordered, group)
		}
		group.candidates = append(group.candidates, index)
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
			0,
		)
		if err != nil {
			continue
		}
		sourceCandidates := make([]lifecycleSourceCandidate, 0, len(group.candidates))
		for _, candidateIndex := range group.candidates {
			sourceCandidates = append(sourceCandidates, lifecycleSourceCandidate{
				index: candidateIndex,
				line:  candidates[candidateIndex].candidate.Line,
			})
		}
		analyzeParsedLifecycleSource(
			fset,
			parsedFile,
			sourceCandidates,
			candidates,
			proofs,
			stats,
		)
	}
}

func analyzeDiffLifecycleCandidates(
	files []changedFile,
	candidates []lifecycleCandidate,
	proofs []bool,
	stats *lifecycleAnalysisStats,
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
	for index, candidate := range candidates {
		key := hunkKey{
			file: candidate.candidate.FileIndex,
			hunk: candidate.candidate.HunkIndex,
		}
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
		group.candidates = append(group.candidates, index)
	}

	for _, group := range ordered {
		hunk := files[group.key.file].Hunks[group.key.hunk]
		var functionStarts []int
		for lineIndex, line := range hunk.Lines {
			if isFunctionStartLine(line) {
				functionStarts = append(functionStarts, lineIndex)
			}
		}
		if len(functionStarts) == 0 {
			continue
		}

		windowCandidates := make(map[int][]int)
		var orderedWindows []int
		for _, candidateIndex := range group.candidates {
			lineIndex := candidates[candidateIndex].candidate.HunkLineIndex
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
				line := hunk.Lines[lineIndex]
				if line.Kind != diffLineAdded && line.Kind != diffLineContext {
					continue
				}
				sourceLine++
				lineMap[lineIndex] = sourceLine
				source.WriteString(line.Text)
				source.WriteByte('\n')
			}

			sourceCandidates := make([]lifecycleSourceCandidate, 0, len(windowCandidates[start]))
			for _, candidateIndex := range windowCandidates[start] {
				line, ok := lineMap[candidates[candidateIndex].candidate.HunkLineIndex]
				if !ok {
					continue
				}
				sourceCandidates = append(sourceCandidates, lifecycleSourceCandidate{
					index: candidateIndex,
					line:  line,
				})
			}
			if len(sourceCandidates) == 0 {
				continue
			}
			fset := token.NewFileSet()
			stats.ParsedSourceUnits++
			parsedFile, err := parser.ParseFile(fset, "review_hunk.go", source.String(), 0)
			if err != nil {
				continue
			}
			analyzeParsedLifecycleSource(
				fset,
				parsedFile,
				sourceCandidates,
				candidates,
				proofs,
				stats,
			)
		}
	}
}

func analyzeParsedLifecycleSource(
	fset *token.FileSet,
	parsedFile *ast.File,
	sourceCandidates []lifecycleSourceCandidate,
	candidates []lifecycleCandidate,
	proofs []bool,
	stats *lifecycleAnalysisStats,
) {
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object),
	}
	config := &types.Config{
		Importer: importer.Default(),
		Error:    func(error) {},
	}
	stats.TypeCheckedSourceUnits++
	_, _ = config.Check(parsedFile.Name.Name, fset, []*ast.File{parsedFile}, info)

	var spans []lifecycleFunctionSpan
	for _, declaration := range parsedFile.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		spans = append(spans, lifecycleFunctionSpan{
			function: function,
			start:    fset.Position(function.Pos()).Line,
			end:      fset.Position(function.End()).Line,
		})
	}
	sort.Slice(spans, func(left int, right int) bool {
		return spans[left].start < spans[right].start
	})

	groups := make(map[*ast.FuncDecl][]lifecycleSourceCandidate)
	var orderedFunctions []*ast.FuncDecl
	for _, candidate := range sourceCandidates {
		position := sort.Search(len(spans), func(index int) bool {
			return spans[index].end >= candidate.line
		})
		if position >= len(spans) || spans[position].start > candidate.line {
			continue
		}
		function := spans[position].function
		if _, ok := groups[function]; !ok {
			orderedFunctions = append(orderedFunctions, function)
		}
		groups[function] = append(groups[function], candidate)
	}

	for _, function := range orderedFunctions {
		stats.AnalyzedFunctions++
		analyzeLifecycleFunction(
			fset,
			info,
			function,
			groups[function],
			candidates,
			proofs,
		)
	}
}

func analyzeLifecycleFunction(
	fset *token.FileSet,
	info *types.Info,
	function *ast.FuncDecl,
	sourceCandidates []lifecycleSourceCandidate,
	candidates []lifecycleCandidate,
	proofs []bool,
) {
	functionCandidates := make([]lifecycleFunctionCandidate, 0, len(sourceCandidates))
	for _, sourceCandidate := range sourceCandidates {
		functionCandidates = append(functionCandidates, lifecycleFunctionCandidate{
			globalIndex: sourceCandidate.index,
			line:        sourceCandidate.line,
			matcher:     candidates[sourceCandidate.index].matcher,
		})
	}
	bindLifecycleAcquisitions(function.Body, fset, info, functionCandidates)
	if functionHasUnsupportedResourceControlFlow(function.Body) {
		return
	}
	analyzer := newBatchResourceCleanupAnalyzer(info, functionCandidates)
	proved := analyzer.prove(function.Body)
	for localIndex, candidate := range functionCandidates {
		if proved.has(localIndex) {
			proofs[candidate.globalIndex] = true
		}
	}
}

func bindLifecycleAcquisitions(
	body *ast.BlockStmt,
	fset *token.FileSet,
	info *types.Info,
	candidates []lifecycleFunctionCandidate,
) {
	byLine := make(map[int][]int)
	for index := range candidates {
		byLine[candidates[index].line] = append(byLine[candidates[index].line], index)
	}
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		assignment, ok := node.(*ast.AssignStmt)
		if !ok || assignment.Tok != token.DEFINE {
			return true
		}
		indexes := byLine[fset.Position(assignment.Pos()).Line]
		for _, index := range indexes {
			candidate := &candidates[index]
			if candidate.assignment != nil {
				continue
			}
			for lhsIndex, expression := range assignment.Lhs {
				identifier, ok := expression.(*ast.Ident)
				if !ok || identifier.Name != candidate.matcher.variable {
					continue
				}
				candidate.assignment = assignment
				candidate.resourceObject = bindingObject(info, identifier)
				if lhsIndex+1 < len(assignment.Lhs) {
					if errorIdentifier, ok := assignment.Lhs[lhsIndex+1].(*ast.Ident); ok &&
						errorIdentifier.Name != "_" {
						candidate.errorObject = bindingObject(info, errorIdentifier)
					}
				}
				break
			}
		}
		return true
	})
}

type lifecycleBits []uint64

func newLifecycleBits(count int) lifecycleBits {
	return make(lifecycleBits, (count+63)/64)
}

func allLifecycleBits(count int) lifecycleBits {
	bits := newLifecycleBits(count)
	for index := 0; index < count; index++ {
		bits.set(index)
	}
	return bits
}

func (b lifecycleBits) clone() lifecycleBits {
	result := make(lifecycleBits, len(b))
	copy(result, b)
	return result
}

func (b lifecycleBits) set(index int) {
	if index < 0 || index/64 >= len(b) {
		return
	}
	b[index/64] |= uint64(1) << uint(index%64)
}

func (b lifecycleBits) has(index int) bool {
	return index >= 0 && index/64 < len(b) &&
		b[index/64]&(uint64(1)<<uint(index%64)) != 0
}

func (b lifecycleBits) any() bool {
	for _, word := range b {
		if word != 0 {
			return true
		}
	}
	return false
}

func (b lifecycleBits) equal(other lifecycleBits) bool {
	if len(b) != len(other) {
		return false
	}
	for index := range b {
		if b[index] != other[index] {
			return false
		}
	}
	return true
}

func lifecycleBitsOr(left lifecycleBits, right lifecycleBits) lifecycleBits {
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	result := make(lifecycleBits, length)
	copy(result, left)
	for index := range right {
		result[index] |= right[index]
	}
	return result
}

func lifecycleBitsAnd(left lifecycleBits, right lifecycleBits) lifecycleBits {
	length := len(left)
	if len(right) > length {
		length = len(right)
	}
	result := make(lifecycleBits, length)
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		result[index] = left[index] & right[index]
	}
	return result
}

func lifecycleBitsWithout(left lifecycleBits, removed lifecycleBits) lifecycleBits {
	result := left.clone()
	limit := len(result)
	if len(removed) < limit {
		limit = len(removed)
	}
	for index := 0; index < limit; index++ {
		result[index] &^= removed[index]
	}
	return result
}

type lifecycleState struct {
	unacquired lifecycleBits
	active     lifecycleBits
	safe       lifecycleBits
}

func emptyLifecycleState(count int) lifecycleState {
	return lifecycleState{
		unacquired: newLifecycleBits(count),
		active:     newLifecycleBits(count),
		safe:       newLifecycleBits(count),
	}
}

func (s lifecycleState) any() bool {
	return s.unacquired.any() || s.active.any() || s.safe.any()
}

func (s lifecycleState) all() lifecycleBits {
	return lifecycleBitsOr(lifecycleBitsOr(s.unacquired, s.active), s.safe)
}

func (s lifecycleState) restrict(mask lifecycleBits) lifecycleState {
	return lifecycleState{
		unacquired: lifecycleBitsAnd(s.unacquired, mask),
		active:     lifecycleBitsAnd(s.active, mask),
		safe:       lifecycleBitsAnd(s.safe, mask),
	}
}

func (s lifecycleState) without(mask lifecycleBits) lifecycleState {
	return lifecycleState{
		unacquired: lifecycleBitsWithout(s.unacquired, mask),
		active:     lifecycleBitsWithout(s.active, mask),
		safe:       lifecycleBitsWithout(s.safe, mask),
	}
}

func (s lifecycleState) union(other lifecycleState) lifecycleState {
	return lifecycleState{
		unacquired: lifecycleBitsOr(s.unacquired, other.unacquired),
		active:     lifecycleBitsOr(s.active, other.active),
		safe:       lifecycleBitsOr(s.safe, other.safe),
	}
}

func (s lifecycleState) equal(other lifecycleState) bool {
	return s.unacquired.equal(other.unacquired) &&
		s.active.equal(other.active) &&
		s.safe.equal(other.safe)
}

type lifecycleFlow struct {
	normal    lifecycleState
	breaks    lifecycleState
	continues lifecycleState
}

type batchResourceCleanupAnalyzer struct {
	info                   *types.Info
	candidates             []lifecycleFunctionCandidate
	count                  int
	invalid                lifecycleBits
	reached                lifecycleBits
	bodyCandidates         lifecycleBits
	acquisitionByStatement map[*ast.AssignStmt]lifecycleBits
	resourceCandidates     map[types.Object]lifecycleBits
	variableCandidates     map[string]lifecycleBits
	methodCandidates       map[string]lifecycleBits
	bodyAliases            map[types.Object]lifecycleBits
}

func newBatchResourceCleanupAnalyzer(
	info *types.Info,
	candidates []lifecycleFunctionCandidate,
) *batchResourceCleanupAnalyzer {
	analyzer := &batchResourceCleanupAnalyzer{
		info:                   info,
		candidates:             candidates,
		count:                  len(candidates),
		invalid:                newLifecycleBits(len(candidates)),
		reached:                newLifecycleBits(len(candidates)),
		bodyCandidates:         newLifecycleBits(len(candidates)),
		acquisitionByStatement: make(map[*ast.AssignStmt]lifecycleBits),
		resourceCandidates:     make(map[types.Object]lifecycleBits),
		variableCandidates:     make(map[string]lifecycleBits),
		methodCandidates:       make(map[string]lifecycleBits),
		bodyAliases:            make(map[types.Object]lifecycleBits),
	}
	for index, candidate := range candidates {
		if candidate.assignment == nil || candidate.resourceObject == nil {
			analyzer.invalid.set(index)
			continue
		}
		addLifecycleMapBit(analyzer.acquisitionByStatement, candidate.assignment, index, analyzer.count)
		addLifecycleObjectBit(analyzer.resourceCandidates, candidate.resourceObject, index, analyzer.count)
		addLifecycleStringBit(analyzer.variableCandidates, candidate.matcher.variable, index, analyzer.count)
		for method := range candidate.matcher.methods {
			addLifecycleStringBit(analyzer.methodCandidates, method, index, analyzer.count)
		}
		if candidate.matcher.body {
			analyzer.bodyCandidates.set(index)
		}
	}
	return analyzer
}

func addLifecycleMapBit[K comparable](
	values map[K]lifecycleBits,
	key K,
	index int,
	count int,
) {
	bits := values[key]
	if bits == nil {
		bits = newLifecycleBits(count)
		values[key] = bits
	}
	bits.set(index)
}

func addLifecycleObjectBit(
	values map[types.Object]lifecycleBits,
	key types.Object,
	index int,
	count int,
) {
	addLifecycleMapBit(values, key, index, count)
}

func addLifecycleStringBit(
	values map[string]lifecycleBits,
	key string,
	index int,
	count int,
) {
	addLifecycleMapBit(values, key, index, count)
}

func (a *batchResourceCleanupAnalyzer) prove(body *ast.BlockStmt) lifecycleBits {
	initial := emptyLifecycleState(a.count)
	initial.unacquired = lifecycleBitsWithout(allLifecycleBits(a.count), a.invalid)
	flow := a.analyzeBlock(body.List, initial)
	proved := newLifecycleBits(a.count)
	for index := range a.candidates {
		if a.invalid.has(index) || !a.reached.has(index) ||
			flow.breaks.all().has(index) || flow.continues.all().has(index) ||
			flow.normal.active.has(index) {
			continue
		}
		proved.set(index)
	}
	return proved
}

func (a *batchResourceCleanupAnalyzer) sanitize(state lifecycleState) lifecycleState {
	return state.without(a.invalid)
}

func (a *batchResourceCleanupAnalyzer) emptyFlow() lifecycleFlow {
	return lifecycleFlow{
		normal:    emptyLifecycleState(a.count),
		breaks:    emptyLifecycleState(a.count),
		continues: emptyLifecycleState(a.count),
	}
}

func (a *batchResourceCleanupAnalyzer) analyzeBlock(
	statements []ast.Stmt,
	input lifecycleState,
) lifecycleFlow {
	flow := a.emptyFlow()
	flow.normal = a.sanitize(input)
	for index := 0; index < len(statements) && flow.normal.any(); index++ {
		if assignment, ok := statements[index].(*ast.AssignStmt); ok {
			if acquisitionMask := a.acquisitionByStatement[assignment]; acquisitionMask != nil {
				acquisitionState := flow.normal.restrict(acquisitionMask)
				otherState := flow.normal.without(acquisitionMask)
				acquired := a.acquire(acquisitionState, acquisitionMask)
				otherFlow := a.analyzeStatement(assignment, otherState)
				flow.normal = a.sanitize(acquired.union(otherFlow.normal))
				flow.breaks = flow.breaks.union(otherFlow.breaks)
				flow.continues = flow.continues.union(otherFlow.continues)

				if index+1 < len(statements) {
					skipMask := a.standardErrorGuardMask(statements[index+1], acquisitionMask)
					if skipMask.any() {
						skipped := flow.normal.restrict(skipMask)
						guardFlow := a.analyzeStatement(
							statements[index+1],
							flow.normal.without(skipMask),
						)
						flow.normal = a.sanitize(skipped.union(guardFlow.normal))
						flow.breaks = flow.breaks.union(guardFlow.breaks)
						flow.continues = flow.continues.union(guardFlow.continues)
						index++
					}
				}
				continue
			}
		}
		statementFlow := a.analyzeStatement(statements[index], flow.normal)
		flow.normal = a.sanitize(statementFlow.normal)
		flow.breaks = flow.breaks.union(statementFlow.breaks)
		flow.continues = flow.continues.union(statementFlow.continues)
	}
	return flow
}

func (a *batchResourceCleanupAnalyzer) acquire(
	input lifecycleState,
	mask lifecycleBits,
) lifecycleState {
	present := lifecycleBitsAnd(input.all(), mask)
	for index := range a.reached {
		a.reached[index] |= present[index]
	}
	invalid := lifecycleBitsAnd(input.active, mask)
	a.invalidate(invalid)
	eligible := lifecycleBitsAnd(lifecycleBitsOr(input.unacquired, input.safe), mask)
	eligible = lifecycleBitsWithout(eligible, invalid)
	result := emptyLifecycleState(a.count)
	result.active = eligible
	return a.sanitize(result)
}

func (a *batchResourceCleanupAnalyzer) analyzeStatement(
	statement ast.Stmt,
	input lifecycleState,
) lifecycleFlow {
	input = a.sanitize(input)
	if !input.any() {
		return a.emptyFlow()
	}
	obscured := lifecycleBitsAnd(input.active, a.statementObscuresMask(statement))
	a.invalidate(obscured)
	input = a.sanitize(input)

	switch typed := statement.(type) {
	case *ast.BlockStmt:
		return a.analyzeBlock(typed.List, input)
	case *ast.AssignStmt:
		a.observeBodyAliasAssignment(typed, input)
		state := a.applyCleanupExpressions(input, typed.Rhs)
		a.rejectActiveReassignment(typed.Lhs, state)
		return a.normalFlow(state)
	case *ast.DeclStmt:
		state := input
		if declaration, ok := typed.Decl.(*ast.GenDecl); ok {
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				a.observeBodyAliasDeclaration(value, state)
				state = a.applyCleanupExpressions(state, value.Values)
			}
		}
		return a.normalFlow(state)
	case *ast.ExprStmt:
		a.rejectBodyBindingCall(typed.X, input)
		state := a.applyCleanupExpression(input, typed.X)
		if isDirectPanicCall(typed.X) {
			a.rejectActiveExit(state)
			return a.emptyFlow()
		}
		return a.normalFlow(state)
	case *ast.DeferStmt:
		state := a.applyCleanupCall(input, typed.Call)
		return a.normalFlow(state)
	case *ast.GoStmt:
		a.rejectBodyBindingCall(typed.Call, input)
		return a.normalFlow(input)
	case *ast.ReturnStmt:
		state := a.applyCleanupExpressions(input, typed.Results)
		a.rejectActiveExit(state)
		return a.emptyFlow()
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
		flow := a.emptyFlow()
		switch typed.Tok {
		case token.BREAK:
			flow.breaks = input
		case token.CONTINUE:
			flow.continues = input
		default:
			a.invalidate(input.all())
		}
		return flow
	case *ast.IncDecStmt:
		a.rejectActiveReassignment([]ast.Expr{typed.X}, input)
		return a.normalFlow(input)
	case *ast.LabeledStmt:
		return a.analyzeStatement(typed.Stmt, input)
	case *ast.SendStmt:
		a.rejectBodyBindingExposure(typed.Value, input)
		return a.normalFlow(input)
	case *ast.EmptyStmt:
		return a.normalFlow(input)
	default:
		a.invalidate(input.all())
		return a.emptyFlow()
	}
}

func (a *batchResourceCleanupAnalyzer) normalFlow(state lifecycleState) lifecycleFlow {
	flow := a.emptyFlow()
	flow.normal = a.sanitize(state)
	return flow
}

func (a *batchResourceCleanupAnalyzer) invalidate(mask lifecycleBits) {
	for index := range a.invalid {
		a.invalid[index] |= mask[index]
	}
}

func (a *batchResourceCleanupAnalyzer) statementObscuresMask(statement ast.Stmt) lifecycleBits {
	mask := newLifecycleBits(a.count)
	ast.Inspect(statement, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncLit:
			ast.Inspect(typed.Body, func(inner ast.Node) bool {
				identifier, ok := inner.(*ast.Ident)
				if ok {
					mask = lifecycleBitsOr(mask, a.identifierMayObscureMask(identifier))
				}
				return true
			})
			return false
		case *ast.UnaryExpr:
			if typed.Op == token.AND {
				mask = lifecycleBitsOr(mask, a.expressionCleanupTargetMask(typed.X))
			}
		}
		return true
	})
	return mask
}

func (a *batchResourceCleanupAnalyzer) identifierResourceMask(
	identifier *ast.Ident,
) lifecycleBits {
	if identifier == nil {
		return newLifecycleBits(a.count)
	}
	mask := a.variableCandidates[identifier.Name]
	if mask == nil {
		return newLifecycleBits(a.count)
	}
	object := bindingObject(a.info, identifier)
	if object == nil {
		return mask.clone()
	}
	objectMask := a.resourceCandidates[object]
	if objectMask == nil {
		return newLifecycleBits(a.count)
	}
	return lifecycleBitsAnd(mask, objectMask)
}

func (a *batchResourceCleanupAnalyzer) identifierBodyAliasMask(
	identifier *ast.Ident,
) lifecycleBits {
	if identifier == nil {
		return newLifecycleBits(a.count)
	}
	object := bindingObject(a.info, identifier)
	if object == nil || a.bodyAliases[object] == nil {
		return newLifecycleBits(a.count)
	}
	return a.bodyAliases[object].clone()
}

func (a *batchResourceCleanupAnalyzer) identifierBodyResponseMask(
	identifier *ast.Ident,
) lifecycleBits {
	mask := lifecycleBitsOr(
		a.identifierResourceMask(identifier),
		a.identifierBodyAliasMask(identifier),
	)
	return lifecycleBitsAnd(mask, a.bodyCandidates)
}

func (a *batchResourceCleanupAnalyzer) identifierMayObscureMask(
	identifier *ast.Ident,
) lifecycleBits {
	return lifecycleBitsOr(
		a.identifierResourceMask(identifier),
		a.identifierBodyAliasMask(identifier),
	)
}

func (a *batchResourceCleanupAnalyzer) expressionCleanupTargetMask(
	expression ast.Expr,
) lifecycleBits {
	expression = unparenthesizedExpression(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		return a.identifierResourceMask(identifier)
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if ok && selector.Sel.Name == "Body" {
		return a.expressionBodyResponseMask(selector.X)
	}
	star, ok := expression.(*ast.StarExpr)
	if !ok {
		return newLifecycleBits(a.count)
	}
	identifier, ok := unparenthesizedExpression(star.X).(*ast.Ident)
	if !ok {
		return newLifecycleBits(a.count)
	}
	return a.identifierBodyResponseMask(identifier)
}

func (a *batchResourceCleanupAnalyzer) expressionBodyResponseMask(
	expression ast.Expr,
) lifecycleBits {
	expression = unparenthesizedExpression(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		return a.identifierBodyResponseMask(identifier)
	}
	star, ok := expression.(*ast.StarExpr)
	if !ok {
		return newLifecycleBits(a.count)
	}
	identifier, ok := unparenthesizedExpression(star.X).(*ast.Ident)
	if !ok {
		return newLifecycleBits(a.count)
	}
	return a.identifierBodyResponseMask(identifier)
}

func (a *batchResourceCleanupAnalyzer) observeBodyAliasAssignment(
	statement *ast.AssignStmt,
	state lifecycleState,
) {
	activeBody := lifecycleBitsAnd(state.active, a.bodyCandidates)
	if !activeBody.any() {
		return
	}
	a.rejectBodyBindingCalls(statement.Rhs, state)
	if len(statement.Lhs) != len(statement.Rhs) {
		for _, expression := range statement.Rhs {
			a.invalidate(lifecycleBitsAnd(activeBody, a.expressionContainsBodyResponseMask(expression)))
		}
		return
	}
	for index, expression := range statement.Rhs {
		valueMask := lifecycleBitsAnd(activeBody, a.expressionBodyResponseValueMask(expression))
		if valueMask.any() {
			a.bindBodyAlias(statement.Lhs[index], valueMask)
			continue
		}
		a.invalidate(lifecycleBitsAnd(activeBody, a.expressionContainsBodyResponseMask(expression)))
	}
}

func (a *batchResourceCleanupAnalyzer) observeBodyAliasDeclaration(
	specification *ast.ValueSpec,
	state lifecycleState,
) {
	activeBody := lifecycleBitsAnd(state.active, a.bodyCandidates)
	if !activeBody.any() {
		return
	}
	a.rejectBodyBindingCalls(specification.Values, state)
	if len(specification.Names) != len(specification.Values) {
		for _, expression := range specification.Values {
			a.invalidate(lifecycleBitsAnd(activeBody, a.expressionContainsBodyResponseMask(expression)))
		}
		return
	}
	for index, expression := range specification.Values {
		valueMask := lifecycleBitsAnd(activeBody, a.expressionBodyResponseValueMask(expression))
		if valueMask.any() {
			a.bindBodyAlias(specification.Names[index], valueMask)
			continue
		}
		a.invalidate(lifecycleBitsAnd(activeBody, a.expressionContainsBodyResponseMask(expression)))
	}
}

func (a *batchResourceCleanupAnalyzer) bindBodyAlias(
	expression ast.Expr,
	mask lifecycleBits,
) {
	identifier, ok := unparenthesizedExpression(expression).(*ast.Ident)
	if !ok {
		a.invalidate(mask)
		return
	}
	if identifier.Name == "_" {
		return
	}
	object := bindingObject(a.info, identifier)
	if object != nil {
		if resourceMask := a.resourceCandidates[object]; resourceMask != nil {
			mask = lifecycleBitsWithout(mask, resourceMask)
		}
	}
	if !mask.any() {
		return
	}
	if object == nil || object.Parent() == nil ||
		object.Pkg() != nil && object.Parent() == object.Pkg().Scope() {
		a.invalidate(mask)
		return
	}
	for index := range mask {
		aliases := a.bodyAliases[object]
		if aliases == nil {
			aliases = newLifecycleBits(a.count)
			a.bodyAliases[object] = aliases
		}
		aliases[index] |= mask[index]
	}
}

func (a *batchResourceCleanupAnalyzer) rejectBodyBindingExposure(
	expression ast.Expr,
	state lifecycleState,
) {
	activeBody := lifecycleBitsAnd(state.active, a.bodyCandidates)
	mask := lifecycleBitsOr(
		a.expressionContainsBodyResponseMask(expression),
		a.expressionCallsWithBodyResponseMask(expression),
	)
	a.invalidate(lifecycleBitsAnd(activeBody, mask))
}

func (a *batchResourceCleanupAnalyzer) rejectBodyBindingCall(
	expression ast.Expr,
	state lifecycleState,
) {
	activeBody := lifecycleBitsAnd(state.active, a.bodyCandidates)
	a.invalidate(lifecycleBitsAnd(activeBody, a.expressionCallsWithBodyResponseMask(expression)))
}

func (a *batchResourceCleanupAnalyzer) rejectBodyBindingCalls(
	expressions []ast.Expr,
	state lifecycleState,
) {
	for _, expression := range expressions {
		a.rejectBodyBindingCall(expression, state)
	}
}

func (a *batchResourceCleanupAnalyzer) expressionBodyResponseValueMask(
	expression ast.Expr,
) lifecycleBits {
	identifier, ok := unparenthesizedExpression(expression).(*ast.Ident)
	if !ok {
		return newLifecycleBits(a.count)
	}
	return a.identifierBodyResponseMask(identifier)
}

func (a *batchResourceCleanupAnalyzer) expressionContainsBodyResponseMask(
	expression ast.Expr,
) lifecycleBits {
	expression = unparenthesizedExpression(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		return a.identifierBodyResponseMask(identifier)
	}
	switch typed := expression.(type) {
	case *ast.CompositeLit:
		mask := newLifecycleBits(a.count)
		for _, element := range typed.Elts {
			mask = lifecycleBitsOr(mask, a.expressionContainsBodyResponseMask(element))
		}
		return mask
	case *ast.KeyValueExpr:
		return lifecycleBitsOr(
			a.expressionContainsBodyResponseMask(typed.Key),
			a.expressionContainsBodyResponseMask(typed.Value),
		)
	case *ast.UnaryExpr:
		if typed.Op == token.AND {
			return a.expressionContainsBodyResponseMask(typed.X)
		}
	}
	return newLifecycleBits(a.count)
}

func (a *batchResourceCleanupAnalyzer) expressionCallsWithBodyResponseMask(
	expression ast.Expr,
) lifecycleBits {
	mask := newLifecycleBits(a.count)
	ast.Inspect(expression, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr); ok {
			mask = lifecycleBitsOr(mask, a.expressionBodyResponseMask(selector.X))
		}
		for _, argument := range call.Args {
			mask = lifecycleBitsOr(mask, a.expressionContainsBodyResponseMask(argument))
		}
		return true
	})
	return mask
}

func (a *batchResourceCleanupAnalyzer) analyzeIf(
	statement *ast.IfStmt,
	input lifecycleState,
) lifecycleFlow {
	state := input
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init, state)
		if initFlow.breaks.any() || initFlow.continues.any() {
			a.invalidate(state.all())
			return a.emptyFlow()
		}
		state = initFlow.normal
	}
	a.rejectBodyBindingCall(statement.Cond, state)
	state = a.applyCleanupExpression(state, statement.Cond)
	thenFlow := a.analyzeBlock(statement.Body.List, state)
	elseFlow := a.normalFlow(state)
	if statement.Else != nil {
		elseFlow = a.analyzeStatement(statement.Else, state)
	}
	return mergeLifecycleFlows(thenFlow, elseFlow)
}

func (a *batchResourceCleanupAnalyzer) analyzeFor(
	statement *ast.ForStmt,
	input lifecycleState,
) lifecycleFlow {
	state := input
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init, state)
		if initFlow.breaks.any() || initFlow.continues.any() {
			a.invalidate(state.all())
			return a.emptyFlow()
		}
		state = initFlow.normal
	}
	if statement.Cond != nil {
		a.rejectBodyBindingCall(statement.Cond, state)
	}
	return a.analyzeLoop(statement.Body, statement.Post, state)
}

func (a *batchResourceCleanupAnalyzer) analyzeRange(
	statement *ast.RangeStmt,
	input lifecycleState,
) lifecycleFlow {
	a.rejectBodyBindingExposure(statement.X, input)
	if statement.Tok == token.ASSIGN {
		a.rejectActiveReassignment([]ast.Expr{statement.Key, statement.Value}, input)
	}
	return a.analyzeLoop(statement.Body, nil, input)
}

func (a *batchResourceCleanupAnalyzer) analyzeLoop(
	body *ast.BlockStmt,
	post ast.Stmt,
	input lifecycleState,
) lifecycleFlow {
	entry := input
	exits := input
	breaks := emptyLifecycleState(a.count)
	for {
		bodyFlow := a.analyzeBlock(body.List, entry)
		breaks = breaks.union(bodyFlow.breaks)
		iteration := bodyFlow.normal.union(bodyFlow.continues)
		if post != nil && iteration.any() {
			postFlow := a.analyzeStatement(post, iteration)
			if postFlow.breaks.any() || postFlow.continues.any() {
				a.invalidate(iteration.all())
				break
			}
			iteration = postFlow.normal
		}
		exits = exits.union(iteration)
		nextEntry := a.sanitize(entry.union(iteration))
		if nextEntry.equal(entry) {
			break
		}
		entry = nextEntry
	}
	return a.normalFlow(exits.union(breaks))
}

func (a *batchResourceCleanupAnalyzer) analyzeSwitch(
	statement *ast.SwitchStmt,
	input lifecycleState,
) lifecycleFlow {
	state := input
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init, state)
		if initFlow.breaks.any() || initFlow.continues.any() {
			a.invalidate(state.all())
			return a.emptyFlow()
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

func (a *batchResourceCleanupAnalyzer) analyzeTypeSwitch(
	statement *ast.TypeSwitchStmt,
	input lifecycleState,
) lifecycleFlow {
	state := input
	for _, initialization := range []ast.Stmt{statement.Init, statement.Assign} {
		if initialization == nil {
			continue
		}
		initFlow := a.analyzeStatement(initialization, state)
		if initFlow.breaks.any() || initFlow.continues.any() {
			a.invalidate(state.all())
			return a.emptyFlow()
		}
		state = initFlow.normal
	}
	return a.analyzeCaseClauses(statement.Body.List, state)
}

func (a *batchResourceCleanupAnalyzer) analyzeCaseClauses(
	clauses []ast.Stmt,
	input lifecycleState,
) lifecycleFlow {
	result := a.emptyFlow()
	hasDefault := false
	for _, rawClause := range clauses {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok {
			a.invalidate(input.all())
			return a.emptyFlow()
		}
		if clause.List == nil {
			hasDefault = true
		}
		clauseFlow := a.analyzeBlock(clause.Body, input)
		result.normal = result.normal.union(clauseFlow.normal).union(clauseFlow.breaks)
		result.continues = result.continues.union(clauseFlow.continues)
	}
	if !hasDefault {
		result.normal = result.normal.union(input)
	}
	return result
}

func (a *batchResourceCleanupAnalyzer) analyzeSelect(
	statement *ast.SelectStmt,
	input lifecycleState,
) lifecycleFlow {
	result := a.normalFlow(input)
	for _, rawClause := range statement.Body.List {
		clause, ok := rawClause.(*ast.CommClause)
		if !ok {
			a.invalidate(input.all())
			return a.emptyFlow()
		}
		state := input
		if clause.Comm != nil {
			commFlow := a.analyzeStatement(clause.Comm, state)
			if commFlow.breaks.any() || commFlow.continues.any() {
				a.invalidate(state.all())
				return a.emptyFlow()
			}
			state = commFlow.normal
		}
		clauseFlow := a.analyzeBlock(clause.Body, state)
		result.normal = result.normal.union(clauseFlow.normal).union(clauseFlow.breaks)
		result.continues = result.continues.union(clauseFlow.continues)
	}
	return result
}

func mergeLifecycleFlows(left lifecycleFlow, right lifecycleFlow) lifecycleFlow {
	return lifecycleFlow{
		normal:    left.normal.union(right.normal),
		breaks:    left.breaks.union(right.breaks),
		continues: left.continues.union(right.continues),
	}
}

func (a *batchResourceCleanupAnalyzer) applyCleanupExpressions(
	state lifecycleState,
	expressions []ast.Expr,
) lifecycleState {
	for _, expression := range expressions {
		state = a.applyCleanupExpression(state, expression)
	}
	return a.sanitize(state)
}

func (a *batchResourceCleanupAnalyzer) applyCleanupExpression(
	state lifecycleState,
	expression ast.Expr,
) lifecycleState {
	call, ok := unparenthesizedExpression(expression).(*ast.CallExpr)
	if !ok {
		return a.sanitize(state)
	}
	return a.applyCleanupCall(state, call)
}

func (a *batchResourceCleanupAnalyzer) applyCleanupCall(
	state lifecycleState,
	call *ast.CallExpr,
) lifecycleState {
	cleanup, invalid := a.cleanupCallMasks(call)
	a.invalidate(invalid)
	state = a.sanitize(state)
	activeCleanup := lifecycleBitsAnd(state.active, cleanup)
	state.active = lifecycleBitsWithout(state.active, activeCleanup)
	state.safe = lifecycleBitsOr(state.safe, activeCleanup)
	return a.sanitize(state)
}

func (a *batchResourceCleanupAnalyzer) cleanupCallMasks(
	call *ast.CallExpr,
) (lifecycleBits, lifecycleBits) {
	cleanup := newLifecycleBits(a.count)
	invalid := newLifecycleBits(a.count)
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return cleanup, invalid
	}
	methodMask := a.methodCandidates[selector.Sel.Name]
	if methodMask == nil {
		return cleanup, invalid
	}

	if identifier, ok := selector.X.(*ast.Ident); ok {
		shapeMask := lifecycleBitsWithout(methodMask, a.bodyCandidates)
		shapeMask = lifecycleBitsAnd(shapeMask, a.variableCandidates[identifier.Name])
		if object := bindingObject(a.info, identifier); object != nil {
			cleanup = lifecycleBitsAnd(shapeMask, a.resourceCandidates[object])
		} else {
			invalid = shapeMask.clone()
		}
		return cleanup, invalid
	}

	bodySelector, ok := selector.X.(*ast.SelectorExpr)
	if !ok || bodySelector.Sel.Name != "Body" {
		return cleanup, invalid
	}
	identifier, ok := bodySelector.X.(*ast.Ident)
	if !ok {
		return cleanup, invalid
	}
	shapeMask := lifecycleBitsAnd(methodMask, a.bodyCandidates)
	shapeMask = lifecycleBitsAnd(shapeMask, a.variableCandidates[identifier.Name])
	if object := bindingObject(a.info, identifier); object != nil {
		cleanup = lifecycleBitsAnd(shapeMask, a.resourceCandidates[object])
	} else {
		invalid = shapeMask.clone()
	}
	return cleanup, invalid
}

func (a *batchResourceCleanupAnalyzer) rejectActiveReassignment(
	expressions []ast.Expr,
	state lifecycleState,
) {
	mask := newLifecycleBits(a.count)
	for _, expression := range expressions {
		mask = lifecycleBitsOr(mask, a.expressionCleanupTargetMask(expression))
	}
	a.invalidate(lifecycleBitsAnd(state.active, mask))
}

func (a *batchResourceCleanupAnalyzer) rejectActiveExit(state lifecycleState) {
	a.invalidate(state.active)
}

func (a *batchResourceCleanupAnalyzer) standardErrorGuardMask(
	statement ast.Stmt,
	candidateMask lifecycleBits,
) lifecycleBits {
	mask := newLifecycleBits(a.count)
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil || !blockAlwaysTerminates(guard.Body) {
		return mask
	}
	for index, candidate := range a.candidates {
		if !candidateMask.has(index) || candidate.errorObject == nil {
			continue
		}
		if conditionChecksErrorNonNil(guard.Cond, a.info, candidate.errorObject) {
			mask.set(index)
		}
	}
	return mask
}
