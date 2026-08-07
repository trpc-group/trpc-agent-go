//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestFunctionStartLineUsesCodeTokens(t *testing.T) {
	tests := []struct {
		name string
		line diffLine
		want bool
	}{
		{name: "tab separator", line: diffLine{Kind: diffLineAdded, Text: "func\tExported() {}"}, want: true},
		{name: "comment separator", line: diffLine{Kind: diffLineContext, Text: "func/*comment*/Exported() {}"}, want: true},
		{name: "line comment", line: diffLine{Kind: diffLineAdded, Text: "// func Exported() {}"}},
		{name: "string literal", line: diffLine{Kind: diffLineAdded, Text: `"func Exported() {}"`}},
		{name: "explicit semicolon", line: diffLine{Kind: diffLineAdded, Text: "; func Exported() {}"}},
		{name: "deleted line", line: diffLine{Kind: diffLineDeleted, Text: "func Exported() {}"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isFunctionStartLine(test.line); got != test.want {
				t.Fatalf("isFunctionStartLine(%+v) = %t, want %t", test.line, got, test.want)
			}
		})
	}
}

func TestMissingTestsDetectsBodyOnlyExportedBehaviorChanges(t *testing.T) {
	tests := []struct {
		name        string
		before      string
		after       string
		wantLine    int
		wantWarning bool
	}{
		{
			name: "exported function body",
			before: `package pkg

func Exported() int {
	return 1
}
`,
			after: `package pkg

func Exported() int {
	return 2
}
`,
			wantLine:    4,
			wantWarning: true,
		},
		{
			name: "exported method body",
			before: `package pkg

type Service struct{}

func (s *Service) Exported() int {
	return 1
}
`,
			after: `package pkg

type Service struct{}

func (s *Service) Exported() int {
	return 2
}
`,
			wantLine:    6,
			wantWarning: true,
		},
		{
			name:        "tab separated declaration",
			before:      "package pkg\n\nfunc\tExported() int {\n\treturn 1\n}\n",
			after:       "package pkg\n\nfunc\tExported() int {\n\treturn 2\n}\n",
			wantLine:    4,
			wantWarning: true,
		},
		{
			name: "multiline signature",
			before: `package pkg

func Exported(
	value int,
) int {
	return value
}
`,
			after: `package pkg

func Exported(
	value int,
) int {
	return value + 1
}
`,
			wantLine:    6,
			wantWarning: true,
		},
		{
			name: "unexported function body",
			before: `package pkg

func internal() int {
	return 1
}
`,
			after: `package pkg

func internal() int {
	return 2
}
`,
			wantWarning: false,
		},
	}

	for _, mode := range []string{"diff-only", "repository"} {
		for _, test := range tests {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				diff := singleFileReplacementDiff("pkg/api.go", test.before, test.after)
				parsed := parseUnifiedDiff([]byte(diff))
				if len(parsed.Warnings) != 0 {
					t.Fatalf("parse warnings = %+v", parsed.Warnings)
				}
				repoRoot := ""
				if mode == "repository" {
					repoRoot = t.TempDir()
					mustWriteFile(t, filepath.Join(repoRoot, "pkg", "api.go"), test.after)
				}
				warnings := missingTestsWarnings(runRules(parsed, repoRoot))
				if !test.wantWarning {
					if len(warnings) != 0 {
						t.Fatalf("warnings = %+v, want none", warnings)
					}
					return
				}
				if len(warnings) != 1 || warnings[0].Line != test.wantLine {
					t.Fatalf("warnings = %+v, want one at line %d", warnings, test.wantLine)
				}
			})
		}
	}
}

