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
	"go/token"
	"go/types"
)

type lifecycleFlags uint8

const (
	lifecycleUnacquired lifecycleFlags = 1 << iota
	lifecycleActive
	lifecycleSafe
)

type lifecyclePatch struct {
	inherit bool
	values  map[int]lifecycleFlags
}

type lifecycleFlow struct {
	normal    lifecyclePatch
	breaks    lifecyclePatch
	continues lifecyclePatch
}

type lifecycleMutation struct {
	id  int
	old lifecycleFlags
}

type lifecycleObscureEvent struct {
	identifier *ast.Ident
	target     ast.Expr
}

type lifecycleExpressionEvent struct {
	bodyValue    []*ast.Ident
	bodyResponse []*ast.Ident
	contains     []*ast.Ident
	calls        []*ast.Ident
}

type lifecycleEventIndex struct {
	expressions map[ast.Expr]lifecycleExpressionEvent
	obscures    map[ast.Stmt][]lifecycleObscureEvent
}

type batchResourceCleanupAnalyzer struct {
	info                   *types.Info
	candidates             []lifecycleFunctionCandidate
	stats                  *lifecycleAnalysisStats
	flags                  []lifecycleFlags
	invalid                []bool
	reached                []bool
	bodyCandidates         []bool
	active                 map[int]struct{}
	present                map[int]struct{}
	journal                []lifecycleMutation
	acquisitionByStatement map[*ast.AssignStmt][]int
	resourceCandidates     map[types.Object][]int
	variableCandidates     map[string][]int
	methodCandidates       map[string][]int
	bodyAliases            map[types.Object]map[int]struct{}
	events                 *lifecycleEventIndex
}

func newBatchResourceCleanupAnalyzer(
	info *types.Info,
	body *ast.BlockStmt,
	candidates []lifecycleFunctionCandidate,
	stats *lifecycleAnalysisStats,
) *batchResourceCleanupAnalyzer {
	analyzer := &batchResourceCleanupAnalyzer{
		info:                   info,
		candidates:             candidates,
		stats:                  stats,
		flags:                  make([]lifecycleFlags, len(candidates)),
		invalid:                make([]bool, len(candidates)),
		reached:                make([]bool, len(candidates)),
		bodyCandidates:         make([]bool, len(candidates)),
		active:                 make(map[int]struct{}),
		present:                make(map[int]struct{}, len(candidates)),
		acquisitionByStatement: make(map[*ast.AssignStmt][]int),
		resourceCandidates:     make(map[types.Object][]int),
		variableCandidates:     make(map[string][]int),
		methodCandidates:       make(map[string][]int),
		bodyAliases:            make(map[types.Object]map[int]struct{}),
	}
	for index, candidate := range candidates {
		if candidate.assignment == nil || candidate.resourceObject == nil {
			analyzer.invalid[index] = true
			continue
		}
		analyzer.flags[index] = lifecycleUnacquired
		analyzer.present[index] = struct{}{}
		appendLifecycleMapID(analyzer.acquisitionByStatement, candidate.assignment, index)
		appendLifecycleMapID(analyzer.resourceCandidates, candidate.resourceObject, index)
		appendLifecycleMapID(analyzer.variableCandidates, candidate.matcher.variable, index)
		for method := range candidate.matcher.methods {
			appendLifecycleMapID(analyzer.methodCandidates, method, index)
		}
		analyzer.bodyCandidates[index] = candidate.matcher.body
	}
	analyzer.events = newLifecycleEventIndex(body)
	return analyzer
}

func appendLifecycleMapID[K comparable](values map[K][]int, key K, id int) {
	values[key] = append(values[key], id)
}

func newLifecycleEventIndex(body *ast.BlockStmt) *lifecycleEventIndex {
	index := &lifecycleEventIndex{
		expressions: make(map[ast.Expr]lifecycleExpressionEvent),
		obscures:    make(map[ast.Stmt][]lifecycleObscureEvent),
	}
	ast.Inspect(body, func(node ast.Node) bool {
		if expression, ok := node.(ast.Expr); ok {
			index.expressionEvent(expression)
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if statement, ok := node.(ast.Stmt); ok {
			index.obscures[statement] = lifecycleStatementObscures(statement)
		}
		return true
	})
	return index
}

func (i *lifecycleEventIndex) expressionEvent(
	expression ast.Expr,
) lifecycleExpressionEvent {
	if expression == nil {
		return lifecycleExpressionEvent{}
	}
	if event, ok := i.expressions[expression]; ok {
		return event
	}
	// Install an empty value first so nested indexing cannot recurse through
	// parenthesized or otherwise shared expression nodes.
	i.expressions[expression] = lifecycleExpressionEvent{}
	raw := unparenthesizedExpression(expression)
	var event lifecycleExpressionEvent
	if identifier, ok := raw.(*ast.Ident); ok {
		event.bodyValue = []*ast.Ident{identifier}
		event.bodyResponse = []*ast.Ident{identifier}
	}
	if star, ok := raw.(*ast.StarExpr); ok {
		if identifier, ok := unparenthesizedExpression(star.X).(*ast.Ident); ok {
			event.bodyResponse = []*ast.Ident{identifier}
		}
	}
	event.contains = i.containsIdentifiers(raw)
	event.calls = i.callIdentifiers(expression)
	i.expressions[expression] = event
	return event
}

func (i *lifecycleEventIndex) containsIdentifiers(
	expression ast.Expr,
) []*ast.Ident {
	expression = unparenthesizedExpression(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		return []*ast.Ident{identifier}
	}
	var identifiers []*ast.Ident
	switch typed := expression.(type) {
	case *ast.CompositeLit:
		for _, element := range typed.Elts {
			identifiers = appendUniqueLifecycleIdentifiers(
				identifiers,
				i.expressionEvent(element).contains,
			)
		}
	case *ast.KeyValueExpr:
		identifiers = appendUniqueLifecycleIdentifiers(
			identifiers,
			i.expressionEvent(typed.Key).contains,
		)
		identifiers = appendUniqueLifecycleIdentifiers(
			identifiers,
			i.expressionEvent(typed.Value).contains,
		)
	case *ast.UnaryExpr:
		if typed.Op == token.AND {
			identifiers = appendUniqueLifecycleIdentifiers(
				identifiers,
				i.expressionEvent(typed.X).contains,
			)
		}
	}
	return identifiers
}

func (i *lifecycleEventIndex) callIdentifiers(
	expression ast.Expr,
) []*ast.Ident {
	var identifiers []*ast.Ident
	ast.Inspect(expression, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr); ok {
			identifiers = appendUniqueLifecycleIdentifiers(
				identifiers,
				i.expressionEvent(selector.X).bodyResponse,
			)
		}
		for _, argument := range call.Args {
			identifiers = appendUniqueLifecycleIdentifiers(
				identifiers,
				i.expressionEvent(argument).contains,
			)
		}
		return true
	})
	return identifiers
}

