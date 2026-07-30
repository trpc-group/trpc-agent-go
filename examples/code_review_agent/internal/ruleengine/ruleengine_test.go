//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package ruleengine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// ── Helper unit tests ──

func TestStripDiffPrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"+func foo() {", "func foo() {"},
		{"-func bar() {", "func bar() {"},
		{" unchanged", "unchanged"},
		{"no prefix", "no prefix"},
		{"", ""},
	}
	for _, tc := range tests {
		got := stripDiffPrefix(tc.input)
		if got != tc.expected {
			t.Errorf("stripDiffPrefix(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestIsFalsePositiveLine(t *testing.T) {
	tests := []struct {
		line   string
		fp     bool
		reason string
	}{
		{"+", true, "blank added line"},
		{" ", true, "blank context line"},
		{"+// TODO: implement", true, "single-line comment"},
		{"+/* block start", true, "block comment start"},
		{"+ * inner block", true, "block comment inner"},
		{"+package main", true, "package declaration"},
		{"+import (", true, "import block start"},
		{"+import \"fmt\"", true, "single import"},
		{"+\"github.com/example/pkg\"", true, "import path"},
		{"+\"just a string\"", true, "bare string literal"},
		{"+`raw string`", true, "bare raw string"},
		{"+func DoWork() error {", false, "function declaration"},
		{"+return db.Query(query)", false, "return statement"},
		{"+result, err := api.Call()", false, "assignment"},
		{"+if err != nil {", false, "error check"},
	}
	for _, tc := range tests {
		got := isFalsePositiveLine(tc.line)
		if got != tc.fp {
			t.Errorf("%s: isFalsePositiveLine(%q) = %v, want %v", tc.reason, tc.line, got, tc.fp)
		}
	}
}

func TestIsLeakCategory(t *testing.T) {
	tests := []struct {
		category string
		expected bool
	}{
		{"goroutine_leak", true},
		{"resource_leak", true},
		{"db_lifecycle", true},
		{"error_handling", true},
		{"security", false},
		{"sensitive_info", false},
		{"missing_test", false},
		{"other", false},
		{"", false},
	}
	for _, tc := range tests {
		got := isLeakCategory(tc.category)
		if got != tc.expected {
			t.Errorf("isLeakCategory(%q) = %v, want %v", tc.category, got, tc.expected)
		}
	}
}

func TestInferCategoryFromID(t *testing.T) {
	tests := []struct {
		id       string
		expected string
	}{
		{"SEC-001", "security"},
		{"ERR-001", "error_handling"},
		{"SEN-001", "sensitive_info"},
		{"DB-001", "db_lifecycle"},
		{"TEST-001", "missing_test"},
		{"GOR-001", "goroutine_leak"},
		{"RES-001", "resource_leak"},
		{"UNKNOWN-001", "other"},
		{"", "other"},
	}
	for _, tc := range tests {
		got := inferCategoryFromID(tc.id)
		if got != tc.expected {
			t.Errorf("inferCategoryFromID(%q) = %q, want %q", tc.id, got, tc.expected)
		}
	}
}

// ── parseToolOutput tests ──

func TestParseToolOutput_GoVetFormat(t *testing.T) {
	result := types.SandboxResult{
		Command: "go_vet",
		Stdout:  "",
		Stderr:  "a.go:10:5: missing argument for Errorf(\"%v\")\nb.go:20:2: unreachable code\n",
	}
	findings := parseToolOutput("task-1", result)
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings from vet output, got %d", len(findings))
	}
	if findings[0].File != "a.go" || findings[0].Line != 10 {
		t.Errorf("first finding: expected a.go:10, got %s:%d", findings[0].File, findings[0].Line)
	}
	if findings[0].Source != "go_vet" {
		t.Errorf("expected source=go_vet, got %q", findings[0].Source)
	}
	if findings[0].RuleID != "TOOL-VET" {
		t.Errorf("expected RuleID=TOOL-VET, got %q", findings[0].RuleID)
	}
}

func TestParseToolOutput_SandboxError(t *testing.T) {
	result := types.SandboxResult{
		Command:   "go_build",
		ErrorType: "timeout",
		Stderr:    "build timed out after 30s",
	}
	findings := parseToolOutput("task-2", result)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for sandbox error, got %d", len(findings))
	}
	if findings[0].Category != "other" {
		t.Errorf("expected category=other, got %q", findings[0].Category)
	}
	if findings[0].Severity != "warning" {
		t.Errorf("expected severity=warning, got %q", findings[0].Severity)
	}
	if findings[0].RuleID != "TOOL-ERR" {
		t.Errorf("expected RuleID=TOOL-ERR, got %q", findings[0].RuleID)
	}
}