func TestMissingTestsDiffWindowBoundaryBehavior(t *testing.T) {
	t.Run("visible incomplete exported function fails closed", func(t *testing.T) {
		diff := strings.Join([]string{
			"diff --git a/pkg/api.go b/pkg/api.go",
			"--- a/pkg/api.go",
			"+++ b/pkg/api.go",
			"@@ -1,2 +1,2 @@",
			" func Exported() int {",
			"-\treturn 1",
			"+\treturn 2",
		}, "\n")
		warnings := missingTestsWarnings(runRules(parseUnifiedDiff([]byte(diff)), ""))
		if len(warnings) != 1 || warnings[0].Line != 2 {
			t.Fatalf("warnings = %+v, want visible exported-window warning", warnings)
		}
	})

	t.Run("invisible function header is not guessed", func(t *testing.T) {
		diff := strings.Join([]string{
			"diff --git a/pkg/api.go b/pkg/api.go",
			"--- a/pkg/api.go",
			"+++ b/pkg/api.go",
			"@@ -100 +100 @@",
			"-\treturn 1",
			"+\treturn 2",
		}, "\n")
		if warnings := missingTestsWarnings(runRules(parseUnifiedDiff([]byte(diff)), "")); len(warnings) != 0 {
			t.Fatalf("warnings = %+v, want no export guess", warnings)
		}
	})
}

func TestMissingTestsRepositoryParseFailureFallsBackToDiffWindow(t *testing.T) {
	before := "package pkg\n\nfunc\tExported() int {\n\treturn 1\n}\n"
	after := "package pkg\n\nfunc\tExported() int {\n\treturn 2\n}\n"
	parsed := parseUnifiedDiff([]byte(singleFileReplacementDiff("pkg/api.go", before, after)))
	warnings := missingTestsWarnings(runRules(parsed, t.TempDir()))
	if len(warnings) != 1 || warnings[0].Line != 4 {
		t.Fatalf("warnings = %+v, want repository fallback warning at line 4", warnings)
	}
}

func TestMissingTestsBodyChangesPreserveFirstCandidateAndFileOrder(t *testing.T) {
	firstBefore := `package pkg

func First() int {
	return 1
}

func Second() int {
	return 2
}
`
	firstAfter := strings.ReplaceAll(firstBefore, "return 1", "return 10")
	firstAfter = strings.ReplaceAll(firstAfter, "return 2", "return 20")
	secondBefore := `package pkg

func Exported() int {
	return 3
}
`
	secondAfter := strings.ReplaceAll(secondBefore, "return 3", "return 30")
	diff := singleFileReplacementDiff("pkg/z.go", firstBefore, firstAfter) +
		singleFileReplacementDiff("pkg/a.go", secondBefore, secondAfter)
	parsed := parseUnifiedDiff([]byte(diff))
	var warnings []ruleMatch
	for _, match := range runRules(parsed, "") {
		if match.RuleID == ruleMissingTests {
			warnings = append(warnings, match)
		}
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %+v, want two", warnings)
	}
	if warnings[0].File != "pkg/z.go" || warnings[0].Line != 4 ||
		warnings[1].File != "pkg/a.go" || warnings[1].Line != 4 {
		t.Fatalf("warnings = %+v, want first body candidate in diff file order", warnings)
	}
}

func TestMissingTestsAnalysisParsesEachSourceUnitOnce(t *testing.T) {
	const after = `package pkg

func Exported() int {
	value := 1
	value++
	value++
	return value
}
`
	const before = `package pkg

func Exported() int {
	value := 0
	value += 2
	value += 3
	return value - 1
}
`
	parsed := parseUnifiedDiff([]byte(singleFileReplacementDiff("pkg/api.go", before, after)))
	candidates := parsed.candidateLines()
	for _, mode := range []string{"diff-only", "repository"} {
		t.Run(mode, func(t *testing.T) {
			repoRoot := ""
			if mode == "repository" {
				repoRoot = t.TempDir()
				mustWriteFile(t, filepath.Join(repoRoot, "pkg", "api.go"), after)
			}
			exported, stats := analyzeExportedBehaviorCandidates(parsed.Files, repoRoot, candidates)
			if stats.ParsedSourceUnits != 1 {
				t.Fatalf("stats = %+v, want one parsed source unit", stats)
			}
			got := make([]int, 0, len(exported))
			for index, matched := range exported {
				if matched {
					got = append(got, index)
				}
			}
			want := make([]int, len(candidates))
			for index := range want {
				want[index] = index
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("exported candidates = %v, want %v", got, want)
			}
		})
	}
}

