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
	"fmt"
	"testing"
)

func TestRunMissingTestsRuleUsesPrecomputedIndexInStableOrder(t *testing.T) {
	files := []changedFile{
		{NewPath: "pkg/new.go", IsNew: true},
		{OldPath: "pkg/existing.go", NewPath: "pkg/existing.go"},
		{OldPath: "covered/service.go", NewPath: "covered/service.go"},
		{OldPath: "covered/service_test.go", NewPath: "covered/service_test.go"},
	}
	candidates := []candidateLine{
		{File: "pkg/existing.go", Line: 3, Text: "value := 1", FileIndex: 1},
		{File: "pkg/new.go", Line: 10, Text: "package pkg", FileIndex: 0},
		{File: "pkg/existing.go", Line: 6, Text: "func Exported() {}", FileIndex: 1},
		{File: "pkg/new.go", Line: 11, Text: "func Later() {}", FileIndex: 0},
		{File: "covered/service.go", Line: 4, Text: "func Exported() {}", FileIndex: 2},
		{File: "covered/service_test.go", Line: 4, Text: "func TestExported() {}", FileIndex: 3},
	}

	index := newMissingTestsRuleIndex(files, candidates)
	matches := runMissingTestsRule(files, index)
	if len(matches) != 2 {
		t.Fatalf("match count = %d, want 2: %+v", len(matches), matches)
	}
	if matches[0].File != "pkg/new.go" || matches[0].Line != 10 {
		t.Fatalf("new-file match = %+v, want first indexed candidate", matches[0])
	}
	if matches[1].File != "pkg/existing.go" || matches[1].Line != 6 {
		t.Fatalf("existing-file match = %+v, want first exported candidate", matches[1])
	}
}

func TestRunMissingTestsRuleHandlesHighFileCount(t *testing.T) {
	const (
		productionFileCount = 3072
		testFileCount       = 1024
	)
	files := make([]changedFile, 0, productionFileCount+testFileCount)
	candidates := make([]candidateLine, 0, productionFileCount)
	for i := 0; i < productionFileCount; i++ {
		path := fmt.Sprintf("pkg/%04d/service.go", i)
		files = append(files, changedFile{NewPath: path, IsNew: true})
		candidates = append(candidates, candidateLine{
			File:      path,
			Line:      i + 1,
			Text:      "package service",
			FileIndex: i,
		})
	}
	for i := 0; i < testFileCount; i++ {
		path := fmt.Sprintf("pkg/%04d/service_test.go", i*3)
		files = append(files, changedFile{NewPath: path, IsNew: true})
		candidates = append(candidates, candidateLine{
			File:      path,
			Line:      i + 1,
			Text:      "func TestService() {}",
			FileIndex: productionFileCount + i,
		})
	}

	index := newMissingTestsRuleIndex(files, candidates)
	matches := runMissingTestsRule(files, index)
	wantCount := productionFileCount - testFileCount
	if len(matches) != wantCount {
		t.Fatalf("match count = %d, want %d", len(matches), wantCount)
	}
	matchIndex := 0
	for i := 0; i < productionFileCount; i++ {
		if i%3 == 0 {
			continue
		}
		wantFile := fmt.Sprintf("pkg/%04d/service.go", i)
		if matches[matchIndex].File != wantFile || matches[matchIndex].Line != i+1 {
			t.Fatalf("match %d = %+v, want file %q line %d", matchIndex, matches[matchIndex], wantFile, i+1)
		}
		matchIndex++
	}
}

var benchmarkMissingTestsMatches []ruleMatch

func BenchmarkRunMissingTestsRule(b *testing.B) {
	for _, fileCount := range []int{128, 1024, 8192} {
		b.Run(fmt.Sprintf("files=%d", fileCount), func(b *testing.B) {
			files, candidates := benchmarkMissingTestsInput(fileCount)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				index := newMissingTestsRuleIndex(files, candidates)
				benchmarkMissingTestsMatches = runMissingTestsRule(files, index)
			}
		})
	}
}

func benchmarkMissingTestsInput(fileCount int) ([]changedFile, []candidateLine) {
	files := make([]changedFile, 0, fileCount)
	candidates := make([]candidateLine, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		path := fmt.Sprintf("pkg/%04d/service.go", i)
		files = append(files, changedFile{NewPath: path, IsNew: i%2 == 0})
		text := "package service"
		if i%2 != 0 {
			text = "func Exported() {}"
		}
		candidates = append(candidates, candidateLine{
			File:      path,
			Line:      i + 1,
			Text:      text,
			FileIndex: i,
		})
	}
	return files, candidates
}