func TestParseToolOutput_BuildErrorPassthrough(t *testing.T) {
	result := types.SandboxResult{
		Command:   "go_build",
		ErrorType: "build_error",
		Stderr:    "main.go:15:2: undefined: someFunc\n",
	}
	findings := parseToolOutput("task-3", result)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding from build error, got %d", len(findings))
	}
	if findings[0].File != "main.go" || findings[0].Line != 15 {
		t.Errorf("expected main.go:15, got %s:%d", findings[0].File, findings[0].Line)
	}
}

func TestParseToolOutput_EmptyOutput(t *testing.T) {
	result := types.SandboxResult{
		Command: "go_vet",
		Stdout:  "",
		Stderr:  "",
	}
	findings := parseToolOutput("task-4", result)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings from empty output, got %d", len(findings))
	}
}

func TestParseToolOutput_StaticcheckMultiLine(t *testing.T) {
	result := types.SandboxResult{
		Command: "staticcheck",
		Stdout:  "pkg/handler.go:42:3: should use time.Since instead of time.Now().Sub\npkg/store.go:99:7: error strings should not be capitalized (ST1005)\n",
	}
	findings := parseToolOutput("task-5", result)
	if len(findings) != 2 {
		t.Fatalf("expected 2 staticcheck findings, got %d", len(findings))
	}
	if findings[1].File != "pkg/store.go" || findings[1].Line != 99 {
		t.Errorf("second finding: expected pkg/store.go:99, got %s:%d", findings[1].File, findings[1].Line)
	}
}

// ── parseRuleMarkdown tests ──

func TestParseRuleMarkdown_SingleRule(t *testing.T) {
	md := `## SEC-001: SQL Injection
- **type**: token
- **severity**: critical
- **pattern**: fmt\.Sprintf.*SELECT
- **message**: "SQL injection risk"
- **fix**: "Use parameterized queries"`
	rules := parseRuleMarkdown(md)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	r := rules[0]
	if r.ID != "SEC-001" {
		t.Errorf("expected ID=SEC-001, got %q", r.ID)
	}
	if r.RuleType != "token" {
		t.Errorf("expected type=token, got %q", r.RuleType)
	}
	if r.Severity != "critical" {
		t.Errorf("expected severity=critical, got %q", r.Severity)
	}
	if r.Pattern != `fmt\.Sprintf.*SELECT` {
		t.Errorf("expected pattern unquoted, got %q", r.Pattern)
	}
	if r.Message != "SQL injection risk" {
		t.Errorf("expected message unquoted, got %q", r.Message)
	}
	if r.Fix != "Use parameterized queries" {
		t.Errorf("expected fix unquoted, got %q", r.Fix)
	}
	if r.Category != "security" {
		t.Errorf("expected category=security, got %q", r.Category)
	}
}

func TestParseRuleMarkdown_MultipleRules(t *testing.T) {
	md := `## SEC-001: SQL Injection
- **type**: token
- **severity**: critical
- **pattern**: fmt\.Sprintf.*SELECT
- **message**: "SQL injection"
- **fix**: "Use params"

## ERR-001: Ignored Error
- **type**: token
- **severity**: high
- **pattern**: _\s*:?=\s*\w+\(
- **message**: "Error ignored"
- **fix**: "Check error"
`
	rules := parseRuleMarkdown(md)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if rules[0].ID != "SEC-001" || rules[1].ID != "ERR-001" {
		t.Errorf("rule IDs: got %q, %q", rules[0].ID, rules[1].ID)
	}
}

func TestParseRuleMarkdown_EmptyContent(t *testing.T) {
	rules := parseRuleMarkdown("")
	if len(rules) != 0 {
		t.Errorf("empty content should return 0 rules, got %d", len(rules))
	}
}

func TestParseRuleMarkdown_NoRules(t *testing.T) {
	md := `# Just a heading
Some text without rule blocks.
- **not**: a rule`
	rules := parseRuleMarkdown(md)
	if len(rules) != 0 {
		t.Errorf("expected 0 rules from non-rule markdown, got %d", len(rules))
	}
}