func missingTestsWarnings(matches []ruleMatch) []ruleMatch {
	var warnings []ruleMatch
	for _, match := range matches {
		if match.RuleID == ruleMissingTests {
			warnings = append(warnings, match)
		}
	}
	sort.Slice(warnings, func(i int, j int) bool {
		if warnings[i].File != warnings[j].File {
			return warnings[i].File < warnings[j].File
		}
		return warnings[i].Line < warnings[j].Line
	})
	return warnings
}

func singleFileReplacementDiff(path string, before string, after string) string {
	beforeLines := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	afterLines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if len(beforeLines) != len(afterLines) {
		panic("singleFileReplacementDiff requires equal line counts")
	}
	var diff strings.Builder
	diff.WriteString("diff --git a/")
	diff.WriteString(path)
	diff.WriteString(" b/")
	diff.WriteString(path)
	diff.WriteByte('\n')
	diff.WriteString("--- a/")
	diff.WriteString(path)
	diff.WriteByte('\n')
	diff.WriteString("+++ b/")
	diff.WriteString(path)
	diff.WriteByte('\n')
	diff.WriteString("@@ -1,")
	diff.WriteString(strconv.Itoa(len(beforeLines)))
	diff.WriteString(" +1,")
	diff.WriteString(strconv.Itoa(len(afterLines)))
	diff.WriteString(" @@\n")
	for index, beforeLine := range beforeLines {
		afterLine := afterLines[index]
		if beforeLine == afterLine {
			diff.WriteByte(' ')
			diff.WriteString(beforeLine)
			diff.WriteByte('\n')
			continue
		}
		diff.WriteByte('-')
		diff.WriteString(beforeLine)
		diff.WriteByte('\n')
		diff.WriteByte('+')
		diff.WriteString(afterLine)
		diff.WriteByte('\n')
	}
	return diff.String()
}

func TestDeletedOrContentlessTestsDoNotSuppressMissingTests(t *testing.T) {
	production := []string{
		"diff --git a/pkg/api.go b/pkg/api.go",
		"--- a/pkg/api.go",
		"+++ b/pkg/api.go",
		"@@ -1 +1,2 @@",
		" package pkg",
		"+func Exported() {}",
	}
	tests := []struct {
		name     string
		testDiff []string
	}{
		{
			name: "deleted test file",
			testDiff: []string{
				"diff --git a/pkg/api_test.go b/pkg/api_test.go",
				"deleted file mode 100644",
				"--- a/pkg/api_test.go",
				"+++ /dev/null",
				"@@ -1,2 +0,0 @@",
				"-package pkg",
				"-func TestExported() {}",
			},
		},
		{
			name: "test hunk only deletes content",
			testDiff: []string{
				"diff --git a/pkg/api_test.go b/pkg/api_test.go",
				"--- a/pkg/api_test.go",
				"+++ b/pkg/api_test.go",
				"@@ -1,2 +1 @@",
				" package pkg",
				"-func TestExported() {}",
			},
		},
		{
			name: "pure rename into test path",
			testDiff: []string{
				"diff --git a/other/helper.go b/pkg/api_test.go",
				"similarity index 100%",
				"rename from other/helper.go",
				"rename to pkg/api_test.go",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := strings.Join(append(append([]string{}, production...), tt.testDiff...), "\n")
			finalized := finalizeRuleMatches(runRules(parseUnifiedDiff([]byte(diff)), ""))
			if len(finalized.Warnings) != 1 || finalized.Warnings[0].RuleID != ruleMissingTests {
				t.Fatalf("warnings = %+v, want missing-tests warning", finalized.Warnings)
			}
		})
	}
}

func TestReviewableNewTestContentStillSuppressesMissingTests(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/pkg/api.go b/pkg/api.go",
		"--- a/pkg/api.go",
		"+++ b/pkg/api.go",
		"@@ -1 +1,2 @@",
		" package pkg",
		"+func Exported() {}",
		"diff --git a/pkg/api_test.go b/pkg/api_test.go",
		"--- a/pkg/api_test.go",
		"+++ b/pkg/api_test.go",
		"@@ -1 +1,2 @@",
		" package pkg",
		"+func TestExported() {}",
	}, "\n")
	finalized := finalizeRuleMatches(runRules(parseUnifiedDiff([]byte(diff)), ""))
	if len(finalized.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want related test content to suppress missing-tests warning", finalized.Warnings)
	}
}
