//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleAnalysisBatchesRepositorySourceUnitsAndFunctions(t *testing.T) {
	source := lifecycleBatchingSource()
	parsed := parseUnifiedDiff([]byte(lifecycleNewFileDiff(source)))
	repoRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(repoRoot, "review.go"), source)

	matches, stats := runRulesWithLifecycleStats(parsed, repoRoot)
	assertNoLifecycleMatches(t, matches)
	assertLifecycleBatchingStats(t, stats, 1, 1, 2)
}

func TestLifecycleAnalysisBatchesDiffFunctionWindows(t *testing.T) {
	source := lifecycleBatchingSource()
	parsed := parseUnifiedDiff([]byte(lifecycleNewFileDiff(source)))

	matches, stats := runRulesWithLifecycleStats(parsed, "")
	assertNoLifecycleMatches(t, matches)
	assertLifecycleBatchingStats(t, stats, 2, 2, 2)
}

func assertLifecycleBatchingStats(
	t *testing.T,
	stats lifecycleAnalysisStats,
	parsed int,
	typeChecked int,
	functions int,
) {
	t.Helper()
	if stats.ParsedSourceUnits != parsed ||
		stats.TypeCheckedSourceUnits != typeChecked ||
		stats.AnalyzedFunctions != functions {
		t.Fatalf(
			"lifecycle batching stats = %+v, want parsed=%d type-checked=%d functions=%d",
			stats,
			parsed,
			typeChecked,
			functions,
		)
	}
}

func TestLifecycleAnalysisSkipsErrorGuardPerCandidate(t *testing.T) {
	source := `package lifecycle

import "os"

func review(firstName string, secondName string) error {
	first, err := os.Open(firstName)
	if err != nil { return err }
	second, err := os.Open(secondName)
	if err != nil { return err }
	defer first.Close()
	defer second.Close()
	return nil
}
`
	parsed := parseUnifiedDiff([]byte(lifecycleNewFileDiff(source)))

	for _, test := range []struct {
		name     string
		repoRoot string
	}{
		{name: "diff-only"},
		{name: "repository", repoRoot: t.TempDir()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.repoRoot != "" {
				mustWriteFile(t, filepath.Join(test.repoRoot, "review.go"), source)
			}
			matches := runRules(parsed, test.repoRoot)
			var lifecycleMatches []ruleMatch
			for _, match := range matches {
				if match.RuleID == ruleUnclosedFile {
					lifecycleMatches = append(lifecycleMatches, match)
				}
			}
			if len(lifecycleMatches) != 1 ||
				!strings.Contains(lifecycleMatches[0].Evidence, "first, err :=") {
				t.Fatalf("lifecycle matches = %+v, want only the first acquisition", lifecycleMatches)
			}
		})
	}
}

func TestLifecycleAnalysisOperationsGrowLinearly(t *testing.T) {
	scenarios := []struct {
		name   string
		source func(int) string
	}{
		{name: "immediate-defer", source: highCandidateLifecycleSource},
		{name: "dense-active", source: denseActiveLifecycleSource},
		{name: "unrelated-statements", source: unrelatedStatementLifecycleSource},
		{name: "sparse-branch-loop", source: sparseBranchLoopLifecycleSource},
	}
	for _, mode := range []string{"repository", "diff-only"} {
		for _, scenario := range scenarios {
			t.Run(mode+"/"+scenario.name, func(t *testing.T) {
				measurements := make(map[int]lifecycleAnalysisStats)
				for _, count := range []int{32, 128, 512, 1024} {
					source := scenario.source(count)
					parsed := parseUnifiedDiff([]byte(lifecycleNewFileDiff(source)))
					repoRoot := ""
					if mode == "repository" {
						repoRoot = t.TempDir()
						mustWriteFile(t, filepath.Join(repoRoot, "review.go"), source)
					}
					matches, stats := runRulesWithLifecycleStats(parsed, repoRoot)
					assertNoLifecycleMatches(t, matches)
					if stats.AnalyzedStatements == 0 || stats.CandidateStateOperations == 0 {
						t.Fatalf("count %d lifecycle stats = %+v, want non-zero operation counters", count, stats)
					}
					measurements[count] = stats
				}
				for _, pair := range [][2]int{{32, 128}, {128, 512}} {
					assertLifecycleOperationGrowth(t, measurements, pair[0], pair[1], 5)
				}
				assertLifecycleOperationGrowth(t, measurements, 512, 1024, 3)
			})
		}
	}
}

func assertLifecycleOperationGrowth(
	t *testing.T,
	measurements map[int]lifecycleAnalysisStats,
	from int,
	to int,
	maximumMultiplier int,
) {
	t.Helper()
	before := measurements[from]
	after := measurements[to]
	if after.AnalyzedStatements > before.AnalyzedStatements*maximumMultiplier {
		t.Fatalf(
			"analyzed statements grew from %d at %d candidates to %d at %d candidates; maximum multiplier %d",
			before.AnalyzedStatements,
			from,
			after.AnalyzedStatements,
			to,
			maximumMultiplier,
		)
	}
	if after.CandidateStateOperations > before.CandidateStateOperations*maximumMultiplier {
		t.Fatalf(
			"candidate operations grew from %d at %d candidates to %d at %d candidates; maximum multiplier %d",
			before.CandidateStateOperations,
			from,
			after.CandidateStateOperations,
			to,
			maximumMultiplier,
		)
	}
}