func appendUniqueLifecycleIdentifiers(
	destination []*ast.Ident,
	identifiers []*ast.Ident,
) []*ast.Ident {
	for _, identifier := range identifiers {
		found := false
		for _, existing := range destination {
			if existing == identifier {
				found = true
				break
			}
		}
		if !found {
			destination = append(destination, identifier)
		}
	}
	return destination
}

func lifecycleStatementObscures(statement ast.Stmt) []lifecycleObscureEvent {
	var events []lifecycleObscureEvent
	seenIdentifiers := make(map[*ast.Ident]bool)
	seenTargets := make(map[ast.Expr]bool)
	ast.Inspect(statement, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncLit:
			ast.Inspect(typed.Body, func(inner ast.Node) bool {
				identifier, ok := inner.(*ast.Ident)
				if ok && !seenIdentifiers[identifier] {
					seenIdentifiers[identifier] = true
					events = append(events, lifecycleObscureEvent{identifier: identifier})
				}
				return true
			})
			return false
		case *ast.UnaryExpr:
			if typed.Op == token.AND && !seenTargets[typed.X] {
				seenTargets[typed.X] = true
				events = append(events, lifecycleObscureEvent{target: typed.X})
			}
		}
		return true
	})
	return events
}

func sameLifecyclePatch() lifecyclePatch {
	return lifecyclePatch{inherit: true}
}

func emptyLifecyclePatch() lifecyclePatch {
	return lifecyclePatch{}
}

func sameLifecycleFlow() lifecycleFlow {
	return lifecycleFlow{
		normal:    sameLifecyclePatch(),
		breaks:    emptyLifecyclePatch(),
		continues: emptyLifecyclePatch(),
	}
}

func emptyLifecycleFlow() lifecycleFlow {
	return lifecycleFlow{
		normal:    emptyLifecyclePatch(),
		breaks:    emptyLifecyclePatch(),
		continues: emptyLifecyclePatch(),
	}
}

func cloneLifecyclePatch(patch lifecyclePatch) lifecyclePatch {
	cloned := lifecyclePatch{inherit: patch.inherit}
	if len(patch.values) == 0 {
		return cloned
	}
	cloned.values = make(map[int]lifecycleFlags, len(patch.values))
	for id, flags := range patch.values {
		cloned.values[id] = flags
	}
	return cloned
}

func composeLifecyclePatch(
	prefix lifecyclePatch,
	suffix lifecyclePatch,
) lifecyclePatch {
	if !suffix.inherit {
		return cloneLifecyclePatch(suffix)
	}
	result := cloneLifecyclePatch(prefix)
	if len(suffix.values) == 0 {
		return result
	}
	if result.values == nil {
		result.values = make(map[int]lifecycleFlags, len(suffix.values))
	}
	for id, flags := range suffix.values {
		result.values[id] = flags
	}
	return result
}

func composeLifecyclePatchInto(
	prefix *lifecyclePatch,
	suffix lifecyclePatch,
) {
	if !suffix.inherit {
		*prefix = cloneLifecyclePatch(suffix)
		return
	}
	if len(suffix.values) == 0 {
		return
	}
	if prefix.values == nil {
		prefix.values = make(map[int]lifecycleFlags, len(suffix.values))
	}
	for id, flags := range suffix.values {
		prefix.values[id] = flags
	}
}

func composeLifecycleFlow(
	prefix lifecyclePatch,
	flow lifecycleFlow,
) lifecycleFlow {
	return lifecycleFlow{
		normal:    composeLifecyclePatch(prefix, flow.normal),
		breaks:    composeLifecyclePatch(prefix, flow.breaks),
		continues: composeLifecyclePatch(prefix, flow.continues),
	}
}

func (a *batchResourceCleanupAnalyzer) candidateOperation() {
	if a.stats != nil {
		a.stats.CandidateStateOperations++
	}
}

func (a *batchResourceCleanupAnalyzer) flagsFor(id int) lifecycleFlags {
	a.candidateOperation()
	if id < 0 || id >= len(a.flags) || a.invalid[id] {
		return 0
	}
	return a.flags[id]
}

func (a *batchResourceCleanupAnalyzer) setFlags(id int, flags lifecycleFlags) {
	a.candidateOperation()
	if id < 0 || id >= len(a.flags) || a.invalid[id] {
		return
	}
	old := a.flags[id]
	if old == flags {
		return
	}
	a.journal = append(a.journal, lifecycleMutation{id: id, old: old})
	a.flags[id] = flags
	a.updateMembership(id, flags)
}

