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
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

type missingTestsAnalysisStats struct {
	ParsedSourceUnits    int
	SourceLinesVisited   int
	CandidatesClassified int
}

type exportedBehaviorSourceCandidate struct {
	index int
	line  int
}

func analyzeExportedBehaviorCandidates(
	files []changedFile,
	repoRoot string,
	candidates []candidateLine,
) ([]bool, missingTestsAnalysisStats) {
	exported := make([]bool, len(candidates))
	classified := make([]bool, len(candidates))
	var indexes []int
	for index, candidate := range candidates {
		if candidate.FileIndex < 0 || candidate.FileIndex >= len(files) {
			continue
		}
		file := files[candidate.FileIndex]
		if file.IsNew || file.IsDeleted || file.IsBinary || !file.isGoFile() ||
			strings.HasSuffix(file.reviewPath(), "_test.go") {
			continue
		}
		indexes = append(indexes, index)
	}
	var stats missingTestsAnalysisStats
	if len(indexes) == 0 {
		return exported, stats
	}
	if repoRoot != "" {
		analyzeRepositoryExportedBehaviorCandidates(
			files,
			repoRoot,
			candidates,
			indexes,
			exported,
			classified,
			&stats,
		)
	}
	analyzeDiffExportedBehaviorCandidates(
		files,
		candidates,
		indexes,
		exported,
		classified,
		&stats,
	)
	return exported, stats
}

func analyzeRepositoryExportedBehaviorCandidates(
	files []changedFile,
	repoRoot string,
	candidates []candidateLine,
	indexes []int,
	exported []bool,
	classified []bool,
	stats *missingTestsAnalysisStats,
) {
	groups := make(map[int][]int)
	var orderedFiles []int
	for _, candidateIndex := range indexes {
		fileIndex := candidates[candidateIndex].FileIndex
		if _, ok := groups[fileIndex]; !ok {
			orderedFiles = append(orderedFiles, fileIndex)
		}
		groups[fileIndex] = append(groups[fileIndex], candidateIndex)
	}

	for _, fileIndex := range orderedFiles {
		filePath := files[fileIndex].reviewPath()
		if filePath == "" {
			continue
		}
		fset := token.NewFileSet()
		stats.ParsedSourceUnits++
		parsedFile, parseErr := parser.ParseFile(
			fset,
			filepath.Join(repoRoot, filepath.FromSlash(filePath)),
			nil,
			parser.AllErrors,
		)
		if parsedFile == nil {
			continue
		}
		sourceCandidates := make([]exportedBehaviorSourceCandidate, 0, len(groups[fileIndex]))
		for _, candidateIndex := range groups[fileIndex] {
			sourceCandidates = append(sourceCandidates, exportedBehaviorSourceCandidate{
				index: candidateIndex,
				line:  candidates[candidateIndex].Line,
			})
		}
		classifyExportedBehaviorCandidates(
			fset,
			parsedFile,
			sourceCandidates,
			exported,
			classified,
			parseErr == nil,
			stats,
		)
	}
}

func analyzeDiffExportedBehaviorCandidates(
	files []changedFile,
	candidates []candidateLine,
	indexes []int,
	exported []bool,
	classified []bool,
	stats *missingTestsAnalysisStats,
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
		if classified[candidateIndex] {
			continue
		}
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
		var functionStarts []int
		for lineIndex, line := range hunk.Lines {
			stats.SourceLinesVisited++
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

			sourceCandidates := make([]exportedBehaviorSourceCandidate, 0, len(windowCandidates[start]))
			for _, candidateIndex := range windowCandidates[start] {
				line, ok := lineMap[candidates[candidateIndex].HunkLineIndex]
				if !ok {
					continue
				}
				sourceCandidates = append(sourceCandidates, exportedBehaviorSourceCandidate{
					index: candidateIndex,
					line:  line,
				})
			}
			if len(sourceCandidates) == 0 {
				continue
			}
			fset := token.NewFileSet()
			stats.ParsedSourceUnits++
			parsedFile, parseErr := parser.ParseFile(
				fset,
				"review_hunk.go",
				source.String(),
				parser.AllErrors,
			)
			if parsedFile != nil {
				classifyExportedBehaviorCandidates(
					fset,
					parsedFile,
					sourceCandidates,
					exported,
					classified,
					parseErr == nil,
					stats,
				)
			}
			if parseErr == nil ||
				!exportedFuncRegex.MatchString(strings.TrimSpace(hunk.Lines[start].Text)) {
				continue
			}
			for _, candidate := range sourceCandidates {
				if classified[candidate.index] {
					continue
				}
				exported[candidate.index] = true
				classified[candidate.index] = true
				stats.CandidatesClassified++
			}
		}
	}
}

func classifyExportedBehaviorCandidates(
	fset *token.FileSet,
	parsedFile *ast.File,
	candidates []exportedBehaviorSourceCandidate,
	exported []bool,
	classified []bool,
	complete bool,
	stats *missingTestsAnalysisStats,
) {
	declarations := make([]*ast.FuncDecl, 0, len(parsedFile.Decls))
	for _, declaration := range parsedFile.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			declarations = append(declarations, function)
		}
	}
	for _, candidate := range candidates {
		if classified[candidate.index] {
			continue
		}
		matched := false
		for _, declaration := range declarations {
			start := fset.Position(declaration.Pos()).Line
			end := fset.Position(declaration.End()).Line
			if candidate.line < start || candidate.line > end {
				continue
			}
			exported[candidate.index] = declaration.Name != nil && declaration.Name.IsExported()
			classified[candidate.index] = true
			stats.CandidatesClassified++
			matched = true
			break
		}
		if !matched && complete {
			classified[candidate.index] = true
			stats.CandidatesClassified++
		}
	}
}