func TestParseRuleMarkdown_BacktickTrim(t *testing.T) {
	md := "## SEC-005: Test\n- **type**: token\n- **pattern**: `\\.Open\\(`\n- **message**: \"msg\"\n- **fix**: \"fix\""
	rules := parseRuleMarkdown(md)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Pattern != `\.Open\(` {
		t.Errorf("expected pattern without backticks, got %q", rules[0].Pattern)
	}
}

// ── LoadRules integration test ──

func TestLoadRules_FromSkillDirectory(t *testing.T) {
	skillDir := filepath.Join("..", "..", "skills", "code-review")
	rules, err := LoadRules(skillDir, "rules/*.md")
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("expected at least some rules loaded, got 0")
	}
	expectedIDs := map[string]bool{
		"DB-001": true, "DB-002": true, "DB-003": true,
		"ERR-001": true, "ERR-002": true, "ERR-003": true,
		"SEC-001": true, "SEC-002": true, "SEC-003": true,
		"SEN-001": true, "SEN-002": true, "SEN-003": true,
		"TEST-001": true, "TEST-002": true,
	}
	found := make(map[string]bool)
	for _, r := range rules {
		found[r.ID] = true
		if r.ID == "" {
			t.Errorf("rule has empty ID")
		}
		if r.RuleType == "" {
			t.Errorf("rule %s has empty type", r.ID)
		}
		if r.Pattern == "" {
			t.Errorf("rule %s has empty pattern", r.ID)
		}
	}
	for id := range expectedIDs {
		if !found[id] {
			t.Errorf("rule %s not found in loaded rules", id)
		}
	}
}

func TestLoadRules_BadGlob(t *testing.T) {
	_, err := LoadRules("/nonexistent/path", "[invalid-glob")
	if err == nil {
		t.Error("expected error from bad glob, got nil")
	}
}

// ── Run node integration tests ──

func TestRun_TokenRuleMatchSQLInjection(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "repo/user.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 15, Content: "+func SearchUsers(db *sql.DB, keyword string) ([]User, error) {"},
			{Type: "+", NewLine: 16, Content: "+	query := fmt.Sprintf(\"SELECT * FROM users WHERE name LIKE '%%%s%%'\", keyword)"},
			{Type: "+", NewLine: 17, Content: "+	return db.Query(query)"},
			{Type: "+", NewLine: 18, Content: "+}"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "SEC-001", RuleType: "token", Category: "security", Severity: "critical",
			Pattern: `fmt\.Sprintf\s*\(\s*["'][^"]*?SELECT[^"]*?["'].*?\)`,
			Message: "SQL注入风险", Fix: "使用参数化查询"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-sql",
	}
	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 1 {
		t.Fatalf("expected 1 SQL injection finding, got %d", len(findings))
	}
	f := findings[0]
	if f.RuleID != "SEC-001" || f.Confidence != 1.0 || f.Source != "rule_engine" {
		t.Errorf("finding has wrong fields: rule=%s conf=%f src=%s", f.RuleID, f.Confidence, f.Source)
	}
}

func TestRun_TokenRuleMatchHardcodedSecret(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "client/api.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 6, Content: "+const ("},
			{Type: "+", NewLine: 7, Content: "+	APIKey   = \"sk-proj-abc123def456ghi789jkl012mno345pqr\""},
			{Type: "+", NewLine: 8, Content: "+	APISecret = \"my-secret-password-12345\""},
			{Type: "+", NewLine: 9, Content: "+)"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "SEN-001", RuleType: "token", Category: "sensitive_info", Severity: "critical",
			Pattern: `(?i)(api[_-]?key|api[_-]?secret|api[_-]?token|token|secret[_-]?key)\s*[:=]\s*["'][A-Za-z0-9_\-]{12,}["']`,
			Message: "硬编码凭据", Fix: "使用环境变量"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-secret",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) < 1 {
		t.Fatalf("expected at least 1 hardcoded secret finding, got %d", len(findings))
	}
}

func TestRun_TokenRuleMatchIgnoredError(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "handler/login.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 11, Content: "+	_ = validateUsername(username)"},
			{Type: "+", NewLine: 13, Content: "+	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "ERR-001", RuleType: "token", Category: "error_handling", Severity: "high",
			Pattern: `_\s*:?=\s*\w+\(|_, _\s*:?=\s*\w+\(|\w+, _\s*:?=\s*\w+\(`,
			Message: "返回值被丢弃", Fix: "检查error"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-err",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) < 1 {
		t.Fatalf("expected at least 1 ignored-error finding, got %d", len(findings))
	}
}

func TestRun_TokenRuleDoesNotMatchComments(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "api/handler.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 8, Content: "+// The API key should be loaded from env: os.Getenv(\"API_KEY\")"},
			{Type: "+", NewLine: 9, Content: "+// Password validation happens upstream at the auth middleware"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "SEN-001", RuleType: "token", Category: "sensitive_info", Severity: "critical",
			Pattern: `(?i)(api[_-]?key).*["'][A-Za-z0-9_\-]{12,}["']`,
			Message: "硬编码凭据", Fix: "使用环境变量"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-comments",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("comments should be skipped, got %d findings", len(findings))
	}
}

func TestRun_TokenRuleDoesNotMatchEmptyFunc(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "utils/helpers.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 9, Content: "+func IsNotEmpty(s string) bool {"},
			{Type: "+", NewLine: 10, Content: "+	return len(s) > 0"},
			{Type: "+", NewLine: 11, Content: "+}"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "TEST-001", RuleType: "token", Category: "missing_test", Severity: "medium",
			Pattern: `func\s+[A-Z]\w+\(`, Message: "缺少测试", Fix: "编写测试"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-empty",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("trivial func with <4 code lines should NOT trigger TEST-001, got %d findings", len(findings))
	}
}

func TestRun_TokenRuleMatchesFuncWithEnoughLines(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "service/order.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 12, Content: "+func CalculateTotal(ctx context.Context, items []Order, taxRate float64) (float64, error) {"},
			{Type: "+", NewLine: 13, Content: "+	var total float64"},
			{Type: "+", NewLine: 14, Content: "+	for _, item := range items {"},
			{Type: "+", NewLine: 15, Content: "+		total += item.Amount"},
			{Type: "+", NewLine: 16, Content: "+	}"},
			{Type: "+", NewLine: 17, Content: "+	return total, nil"},
			{Type: "+", NewLine: 18, Content: "+}"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "TEST-001", RuleType: "token", Category: "missing_test", Severity: "medium",
			Pattern: `func\s+[A-Z]\w+\(`, Message: "缺少测试", Fix: "编写测试"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-test-001",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) < 1 {
		t.Errorf("expected TEST-001 finding for func with >=4 code lines, got %d", len(findings))
	}
}

func TestRun_TokenRuleIgnoresImportLines(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "cmd/server/main.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 7, Content: "+	\"github.com/example/middleware\""},
			{Type: "+", NewLine: 8, Content: "+	\"golang.org/x/sync/errgroup\""},
		}}},
	}}
	rules := []types.Rule{
		{ID: "SEC-001", RuleType: "token", Category: "security", Severity: "critical",
			Pattern: `\.`, Message: "dot found", Fix: "N/A"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-import",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("import path lines should be filtered, got %d findings", len(findings))
	}
}