func (a *batchResourceCleanupAnalyzer) updateMembership(
	id int,
	flags lifecycleFlags,
) {
	if a.invalid[id] || flags == 0 {
		delete(a.present, id)
	} else {
		a.present[id] = struct{}{}
	}
	if a.invalid[id] || flags&lifecycleActive == 0 {
		delete(a.active, id)
	} else {
		a.active[id] = struct{}{}
	}
}

func (a *batchResourceCleanupAnalyzer) checkpoint() int {
	return len(a.journal)
}

func (a *batchResourceCleanupAnalyzer) rollback(checkpoint int) {
	for index := len(a.journal) - 1; index >= checkpoint; index-- {
		mutation := a.journal[index]
		a.candidateOperation()
		a.flags[mutation.id] = mutation.old
		a.updateMembership(mutation.id, mutation.old)
	}
	a.journal = a.journal[:checkpoint]
}

func (a *batchResourceCleanupAnalyzer) patchSince(checkpoint int) lifecyclePatch {
	if checkpoint == len(a.journal) {
		return sameLifecyclePatch()
	}
	oldValues := make(map[int]lifecycleFlags)
	for _, mutation := range a.journal[checkpoint:] {
		a.candidateOperation()
		if _, ok := oldValues[mutation.id]; !ok {
			oldValues[mutation.id] = mutation.old
		}
	}
	patch := sameLifecyclePatch()
	for id, old := range oldValues {
		flags := a.flagsFor(id)
		if flags == old {
			continue
		}
		if patch.values == nil {
			patch.values = make(map[int]lifecycleFlags, len(oldValues))
		}
		patch.values[id] = flags
	}
	return patch
}

func (a *batchResourceCleanupAnalyzer) applyPatch(patch lifecyclePatch) {
	if !patch.inherit {
		removed := make([]int, 0, len(a.present))
		for id := range a.present {
			if _, ok := patch.values[id]; !ok {
				removed = append(removed, id)
			}
		}
		for _, id := range removed {
			a.setFlags(id, 0)
		}
	}
	for id, flags := range patch.values {
		a.setFlags(id, flags)
	}
}

func (a *batchResourceCleanupAnalyzer) patchHasAny(patch lifecyclePatch) bool {
	if !patch.inherit {
		for id, flags := range patch.values {
			if a.flagsFor(id) != 0 && flags != 0 {
				return true
			}
		}
		return false
	}
	count := len(a.present)
	for id, flags := range patch.values {
		old := a.flagsFor(id)
		if old == 0 && flags != 0 {
			count++
		}
		if old != 0 && flags == 0 {
			count--
		}
	}
	return count > 0
}

func (a *batchResourceCleanupAnalyzer) patchFlags(
	patch lifecyclePatch,
	id int,
) lifecycleFlags {
	if a.invalid[id] {
		return 0
	}
	if flags, ok := patch.values[id]; ok {
		a.candidateOperation()
		return flags
	}
	if patch.inherit {
		return a.flagsFor(id)
	}
	return 0
}

func (a *batchResourceCleanupAnalyzer) joinPatches(
	left lifecyclePatch,
	right lifecyclePatch,
) lifecyclePatch {
	result := lifecyclePatch{inherit: left.inherit || right.inherit}
	keys := make(map[int]struct{}, len(left.values)+len(right.values))
	for id := range left.values {
		keys[id] = struct{}{}
	}
	for id := range right.values {
		keys[id] = struct{}{}
	}
	for id := range keys {
		a.candidateOperation()
		if a.invalid[id] {
			continue
		}
		entry := a.flagsFor(id)
		leftFlags := entry
		if !left.inherit {
			leftFlags = 0
		}
		if flags, ok := left.values[id]; ok {
			leftFlags = flags
		}
		rightFlags := entry
		if !right.inherit {
			rightFlags = 0
		}
		if flags, ok := right.values[id]; ok {
			rightFlags = flags
		}
		joined := leftFlags | rightFlags
		baseline := lifecycleFlags(0)
		if result.inherit {
			baseline = entry
		}
		if joined == baseline {
			continue
		}
		if result.values == nil {
			result.values = make(map[int]lifecycleFlags, len(keys))
		}
		result.values[id] = joined
	}
	return result
}

func (a *batchResourceCleanupAnalyzer) joinFlows(
	left lifecycleFlow,
	right lifecycleFlow,
) lifecycleFlow {
	return lifecycleFlow{
		normal:    a.joinPatches(left.normal, right.normal),
		breaks:    a.joinPatches(left.breaks, right.breaks),
		continues: a.joinPatches(left.continues, right.continues),
	}
}

func (a *batchResourceCleanupAnalyzer) joinPatchList(
	patches []lifecyclePatch,
) lifecyclePatch {
	result := emptyLifecyclePatch()
	for _, patch := range patches {
		result = a.joinPatches(result, patch)
	}
	return result
}

func (a *batchResourceCleanupAnalyzer) invalidateID(id int) {
	a.candidateOperation()
	if id < 0 || id >= len(a.invalid) || a.invalid[id] {
		return
	}
	a.invalid[id] = true
	delete(a.present, id)
	delete(a.active, id)
}

func (a *batchResourceCleanupAnalyzer) invalidateIDs(ids map[int]struct{}) {
	for id := range ids {
		a.invalidateID(id)
	}
}

func (a *batchResourceCleanupAnalyzer) invalidateActiveIDs(ids map[int]struct{}) {
	for id := range ids {
		if a.flagsFor(id)&lifecycleActive != 0 {
			a.invalidateID(id)
		}
	}
}