func BenchmarkLifecycleAnalysisHighCandidateCount(b *testing.B) {
	for _, mode := range []string{"repository", "diff-only"} {
		for _, count := range []int{32, 128, 512, 1024} {
			b.Run(fmt.Sprintf("%s/%d", mode, count), func(b *testing.B) {
				source := highCandidateLifecycleSource(count)
				parsed := parseUnifiedDiff([]byte(lifecycleNewFileDiff(source)))
				repoRoot := ""
				if mode == "repository" {
					repoRoot = b.TempDir()
					mustWriteBenchmarkFile(b, filepath.Join(repoRoot, "review.go"), source)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for iteration := 0; iteration < b.N; iteration++ {
					lifecycleBenchmarkMatches, lifecycleBenchmarkStats =
						runRulesWithLifecycleStats(parsed, repoRoot)
				}
			})
		}
	}
}

var (
	lifecycleBenchmarkMatches []ruleMatch
	lifecycleBenchmarkStats   lifecycleAnalysisStats
)

func lifecycleBatchingSource() string {
	return `package lifecycle

import "os"

func reviewFirst(firstName string, secondName string) error {
	first, err := os.Open(firstName)
	if err != nil { return err }
	defer first.Close()
	second, err := os.Create(secondName)
	if err != nil { return err }
	defer second.Close()
	return nil
}

func reviewSecond(name string) error {
	third, err := os.Open(name)
	if err != nil { return err }
	defer third.Close()
	return nil
}
`
}

func highCandidateLifecycleSource(count int) string {
	var source strings.Builder
	source.WriteString("package lifecycle\n\nimport \"os\"\n\nfunc review() error {\n")
	for index := 0; index < count; index++ {
		fmt.Fprintf(&source, "\tfile%d, err%d := os.Open(%q)\n", index, index, fmt.Sprint(index))
		fmt.Fprintf(&source, "\tif err%d != nil { return err%d }\n", index, index)
		fmt.Fprintf(&source, "\tdefer file%d.Close()\n", index)
	}
	source.WriteString("\treturn nil\n}\n")
	return source.String()
}

func denseActiveLifecycleSource(count int) string {
	var source strings.Builder
	source.WriteString("package lifecycle\n\nimport \"os\"\n\nfunc review() error {\n")
	for index := 0; index < count; index++ {
		fmt.Fprintf(&source, "\tfile%d, _ := os.Open(%q)\n", index, fmt.Sprint(index))
	}
	for index := 0; index < count; index++ {
		fmt.Fprintf(&source, "\tdefer file%d.Close()\n", index)
	}
	source.WriteString("\treturn nil\n}\n")
	return source.String()
}

func unrelatedStatementLifecycleSource(count int) string {
	var source strings.Builder
	source.WriteString("package lifecycle\n\nimport \"os\"\n\nfunc review() error {\n\tvalue := 0\n")
	for index := 0; index < count; index++ {
		fmt.Fprintf(&source, "\tfile%d, _ := os.Open(%q)\n", index, fmt.Sprint(index))
		fmt.Fprintf(&source, "\tdefer file%d.Close()\n", index)
		fmt.Fprintf(&source, "\tvalue += %d\n", index)
	}
	source.WriteString("\t_ = value\n\treturn nil\n}\n")
	return source.String()
}

func sparseBranchLoopLifecycleSource(count int) string {
	var source strings.Builder
	source.WriteString("package lifecycle\n\nimport \"os\"\n\nfunc review(flag bool) error {\n")
	source.WriteString("\tfile0, _ := os.Open(\"0\")\n")
	source.WriteString("\tif flag { _ = file0.Close() } else { _ = file0.Close() }\n")
	source.WriteString("\tfile1, _ := os.Open(\"1\")\n")
	source.WriteString("\tfor flag { _ = file1.Close(); break }\n")
	source.WriteString("\tdefer file1.Close()\n")
	for index := 2; index < count; index++ {
		fmt.Fprintf(&source, "\tfile%d, _ := os.Open(%q)\n", index, fmt.Sprint(index))
		fmt.Fprintf(&source, "\tdefer file%d.Close()\n", index)
	}
	source.WriteString("\treturn nil\n}\n")
	return source.String()
}

func lifecycleNewFileDiff(source string) string {
	lines := strings.Split(strings.TrimSpace(source), "\n")
	var added strings.Builder
	for _, line := range lines {
		added.WriteByte('+')
		added.WriteString(line)
		added.WriteByte('\n')
	}
	return fmt.Sprintf(
		"diff --git a/review.go b/review.go\nnew file mode 100644\n--- /dev/null\n+++ b/review.go\n@@ -0,0 +1,%d @@\n%s",
		len(lines),
		added.String(),
	)
}

func assertNoLifecycleMatches(t *testing.T, matches []ruleMatch) {
	t.Helper()
	for _, match := range matches {
		switch match.RuleID {
		case ruleUnclosedFile, ruleUnclosedHTTPBody, ruleUnclosedSQLRows,
			ruleDatabaseTxLifecycle, ruleDatabaseOpenLifecycle:
			t.Fatalf("unexpected lifecycle match: %+v", match)
		}
	}
}

func mustWriteBenchmarkFile(b *testing.B, path string, content string) {
	b.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		b.Fatalf("write benchmark source: %v", err)
	}
}