func TestRun_EmptyChanges(t *testing.T) {
	rules := []types.Rule{
		{ID: "SEC-001", RuleType: "token", Category: "security", Severity: "critical",
			Pattern: `.*`, Message: "any", Fix: "fix"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    []types.FileChange{},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-empty-changes",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("no changes should produce 0 findings, got %d", len(findings))
	}
}

func TestRun_EmptyRules(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "a.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 1, Content: "+func Foo() {}"},
		}}},
	}}
	gs := graph.State{
		state.StateKeySkillRules:     []types.Rule{},
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-no-rules",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("no rules should produce 0 findings, got %d", len(findings))
	}
}

func TestRun_BrokenRegexPatternSkipsGracefully(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "a.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 1, Content: "+func Foo() {}"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "BROKEN-001", RuleType: "token", Category: "other", Severity: "low",
			Pattern: `[invalid\K`, Message: "broken", Fix: "fix"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-broken",
	}
	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run should not fail on broken regex: %v", err)
	}
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("broken regex should produce 0 findings, got %d", len(findings))
	}
}

func TestRun_TimingRecorded(t *testing.T) {
	gs := graph.State{
		state.StateKeySkillRules:     []types.Rule{},
		state.StateKeyFileChanges:    []types.FileChange{},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-timing",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	ms, ok := finalState[state.StateKeyNodeRuleEngineMs].(int64)
	if !ok {
		t.Error("node timing not recorded in state")
	}
	if ms < 0 {
		t.Errorf("timing should be non-negative, got %d", ms)
	}
}

func TestRun_FindingHasRequiredFields(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "a.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 5, Content: "+func Foo() {}"},
			{Type: "+", NewLine: 6, Content: "+	var x = 1"},
			{Type: "+", NewLine: 7, Content: "+	x++"},
			{Type: "+", NewLine: 8, Content: "+	return"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "TEST-001", RuleType: "token", Category: "missing_test", Severity: "medium",
			Pattern: `func\s+[A-Z]\w+\(`, Message: "缺少测试", Fix: "写测试"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-fields",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) < 1 {
		t.Fatal("expected at least 1 finding")
	}
	f := findings[0]
	if f.ID == "" {
		t.Error("finding.ID should not be empty")
	}
	if f.TaskID != "task-fields" {
		t.Errorf("expected task_id=task-fields, got %q", f.TaskID)
	}
	if f.Source != "rule_engine" {
		t.Errorf("expected source=rule_engine, got %q", f.Source)
	}
	if f.DecisionKind != "deterministic" {
		t.Errorf("expected decision_kind=deterministic, got %q", f.DecisionKind)
	}
}

// ── Leak category tests (scanning removed lines) ──

func TestRun_LeakCategoryScansRemovedLines(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "io/reader.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "-", NewLine: 6, Content: "-	defer f.Close()"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "RES-001", RuleType: "token", Category: "resource_leak", Severity: "high",
			Pattern: `defer\s+\w+\.Close\(\)`,
			Message: "资源泄漏: Close被删除", Fix: "添加 defer Close"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-leak",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) < 1 {
		t.Errorf("leak category should scan removed lines, got 0 findings")
	}
}