func (a *batchResourceCleanupAnalyzer) invalidateAllPresent() {
	ids := make([]int, 0, len(a.present))
	for id := range a.present {
		ids = append(ids, id)
	}
	for _, id := range ids {
		a.invalidateID(id)
	}
}

func (a *batchResourceCleanupAnalyzer) rejectActiveExit() {
	ids := make([]int, 0, len(a.active))
	for id := range a.active {
		ids = append(ids, id)
	}
	for _, id := range ids {
		a.invalidateID(id)
	}
}

func (a *batchResourceCleanupAnalyzer) prove(body *ast.BlockStmt) []bool {
	flow := a.analyzeBlock(body.List)
	proved := make([]bool, len(a.candidates))
	for id := range a.candidates {
		if a.invalid[id] || !a.reached[id] {
			continue
		}
		if a.patchFlags(flow.breaks, id) != 0 ||
			a.patchFlags(flow.continues, id) != 0 ||
			a.patchFlags(flow.normal, id)&lifecycleActive != 0 {
			continue
		}
		proved[id] = true
	}
	return proved
}

func (a *batchResourceCleanupAnalyzer) analyzeBlock(
	statements []ast.Stmt,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	normal := sameLifecyclePatch()
	var breakPatches []lifecyclePatch
	var continuePatches []lifecyclePatch
	for index := 0; index < len(statements) && len(a.present) > 0; index++ {
		statement := statements[index]
		flow := a.analyzeStatement(statement)
		if !a.advanceBlockFlow(&normal, &breakPatches, &continuePatches, flow) {
			break
		}

		assignment, ok := statement.(*ast.AssignStmt)
		if !ok || index+1 >= len(statements) {
			continue
		}
		acquisitionIDs := a.acquisitionByStatement[assignment]
		if len(acquisitionIDs) == 0 {
			continue
		}
		skipIDs := a.standardErrorGuardIDs(statements[index+1], acquisitionIDs)
		if len(skipIDs) == 0 {
			continue
		}
		guardFlow := a.analyzeErrorGuardSkipping(statements[index+1], skipIDs)
		if !a.advanceBlockFlow(&normal, &breakPatches, &continuePatches, guardFlow) {
			index++
			break
		}
		index++
	}
	result := lifecycleFlow{normal: cloneLifecyclePatch(normal)}
	a.rollback(checkpoint)
	result.breaks = a.joinPatchList(breakPatches)
	result.continues = a.joinPatchList(continuePatches)
	return result
}

func (a *batchResourceCleanupAnalyzer) advanceBlockFlow(
	normal *lifecyclePatch,
	breakPatches *[]lifecyclePatch,
	continuePatches *[]lifecyclePatch,
	flow lifecycleFlow,
) bool {
	if a.patchHasAny(flow.breaks) {
		*breakPatches = append(
			*breakPatches,
			composeLifecyclePatch(*normal, flow.breaks),
		)
	}
	if a.patchHasAny(flow.continues) {
		*continuePatches = append(
			*continuePatches,
			composeLifecyclePatch(*normal, flow.continues),
		)
	}
	hasNormal := a.patchHasAny(flow.normal)
	composeLifecyclePatchInto(normal, flow.normal)
	if !hasNormal {
		return false
	}
	a.applyPatch(flow.normal)
	return len(a.present) > 0
}

func (a *batchResourceCleanupAnalyzer) analyzeStatement(
	statement ast.Stmt,
) lifecycleFlow {
	if statement == nil || len(a.present) == 0 {
		return emptyLifecycleFlow()
	}
	if a.stats != nil {
		a.stats.AnalyzedStatements++
	}
	checkpoint := a.checkpoint()
	a.applyStatementObscures(statement)

	var flow lifecycleFlow
	switch typed := statement.(type) {
	case *ast.BlockStmt:
		flow = a.analyzeBlock(typed.List)
	case *ast.AssignStmt:
		if ids := a.acquisitionByStatement[typed]; len(ids) != 0 {
			flow = a.analyzeAcquisitionAssignment(typed, ids)
		} else {
			a.analyzeAssignment(typed)
			flow = sameLifecycleFlow()
		}
	case *ast.DeclStmt:
		a.analyzeDeclaration(typed)
		flow = sameLifecycleFlow()
	case *ast.ExprStmt:
		a.rejectBodyBindingCall(typed.X)
		a.applyCleanupExpression(typed.X)
		if isDirectPanicCall(typed.X) {
			a.rejectActiveExit()
			flow = emptyLifecycleFlow()
		} else {
			flow = sameLifecycleFlow()
		}
	case *ast.DeferStmt:
		a.applyCleanupCall(typed.Call)
		flow = sameLifecycleFlow()
	case *ast.GoStmt:
		a.rejectBodyBindingCall(typed.Call)
		flow = sameLifecycleFlow()
	case *ast.ReturnStmt:
		a.applyCleanupExpressions(typed.Results)
		a.rejectActiveExit()
		flow = emptyLifecycleFlow()
	case *ast.IfStmt:
		flow = a.analyzeIf(typed)
	case *ast.ForStmt:
		flow = a.analyzeFor(typed)
	case *ast.RangeStmt:
		flow = a.analyzeRange(typed)
	case *ast.SwitchStmt:
		flow = a.analyzeSwitch(typed)
	case *ast.TypeSwitchStmt:
		flow = a.analyzeTypeSwitch(typed)
	case *ast.SelectStmt:
		flow = a.analyzeSelect(typed)
	case *ast.BranchStmt:
		flow = emptyLifecycleFlow()
		switch typed.Tok {
		case token.BREAK:
			flow.breaks = sameLifecyclePatch()
		case token.CONTINUE:
			flow.continues = sameLifecyclePatch()
		default:
			a.invalidateAllPresent()
		}
	case *ast.IncDecStmt:
		a.rejectActiveReassignment([]ast.Expr{typed.X})
		flow = sameLifecycleFlow()
	case *ast.LabeledStmt:
		flow = a.analyzeStatement(typed.Stmt)
	case *ast.SendStmt:
		a.rejectBodyBindingExposure(typed.Value)
		flow = sameLifecycleFlow()
	case *ast.EmptyStmt:
		flow = sameLifecycleFlow()
	default:
		a.invalidateAllPresent()
		flow = emptyLifecycleFlow()
	}

	prefix := a.patchSince(checkpoint)
	a.rollback(checkpoint)
	return composeLifecycleFlow(prefix, flow)
}

