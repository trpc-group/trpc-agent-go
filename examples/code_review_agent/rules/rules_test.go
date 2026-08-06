//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

func TestSQLInjectionRule(t *testing.T) {
	rule := NewSQLInjectionRule()

	if rule.ID() != "SEC001" {
		t.Errorf("ID() = %s, want SEC001", rule.ID())
	}

	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{
			name:     "SQL injection with Sprintf",
			content:  `query := fmt.Sprintf("SELECT * FROM users WHERE name = '%s'", username)`,
			expected: 1,
		},
		{
			name:     "Safe query with parameter",
			content:  `rows, err := db.Query("SELECT * FROM users WHERE name = ?", username)`,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &input.DiffFile{Path: "test.go"}
			changes := []input.Change{
				{Type: "add", NewLine: 10, Content: tt.content},
			}

			findings := rule.Check(file, changes)
			if len(findings) != tt.expected {
				t.Errorf("Check() found %d findings, want %d", len(findings), tt.expected)
			}
		})
	}
}

func TestSensitiveInfoRule(t *testing.T) {
	rule := NewSensitiveInfoRule()

	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{
			name:     "api key",
			content:  `api_key = "sk-1234567890abcdef1234567890abcdef"`,
			expected: 1,
		},
		{
			name:     "password",
			content:  `password = "my_secret_password_123"`,
			expected: 1,
		},
		{
			name:     "normal code",
			content:  `fmt.Println("Hello")`,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &input.DiffFile{Path: "test.go"}
			changes := []input.Change{
				{Type: "add", NewLine: 10, Content: tt.content},
			}

			findings := rule.Check(file, changes)
			if len(findings) != tt.expected {
				t.Errorf("Check() found %d findings, want %d", len(findings), tt.expected)
			}
		})
	}
}

func TestGoroutineLeakRule(t *testing.T) {
	rule := &GoroutineLeakRule{}

	file := &input.DiffFile{Path: "test.go"}
	changes := []input.Change{
		{Type: "add", NewLine: 10, Content: "go func() {"},
		{Type: "add", NewLine: 11, Content: "for {"},
	}

	findings := rule.Check(file, changes)
	if len(findings) != 1 {
		t.Errorf("Check() found %d findings, want 1", len(findings))
	}
}

func TestResourceLeakRule(t *testing.T) {
	rule := NewResourceLeakRule()

	tests := []struct {
		name     string
		changes  []input.Change
		expected int
	}{
		{
			name: "file open without close",
			changes: []input.Change{
				{Type: "add", NewLine: 10, Content: `f, err := os.Open("file.txt")`},
			},
			expected: 1,
		},
		{
			name: "file open with close",
			changes: []input.Change{
				{Type: "add", NewLine: 10, Content: `f, err := os.Open("file.txt")`},
				{Type: "add", NewLine: 11, Content: `defer f.Close()`},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &input.DiffFile{Path: "test.go"}
			findings := rule.Check(file, tt.changes)
			if len(findings) != tt.expected {
				t.Errorf("Check() found %d findings, want %d", len(findings), tt.expected)
			}
		})
	}
}

func TestMissingTestRule(t *testing.T) {
	rule := NewMissingTestRule()

	tests := []struct {
		name     string
		file     string
		content  string
		expected int
	}{
		{
			name:     "exported function",
			file:     "math.go",
			content:  `func Add(a, b int) int {`,
			expected: 1,
		},
		{
			name:     "unexported function",
			file:     "math.go",
			content:  `func add(a, b int) int {`,
			expected: 0,
		},
		{
			name:     "test file",
			file:     "math_test.go",
			content:  `func Add(a, b int) int {`,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := &input.DiffFile{Path: tt.file}
			changes := []input.Change{
				{Type: "add", NewLine: 10, Content: tt.content},
			}

			findings := rule.Check(file, changes)
			if len(findings) != tt.expected {
				t.Errorf("Check() found %d findings, want %d", len(findings), tt.expected)
			}
		})
	}
}

func TestDeduplicateFindingsAcrossSources(t *testing.T) {
	findings := []store.Finding{
		{File: "db.go", Line: 10, Category: "security", RuleID: "SEC001", Confidence: 0.95},
		{File: "db.go", Line: 10, Category: "security", RuleID: "AI_SEC001", Confidence: 0.95},
	}
	unique, warnings := DeduplicateFindings(findings)
	if len(unique) != 1 || len(warnings) != 0 {
		t.Fatalf("cross-source dedup = %d unique, %d warnings; want 1, 0", len(unique), len(warnings))
	}
}

func TestDeduplicateFindings(t *testing.T) {
	findings := []store.Finding{
		{File: "db.go", Line: 10, Category: "security", RuleID: "SEC001", Severity: "critical", Confidence: 0.95},
		{File: "db.go", Line: 10, Category: "security", RuleID: "SEC001", Severity: "critical", Confidence: 0.95}, // 重复
		{File: "db.go", Line: 20, Category: "security", RuleID: "SEC001", Severity: "critical", Confidence: 0.95},
		{File: "db.go", Line: 30, Category: "error", RuleID: "ERR001", Severity: "medium", Confidence: 0.50}, // 低置信度
	}

	unique, warnings := DeduplicateFindings(findings)

	if len(unique) != 2 {
		t.Errorf("DeduplicateFindings() got %d unique findings, want 2", len(unique))
	}

	if len(warnings) != 1 {
		t.Errorf("DeduplicateFindings() got %d warnings, want 1", len(warnings))
	}
}

func TestRuleEngine_CheckDiffResult(t *testing.T) {
	engine := NewRuleEngine()

	result := &input.DiffParseResult{
		Files: []input.DiffFile{
			{
				Path: "db.go",
				Hunks: []input.DiffHunk{
					{
						Changes: []input.Change{
							{Type: "add", NewLine: 10, Content: `query := fmt.Sprintf("SELECT * FROM users WHERE name = '%s'", username)`},
						},
					},
				},
			},
		},
	}

	findings := engine.CheckDiffResult("task_001", result)

	if len(findings) == 0 {
		t.Error("expected at least one finding")
	}

	found := false
	for _, f := range findings {
		if f.RuleID == "SEC001" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected SEC001 finding")
	}
}