func TestRun_NonLeakCategoryIgnoresRemovedLines(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "a.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "-", NewLine: 5, Content: "-	query := fmt.Sprintf(\"SELECT * FROM users\")"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "SEC-001", RuleType: "token", Category: "security", Severity: "critical",
			Pattern: `fmt\.Sprintf.*SELECT`, Message: "SQL injection", Fix: "fix"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-nonleak",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("non-leak category should ignore removed lines, got %d findings", len(findings))
	}
}

func TestRun_ToolRuleIntegration(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "a.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 1, Content: "+func Foo() {}"},
		}}},
	}}
	results := []types.SandboxResult{{
		Command: "go_vet",
		Stdout:  "",
		Stderr:  "a.go:15:3: composite literal uses unkeyed fields\n",
	}}
	gs := graph.State{
		state.StateKeySkillRules:     []types.Rule{},
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: results,
		state.StateKeyTaskID:         "task-tool",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 1 {
		t.Fatalf("expected 1 tool finding from go vet output, got %d", len(findings))
	}
	if findings[0].Source != "go_vet" {
		t.Errorf("expected source=go_vet, got %q", findings[0].Source)
	}
}

func TestRun_NegativeCommentsNotFlagged(t *testing.T) {
	skillDir := filepath.Join("..", "..", "skills", "code-review")
	rules, err := LoadRules(skillDir, "rules/*.md")
	if err != nil || len(rules) == 0 {
		t.Skipf("rules not available: %v", err)
	}
	changes := []types.FileChange{{
		FilePath: "api/handler.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 8, Content: "+// The API key should be loaded from env: os.Getenv(\"API_KEY\")"},
			{Type: "+", NewLine: 9, Content: "+// Password validation happens upstream at the auth middleware"},
		}}},
	}}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-neg-comments",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("comments should produce 0 findings with real rules, got %d", len(findings))
	}
}

func TestRun_NegativeStringLiteralNotFlagged(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "messages/errors.go",
		Hunks: []types.Hunk{{Lines: []types.Line{
			{Type: "+", NewLine: 6, Content: "+	ErrUserNotFound    = errors.New(\"user not found in database\")"},
		}}},
	}}
	rules := []types.Rule{
		{ID: "SEN-002", RuleType: "token", Category: "sensitive_info", Severity: "critical",
			Pattern: `(?i)(password|passwd|pwd)\s*[:=]\s*["'][A-Za-z0-9_\-!@#\$%^&*()]{6,}["']`,
			Message: "硬编码密码", Fix: "从环境变量读取"},
	}
	gs := graph.State{
		state.StateKeySkillRules:     rules,
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyTaskID:         "task-neg-strings",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyRuleFindings].([]types.Finding)
	for _, f := range findings {
		if f.Category == "sensitive_info" {
			t.Errorf("error message strings should not be flagged as sensitive: %s (rule=%s)", f.Title, f.RuleID)
		}
	}
}

func BenchmarkParseToolOutput(b *testing.B) {
	result := types.SandboxResult{
		Command: "go_vet",
		Stderr:  strings.Repeat("a.go:10:5: some warning message here\n", 100),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseToolOutput("bench", result)
	}
}
