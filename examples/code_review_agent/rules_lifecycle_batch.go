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
			stats,
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
	stats *lifecycleAnalysisStats,
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
	analyzer := newBatchResourceCleanupAnalyzer(info, function.Body, functionCandidates, stats)
	proved := analyzer.prove(function.Body)
	for localIndex, candidate := range functionCandidates {
		if proved[localIndex] {
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