func (a *batchResourceCleanupAnalyzer) analyzeAcquisitionAssignment(
	statement *ast.AssignStmt,
	acquisitionIDs []int,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	filter := sameLifecyclePatch()
	filter.values = make(map[int]lifecycleFlags, len(acquisitionIDs))
	for _, id := range acquisitionIDs {
		filter.values[id] = 0
	}
	a.applyPatch(filter)
	otherCheckpoint := a.checkpoint()
	a.analyzeAssignment(statement)
	otherNormal := a.patchSince(otherCheckpoint)
	a.rollback(otherCheckpoint)
	otherNormal = composeLifecyclePatch(filter, otherNormal)
	a.rollback(checkpoint)

	acquired := emptyLifecyclePatch()
	for _, id := range acquisitionIDs {
		flags := a.flagsFor(id)
		if flags == 0 {
			continue
		}
		a.reached[id] = true
		if flags&lifecycleActive != 0 {
			a.invalidateID(id)
			continue
		}
		if flags&(lifecycleUnacquired|lifecycleSafe) == 0 {
			continue
		}
		if acquired.values == nil {
			acquired.values = make(map[int]lifecycleFlags, len(acquisitionIDs))
		}
		acquired.values[id] = lifecycleActive
	}
	return lifecycleFlow{
		normal:    a.joinPatches(otherNormal, acquired),
		breaks:    emptyLifecyclePatch(),
		continues: emptyLifecyclePatch(),
	}
}

func (a *batchResourceCleanupAnalyzer) analyzeErrorGuardSkipping(
	statement ast.Stmt,
	skipIDs []int,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	filter := sameLifecyclePatch()
	filter.values = make(map[int]lifecycleFlags, len(skipIDs))
	preserved := emptyLifecyclePatch()
	preserved.values = make(map[int]lifecycleFlags, len(skipIDs))
	for _, id := range skipIDs {
		flags := a.flagsFor(id)
		if flags == 0 {
			continue
		}
		filter.values[id] = 0
		preserved.values[id] = flags
	}
	a.applyPatch(filter)
	other := a.analyzeStatement(statement)
	other = composeLifecycleFlow(filter, other)
	a.rollback(checkpoint)
	other.normal = a.joinPatches(preserved, other.normal)
	return other
}

func (a *batchResourceCleanupAnalyzer) analyzeAssignment(
	statement *ast.AssignStmt,
) {
	a.observeBodyAliasAssignment(statement)
	a.applyCleanupExpressions(statement.Rhs)
	a.rejectActiveReassignment(statement.Lhs)
}

func (a *batchResourceCleanupAnalyzer) analyzeDeclaration(
	statement *ast.DeclStmt,
) {
	declaration, ok := statement.Decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, specification := range declaration.Specs {
		value, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}
		a.observeBodyAliasDeclaration(value)
		a.applyCleanupExpressions(value.Values)
	}
}

func (a *batchResourceCleanupAnalyzer) applyStatementObscures(statement ast.Stmt) {
	for _, event := range a.events.obscures[statement] {
		var ids map[int]struct{}
		if event.identifier != nil {
			ids = a.identifierMayObscureIDs(event.identifier)
		} else {
			ids = a.expressionCleanupTargetIDs(event.target)
		}
		a.invalidateActiveIDs(ids)
	}
}

func (a *batchResourceCleanupAnalyzer) standardErrorGuardIDs(
	statement ast.Stmt,
	candidateIDs []int,
) []int {
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Init != nil || guard.Else != nil || !blockAlwaysTerminates(guard.Body) {
		return nil
	}
	var ids []int
	for _, id := range candidateIDs {
		a.candidateOperation()
		if id < 0 || id >= len(a.candidates) || a.invalid[id] {
			continue
		}
		candidate := a.candidates[id]
		if candidate.errorObject != nil &&
			conditionChecksErrorNonNil(guard.Cond, a.info, candidate.errorObject) {
			ids = append(ids, id)
		}
	}
	return ids
}

func (a *batchResourceCleanupAnalyzer) analyzeIf(
	statement *ast.IfStmt,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init)
		if a.patchHasAny(initFlow.breaks) || a.patchHasAny(initFlow.continues) {
			a.invalidateAllPresent()
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		if !a.patchHasAny(initFlow.normal) {
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		a.applyPatch(initFlow.normal)
	}
	a.rejectBodyBindingCall(statement.Cond)
	a.applyCleanupExpression(statement.Cond)
	thenFlow := a.analyzeBlock(statement.Body.List)
	elseFlow := sameLifecycleFlow()
	if statement.Else != nil {
		elseFlow = a.analyzeStatement(statement.Else)
	}
	merged := a.joinFlows(thenFlow, elseFlow)
	prefix := a.patchSince(checkpoint)
	a.rollback(checkpoint)
	return composeLifecycleFlow(prefix, merged)
}

