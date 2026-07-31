//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"
)

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
