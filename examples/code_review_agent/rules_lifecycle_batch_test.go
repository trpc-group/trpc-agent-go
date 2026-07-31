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
	want := lifecycleAnalysisStats{
		ParsedSourceUnits:      1,
		TypeCheckedSourceUnits: 1,
		AnalyzedFunctions:      2,
	}
	if stats != want {
		t.Fatalf("lifecycle analysis stats = %+v, want %+v", stats, want)
	}
}

func TestLifecycleAnalysisBatchesDiffFunctionWindows(t *testing.T) {
	source := lifecycleBatchingSource()
	parsed := parseUnifiedDiff([]byte(lifecycleNewFileDiff(source)))

	matches, stats := runRulesWithLifecycleStats(parsed, "")
	assertNoLifecycleMatches(t, matches)
	want := lifecycleAnalysisStats{
		ParsedSourceUnits:      2,
		TypeCheckedSourceUnits: 2,
		AnalyzedFunctions:      2,
	}
	if stats != want {
		t.Fatalf("lifecycle analysis stats = %+v, want %+v", stats, want)
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