func (a *batchResourceCleanupAnalyzer) analyzeFor(
	statement *ast.ForStmt,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init)
		if a.patchHasAny(initFlow.breaks) || a.patchHasAny(initFlow.continues) {
			a.invalidateAllPresent()
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		if !a.patchHasAny(initFlow.normal) {
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		a.applyPatch(initFlow.normal)
	}
	if statement.Cond != nil {
		a.rejectBodyBindingCall(statement.Cond)
	}
	loopFlow := a.analyzeLoop(statement.Body, statement.Post)
	prefix := a.patchSince(checkpoint)
	a.rollback(checkpoint)
	return composeLifecycleFlow(prefix, loopFlow)
}

func (a *batchResourceCleanupAnalyzer) analyzeRange(
	statement *ast.RangeStmt,
) lifecycleFlow {
	a.rejectBodyBindingExposure(statement.X)
	if statement.Tok == token.ASSIGN {
		a.rejectActiveReassignment([]ast.Expr{statement.Key, statement.Value})
	}
	return a.analyzeLoop(statement.Body, nil)
}

func (a *batchResourceCleanupAnalyzer) analyzeLoop(
	body *ast.BlockStmt,
	post ast.Stmt,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	entryPatch := sameLifecyclePatch()
	var breakPatches []lifecyclePatch
	for {
		bodyFlow := a.analyzeBlock(body.List)
		if a.patchHasAny(bodyFlow.breaks) {
			breakPatches = append(
				breakPatches,
				composeLifecyclePatch(entryPatch, bodyFlow.breaks),
			)
		}
		iteration := a.joinPatches(bodyFlow.normal, bodyFlow.continues)
		abnormalPost := false
		if post != nil && a.patchHasAny(iteration) {
			iterationCheckpoint := a.checkpoint()
			a.applyPatch(iteration)
			postFlow := a.analyzeStatement(post)
			if a.patchHasAny(postFlow.breaks) || a.patchHasAny(postFlow.continues) {
				a.invalidateAllPresent()
				abnormalPost = true
			} else {
				iteration = composeLifecyclePatch(iteration, postFlow.normal)
			}
			a.rollback(iterationCheckpoint)
		}
		if abnormalPost {
			break
		}
		nextDelta := a.joinPatches(sameLifecyclePatch(), iteration)
		if nextDelta.inherit && len(nextDelta.values) == 0 {
			break
		}
		a.applyPatch(nextDelta)
		composeLifecyclePatchInto(&entryPatch, nextDelta)
		if len(a.present) == 0 {
			break
		}
	}
	finalEntry := cloneLifecyclePatch(entryPatch)
	a.rollback(checkpoint)
	result := finalEntry
	for _, patch := range breakPatches {
		result = a.joinPatches(result, patch)
	}
	return lifecycleFlow{
		normal:    result,
		breaks:    emptyLifecyclePatch(),
		continues: emptyLifecyclePatch(),
	}
}

