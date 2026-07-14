//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package policy

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
)

func TestRuleDetector_Detect_MultipleRules(t *testing.T) {
	detector := NewRuleDetector()

	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
new file mode 100644
--- /dev/null
+++ b/pkg/utils/helper.go
@@ -0,0 +1,15 @@
+package utils
+
+func Process() {
+    _ = os.Open("file.txt")
+    go func() {
+        time.Sleep(time.Second)
+    }()
+    ch := make(chan int)
+    if err != nil {}
+    api_key = "sk-1234567890abcdef1234567890abcdef"
+    token := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test"
+    query := fmt.Sprintf("SELECT * FROM users WHERE id=%s", id)
+}
`

	diff, err := parser.ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	findings := detector.Detect(diff)

	if len(findings) == 0 {
		t.Error("Expected to find rule matches")
	}

	ruleIDs := make(map[string]bool)
	for _, f := range findings {
		ruleIDs[f.RuleID] = true
	}

	expectedRules := []string{"GOROUTINE_LEAK", "CHANNEL_UNBUFFERED", "ERROR_IGNORED", "EMPTY_ERROR_CHECK", "SECRET_HARDCODED", "JWT_TOKEN", "SQL_INJECTION"}
	for _, ruleID := range expectedRules {
		if !ruleIDs[ruleID] {
			t.Errorf("Expected to find rule '%s'", ruleID)
		}
	}
}

func TestRuleDetector_Detect_EmptyDiff(t *testing.T) {
	detector := NewRuleDetector()

	diff := &parser.DiffResult{}
	findings := detector.Detect(diff)

	if len(findings) != 0 {
		t.Errorf("Expected 0 findings for empty diff, got %d", len(findings))
	}
}

func TestRuleDetector_Detect_NonGoFile(t *testing.T) {
	detector := NewRuleDetector()

	diffContent := `diff --git a/README.md b/README.md
index abc1234..def5678 100644
--- a/README.md
+++ b/README.md
@@ -1,3 +1,4 @@
 # Project
+api_key = "secret-key"
`

	diff, err := parser.ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	findings := detector.Detect(diff)

	if len(findings) != 0 {
		t.Errorf("Expected 0 findings for non-Go file, got %d", len(findings))
	}
}

func TestRuleDetector_Detect_NoAddedLines(t *testing.T) {
	detector := NewRuleDetector()

	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
index abc1234..def5678 100644
--- a/pkg/utils/helper.go
+++ b/pkg/utils/helper.go
@@ -5,3 +5,0 @@
-func OldFunc() {
-    go func() {}()
-}
`

	diff, err := parser.ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	findings := detector.Detect(diff)

	if len(findings) != 0 {
		t.Errorf("Expected 0 findings when no lines added, got %d", len(findings))
	}
}

func TestRuleDetector_Detect_LineNumbers(t *testing.T) {
	detector := NewRuleDetector()

	diffContent := `diff --git a/pkg/utils/helper.go b/pkg/utils/helper.go
index abc1234..def5678 100644
--- a/pkg/utils/helper.go
+++ b/pkg/utils/helper.go
@@ -10,5 +10,8 @@ package utils
 func Add(a, b int) int {
     return a + b
 }
 
+func Leak() {
+    go func() {}()
+}
`

	diff, err := parser.ParseDiff(diffContent)
	if err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	findings := detector.Detect(diff)

	if len(findings) != 1 {
		t.Fatalf("Expected 1 finding, got %d", len(findings))
	}

	if findings[0].LineNumber != 15 {
		t.Errorf("Expected line number 15, got %d", findings[0].LineNumber)
	}

	if findings[0].RuleID != "GOROUTINE_LEAK" {
		t.Errorf("Expected rule 'GOROUTINE_LEAK', got '%s'", findings[0].RuleID)
	}
}

func TestRuleDetector_DetectInCode_AllRules(t *testing.T) {
	detector := NewRuleDetector()

	testCases := []struct {
		name     string
		code     string
		ruleID   string
		severity storage.FindingSeverity
		category storage.FindingCategory
	}{
		{"goroutine leak", "go func() {}()", "GOROUTINE_LEAK", storage.SeverityHigh, storage.CategoryReliability},
		{"unbuffered channel", "ch := make(chan int)", "CHANNEL_UNBUFFERED", storage.SeverityMedium, storage.CategoryReliability},
		{"error ignored", "_ = os.Open(path)", "ERROR_IGNORED", storage.SeverityMedium, storage.CategoryReliability},
		{"empty error check", "if err != nil {}", "EMPTY_ERROR_CHECK", storage.SeverityMedium, storage.CategoryReliability},
		{"secret hardcoded", `api_key = "sk-1234567890abcdef"`, "SECRET_HARDCODED", storage.SeverityHigh, storage.CategorySecurity},
		{"JWT token", `token = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test"`, "JWT_TOKEN", storage.SeverityHigh, storage.CategorySecurity},
		{"SQL injection", `fmt.Sprintf("SELECT * FROM users WHERE id=%s", id)`, "SQL_INJECTION", storage.SeverityHigh, storage.CategorySecurity},
		{"missing close", "f, _ := os.Open(path)", "MISSING_CLOSE", storage.SeverityMedium, storage.CategoryReliability},
		{"defer in loop", "for i := 0; i < 10; i++ {\n    f, _ := os.Open(path)\n    defer f.Close()\n}", "DEFER_IN_LOOP", storage.SeverityHigh, storage.CategoryReliability},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			findings := detector.DetectInCode(tc.code, "test.go")

			found := false
			for _, f := range findings {
				if f.RuleID == tc.ruleID {
					found = true
					if f.Severity != tc.severity {
						t.Errorf("Expected severity %s, got %s", tc.severity, f.Severity)
					}
					if f.Category != tc.category {
						t.Errorf("Expected category %s, got %s", tc.category, f.Category)
					}
					break
				}
			}

			if !found {
				t.Errorf("Expected to find rule '%s' in code: %q", tc.ruleID, tc.code)
			}
		})
	}
}

func TestRuleDetector_GetRules(t *testing.T) {
	detector := NewRuleDetector()

	rules := detector.GetRules()

	if len(rules) == 0 {
		t.Error("Expected at least one rule")
	}

	ruleIDs := make(map[string]bool)
	for _, rule := range rules {
		ruleIDs[rule.ID] = true
	}

	expectedRules := []string{"GOROUTINE_LEAK", "CHANNEL_UNBUFFERED", "DEFER_IN_LOOP", "ERROR_IGNORED", "SECRET_HARDCODED", "JWT_TOKEN", "SQL_INJECTION"}
	for _, ruleID := range expectedRules {
		if !ruleIDs[ruleID] {
			t.Errorf("Expected to find rule '%s'", ruleID)
		}
	}
}

func TestRemoveDuplicates_Empty(t *testing.T) {
	result := RemoveDuplicates([]storage.Finding{})
	if len(result) != 0 {
		t.Errorf("Expected 0 findings, got %d", len(result))
	}
}

func TestRemoveDuplicates_AllUnique(t *testing.T) {
	findings := []storage.Finding{
		{RuleID: "GOROUTINE_LEAK", Filepath: "file1.go", LineNumber: 10},
		{RuleID: "ERROR_IGNORED", Filepath: "file1.go", LineNumber: 10},
		{RuleID: "GOROUTINE_LEAK", Filepath: "file2.go", LineNumber: 10},
	}

	result := RemoveDuplicates(findings)
	if len(result) != 3 {
		t.Errorf("Expected 3 findings, got %d", len(result))
	}
}