func (a *batchResourceCleanupAnalyzer) analyzeSwitch(
	statement *ast.SwitchStmt,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	if statement.Init != nil {
		initFlow := a.analyzeStatement(statement.Init)
		if a.patchHasAny(initFlow.breaks) || a.patchHasAny(initFlow.continues) {
			a.invalidateAllPresent()
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		if !a.patchHasAny(initFlow.normal) {
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		a.applyPatch(initFlow.normal)
	}
	if statement.Tag != nil {
		a.rejectBodyBindingCall(statement.Tag)
	}
	for _, rawClause := range statement.Body.List {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, expression := range clause.List {
			a.rejectBodyBindingCall(expression)
		}
	}
	clauses := a.analyzeCaseClauses(statement.Body.List)
	prefix := a.patchSince(checkpoint)
	a.rollback(checkpoint)
	return composeLifecycleFlow(prefix, clauses)
}

func (a *batchResourceCleanupAnalyzer) analyzeTypeSwitch(
	statement *ast.TypeSwitchStmt,
) lifecycleFlow {
	checkpoint := a.checkpoint()
	for _, initialization := range []ast.Stmt{statement.Init, statement.Assign} {
		if initialization == nil {
			continue
		}
		initFlow := a.analyzeStatement(initialization)
		if a.patchHasAny(initFlow.breaks) || a.patchHasAny(initFlow.continues) {
			a.invalidateAllPresent()
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		if !a.patchHasAny(initFlow.normal) {
			a.rollback(checkpoint)
			return emptyLifecycleFlow()
		}
		a.applyPatch(initFlow.normal)
	}
	clauses := a.analyzeCaseClauses(statement.Body.List)
	prefix := a.patchSince(checkpoint)
	a.rollback(checkpoint)
	return composeLifecycleFlow(prefix, clauses)
}

func (a *batchResourceCleanupAnalyzer) analyzeCaseClauses(
	clauses []ast.Stmt,
) lifecycleFlow {
	result := emptyLifecycleFlow()
	hasDefault := false
	for _, rawClause := range clauses {
		clause, ok := rawClause.(*ast.CaseClause)
		if !ok {
			a.invalidateAllPresent()
			return emptyLifecycleFlow()
		}
		if clause.List == nil {
			hasDefault = true
		}
		clauseFlow := a.analyzeBlock(clause.Body)
		clauseFlow.normal = a.joinPatches(clauseFlow.normal, clauseFlow.breaks)
		clauseFlow.breaks = emptyLifecyclePatch()
		result = a.joinFlows(result, clauseFlow)
	}
	if !hasDefault {
		result.normal = a.joinPatches(result.normal, sameLifecyclePatch())
	}
	return result
}

func (a *batchResourceCleanupAnalyzer) analyzeSelect(
	statement *ast.SelectStmt,
) lifecycleFlow {
	result := sameLifecycleFlow()
	for _, rawClause := range statement.Body.List {
		clause, ok := rawClause.(*ast.CommClause)
		if !ok {
			a.invalidateAllPresent()
			return emptyLifecycleFlow()
		}
		checkpoint := a.checkpoint()
		prefix := sameLifecyclePatch()
		if clause.Comm != nil {
			commFlow := a.analyzeStatement(clause.Comm)
			if a.patchHasAny(commFlow.breaks) || a.patchHasAny(commFlow.continues) {
				a.invalidateAllPresent()
				a.rollback(checkpoint)
				return emptyLifecycleFlow()
			}
			if !a.patchHasAny(commFlow.normal) {
				a.rollback(checkpoint)
				continue
			}
			a.applyPatch(commFlow.normal)
			prefix = a.patchSince(checkpoint)
		}
		clauseFlow := a.analyzeBlock(clause.Body)
		clauseFlow.normal = a.joinPatches(clauseFlow.normal, clauseFlow.breaks)
		clauseFlow.breaks = emptyLifecyclePatch()
		clauseFlow = composeLifecycleFlow(prefix, clauseFlow)
		a.rollback(checkpoint)
		result = a.joinFlows(result, clauseFlow)
	}
	return result
}

func (a *batchResourceCleanupAnalyzer) identifierResourceIDs(
	identifier *ast.Ident,
) map[int]struct{} {
	ids := make(map[int]struct{})
	if identifier == nil {
		return ids
	}
	object := bindingObject(a.info, identifier)
	if object == nil {
		for _, id := range a.variableCandidates[identifier.Name] {
			a.candidateOperation()
			if !a.invalid[id] {
				ids[id] = struct{}{}
			}
		}
		return ids
	}
	for _, id := range a.resourceCandidates[object] {
		a.candidateOperation()
		if !a.invalid[id] && a.candidates[id].matcher.variable == identifier.Name {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func (a *batchResourceCleanupAnalyzer) identifierBodyAliasIDs(
	identifier *ast.Ident,
) map[int]struct{} {
	ids := make(map[int]struct{})
	if identifier == nil {
		return ids
	}
	object := bindingObject(a.info, identifier)
	for id := range a.bodyAliases[object] {
		a.candidateOperation()
		if !a.invalid[id] {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func (a *batchResourceCleanupAnalyzer) identifierBodyResponseIDs(
	identifier *ast.Ident,
) map[int]struct{} {
	ids := a.identifierResourceIDs(identifier)
	for id := range a.identifierBodyAliasIDs(identifier) {
		ids[id] = struct{}{}
	}
	for id := range ids {
		a.candidateOperation()
		if !a.bodyCandidates[id] {
			delete(ids, id)
		}
	}
	return ids
}

func (a *batchResourceCleanupAnalyzer) identifierMayObscureIDs(
	identifier *ast.Ident,
) map[int]struct{} {
	ids := a.identifierResourceIDs(identifier)
	for id := range a.identifierBodyAliasIDs(identifier) {
		ids[id] = struct{}{}
	}
	return ids
}

func (a *batchResourceCleanupAnalyzer) responseIDs(
	identifiers []*ast.Ident,
) map[int]struct{} {
	ids := make(map[int]struct{})
	for _, identifier := range identifiers {
		for id := range a.identifierBodyResponseIDs(identifier) {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func (a *batchResourceCleanupAnalyzer) expressionBodyResponseValueIDs(
	expression ast.Expr,
) map[int]struct{} {
	return a.responseIDs(a.events.expressionEvent(expression).bodyValue)
}

func (a *batchResourceCleanupAnalyzer) expressionBodyResponseIDs(
	expression ast.Expr,
) map[int]struct{} {
	return a.responseIDs(a.events.expressionEvent(expression).bodyResponse)
}

func (a *batchResourceCleanupAnalyzer) expressionContainsBodyResponseIDs(
	expression ast.Expr,
) map[int]struct{} {
	return a.responseIDs(a.events.expressionEvent(expression).contains)
}

func (a *batchResourceCleanupAnalyzer) expressionCallsWithBodyResponseIDs(
	expression ast.Expr,
) map[int]struct{} {
	return a.responseIDs(a.events.expressionEvent(expression).calls)
}

func (a *batchResourceCleanupAnalyzer) expressionCleanupTargetIDs(
	expression ast.Expr,
) map[int]struct{} {
	expression = unparenthesizedExpression(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		return a.identifierResourceIDs(identifier)
	}
	if selector, ok := expression.(*ast.SelectorExpr); ok && selector.Sel.Name == "Body" {
		return a.expressionBodyResponseIDs(selector.X)
	}
	star, ok := expression.(*ast.StarExpr)
	if !ok {
		return make(map[int]struct{})
	}
	identifier, ok := unparenthesizedExpression(star.X).(*ast.Ident)
	if !ok {
		return make(map[int]struct{})
	}
	return a.identifierBodyResponseIDs(identifier)
}

func (a *batchResourceCleanupAnalyzer) observeBodyAliasAssignment(
	statement *ast.AssignStmt,
) {
	a.rejectBodyBindingCalls(statement.Rhs)
	if len(statement.Lhs) != len(statement.Rhs) {
		for _, expression := range statement.Rhs {
			a.invalidateActiveIDs(a.expressionContainsBodyResponseIDs(expression))
		}
		return
	}
	for index, expression := range statement.Rhs {
		ids := a.expressionBodyResponseValueIDs(expression)
		active := a.activeIDs(ids)
		if len(active) != 0 {
			a.bindBodyAlias(statement.Lhs[index], active)
			continue
		}
		a.invalidateActiveIDs(a.expressionContainsBodyResponseIDs(expression))
	}
}

func (a *batchResourceCleanupAnalyzer) observeBodyAliasDeclaration(
	specification *ast.ValueSpec,
) {
	a.rejectBodyBindingCalls(specification.Values)
	if len(specification.Names) != len(specification.Values) {
		for _, expression := range specification.Values {
			a.invalidateActiveIDs(a.expressionContainsBodyResponseIDs(expression))
		}
		return
	}
	for index, expression := range specification.Values {
		ids := a.expressionBodyResponseValueIDs(expression)
		active := a.activeIDs(ids)
		if len(active) != 0 {
			a.bindBodyAlias(specification.Names[index], active)
			continue
		}
		a.invalidateActiveIDs(a.expressionContainsBodyResponseIDs(expression))
	}
}

func (a *batchResourceCleanupAnalyzer) activeIDs(
	ids map[int]struct{},
) map[int]struct{} {
	active := make(map[int]struct{})
	for id := range ids {
		if a.flagsFor(id)&lifecycleActive != 0 {
			active[id] = struct{}{}
		}
	}
	return active
}

func (a *batchResourceCleanupAnalyzer) bindBodyAlias(
	expression ast.Expr,
	ids map[int]struct{},
) {
	identifier, ok := unparenthesizedExpression(expression).(*ast.Ident)
	if !ok {
		a.invalidateIDs(ids)
		return
	}
	if identifier.Name == "_" {
		return
	}
	object := bindingObject(a.info, identifier)
	if object != nil {
		for _, id := range a.resourceCandidates[object] {
			a.candidateOperation()
			delete(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	if object == nil || object.Parent() == nil ||
		object.Pkg() != nil && object.Parent() == object.Pkg().Scope() {
		a.invalidateIDs(ids)
		return
	}
	aliases := a.bodyAliases[object]
	if aliases == nil {
		aliases = make(map[int]struct{})
		a.bodyAliases[object] = aliases
	}
	for id := range ids {
		a.candidateOperation()
		if !a.invalid[id] {
			aliases[id] = struct{}{}
		}
	}
}

func (a *batchResourceCleanupAnalyzer) rejectBodyBindingExposure(
	expression ast.Expr,
) {
	ids := a.expressionContainsBodyResponseIDs(expression)
	for id := range a.expressionCallsWithBodyResponseIDs(expression) {
		ids[id] = struct{}{}
	}
	a.invalidateActiveIDs(ids)
}

func (a *batchResourceCleanupAnalyzer) rejectBodyBindingCall(
	expression ast.Expr,
) {
	a.invalidateActiveIDs(a.expressionCallsWithBodyResponseIDs(expression))
}

func (a *batchResourceCleanupAnalyzer) rejectBodyBindingCalls(
	expressions []ast.Expr,
) {
	for _, expression := range expressions {
		a.rejectBodyBindingCall(expression)
	}
}

func (a *batchResourceCleanupAnalyzer) applyCleanupExpressions(
	expressions []ast.Expr,
) {
	for _, expression := range expressions {
		a.applyCleanupExpression(expression)
	}
}

func (a *batchResourceCleanupAnalyzer) applyCleanupExpression(
	expression ast.Expr,
) {
	call, ok := unparenthesizedExpression(expression).(*ast.CallExpr)
	if !ok {
		return
	}
	a.applyCleanupCall(call)
}

func (a *batchResourceCleanupAnalyzer) applyCleanupCall(call *ast.CallExpr) {
	cleanup, invalid := a.cleanupCallIDs(call)
	for id := range invalid {
		a.invalidateID(id)
	}
	for id := range cleanup {
		flags := a.flagsFor(id)
		if flags&lifecycleActive == 0 {
			continue
		}
		a.setFlags(id, flags&^lifecycleActive|lifecycleSafe)
	}
}

func (a *batchResourceCleanupAnalyzer) cleanupCallIDs(
	call *ast.CallExpr,
) (map[int]struct{}, map[int]struct{}) {
	cleanup := make(map[int]struct{})
	invalid := make(map[int]struct{})
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(a.methodCandidates[selector.Sel.Name]) == 0 {
		return cleanup, invalid
	}

	if identifier, ok := selector.X.(*ast.Ident); ok {
		a.classifyCleanupReceiver(
			identifier,
			selector.Sel.Name,
			false,
			cleanup,
			invalid,
		)
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
	a.classifyCleanupReceiver(
		identifier,
		selector.Sel.Name,
		true,
		cleanup,
		invalid,
	)
	return cleanup, invalid
}

func (a *batchResourceCleanupAnalyzer) classifyCleanupReceiver(
	identifier *ast.Ident,
	method string,
	body bool,
	cleanup map[int]struct{},
	invalid map[int]struct{},
) {
	object := bindingObject(a.info, identifier)
	var ids []int
	if object != nil {
		ids = a.resourceCandidates[object]
	} else {
		ids = a.variableCandidates[identifier.Name]
	}
	for _, id := range ids {
		a.candidateOperation()
		candidate := a.candidates[id]
		if a.invalid[id] || candidate.matcher.body != body ||
			candidate.matcher.variable != identifier.Name ||
			!candidate.matcher.methods[method] {
			continue
		}
		if object == nil {
			invalid[id] = struct{}{}
		} else {
			cleanup[id] = struct{}{}
		}
	}
}

func (a *batchResourceCleanupAnalyzer) rejectActiveReassignment(
	expressions []ast.Expr,
) {
	ids := make(map[int]struct{})
	for _, expression := range expressions {
		for id := range a.expressionCleanupTargetIDs(expression) {
			ids[id] = struct{}{}
		}
	}
	a.invalidateActiveIDs(ids)
}
