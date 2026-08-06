//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmanalyzer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type mockModel struct {
	response string
	err      error
}

func (m *mockModel) Info() model.Info { return model.Info{Name: "mock"} }

func (m *mockModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Content: m.response}},
		},
	}
	close(ch)
	return ch, nil
}

func makeFileChange(filePath string, addedLines []string) types.FileChange {
	var lines []types.Line
	for i, content := range addedLines {
		lines = append(lines, types.Line{
			Type: "+", NewLine: i + 1, Content: "+" + content,
		})
	}
	return types.FileChange{
		FilePath: filePath,
		Hunks:    []types.Hunk{{Lines: lines}},
	}
}

func TestLoadMockFindings_MatchesByFile(t *testing.T) {
	changedFiles := map[string]bool{"worker/pool.go": true}
	findings, err := loadMockFindings("../../testdata/mock_llm_findings.json", changedFiles)
	if err != nil {
		t.Fatalf("loadMockFindings failed: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 mock findings for worker/pool.go, got %d", len(findings))
	}
}

func TestLoadMockFindings_NoMatchReturnsEmpty(t *testing.T) {
	changedFiles := map[string]bool{"other/file.go": true}
	findings, err := loadMockFindings("../../testdata/mock_llm_findings.json", changedFiles)
	if err != nil {
		t.Fatalf("loadMockFindings failed: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for unrelated file, got %d", len(findings))
	}
}

func TestLoadMockFindings_EmptyMatchFileAlwaysMatches(t *testing.T) {
	changedFiles := map[string]bool{"any.go": true}
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "mock.json")
	data := `[{"id":"test","match_file":"","severity":"low","category":"other","file":"x.go","line":1,"title":"t","confidence":0.5}]`
	if err := os.WriteFile(tmpFile, []byte(data), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	findings, err := loadMockFindings(tmpFile, changedFiles)
	if err != nil {
		t.Fatalf("loadMockFindings failed: %v", err)
	}
	if len(findings) != 1 {
		t.Errorf("finding with empty match_file should always match, got %d", len(findings))
	}
}

func TestLoadMockFindings_FileNotFound(t *testing.T) {
	_, err := loadMockFindings("/nonexistent/path/mock.json", nil)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadMockFindings_BadJSON(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "bad.json")
	if err := os.WriteFile(tmpFile, []byte("not json"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := loadMockFindings(tmpFile, nil)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestBuildSystemPrompt_CustomPrompt(t *testing.T) {
	cfg := types.LLMConfig{SystemPrompt: "You are a strict code reviewer."}
	result := buildSystemPrompt(cfg, nil, nil)
	if result != "You are a strict code reviewer." {
		t.Errorf("expected custom prompt, got %q", result)
	}
}

func TestBuildSystemPrompt_DefaultPrompt(t *testing.T) {
	cfg := types.LLMConfig{}
	result := buildSystemPrompt(cfg, nil, nil)
	if !strings.Contains(result, "code review expert") {
		t.Error("default prompt should mention code review expert")
	}
	if !strings.Contains(result, "JSON array") {
		t.Error("default prompt should mention JSON array output")
	}
}

func TestBuildSystemPrompt_IncludesRuleFindings(t *testing.T) {
	cfg := types.LLMConfig{}
	ruleFindings := []types.Finding{
		{File: "a.go", Line: 10, RuleID: "SEC-001", Title: "SQL injection"},
	}
	result := buildSystemPrompt(cfg, nil, ruleFindings)
	if !strings.Contains(result, "Rule engine already found") {
		t.Error("prompt should mention rule engine findings")
	}
	if !strings.Contains(result, "SEC-001") {
		t.Error("prompt should list rule findings")
	}
}

func TestBuildSystemPrompt_IncludesSandboxResults(t *testing.T) {
	cfg := types.LLMConfig{}
	results := []types.SandboxResult{
		{Command: "go_vet", ExitCode: 1, Stdout: "issues found"},
	}
	result := buildSystemPrompt(cfg, results, nil)
	if !strings.Contains(result, "Sandbox tool outputs") {
		t.Error("prompt should include sandbox output section")
	}
}

func TestBuildUserPrompt_BuildsFromChanges(t *testing.T) {
	changes := []types.FileChange{
		makeFileChange("a.go", []string{"func NewFunction() error {", "\treturn nil", "}"}),
	}
	result := buildUserPrompt(changes, 4096)
	if !strings.Contains(result, "### a.go") {
		t.Error("user prompt should contain file header")
	}
	if !strings.Contains(result, "func NewFunction() error {") {
		t.Error("user prompt should contain added lines")
	}
}

func TestBuildUserPrompt_Truncation(t *testing.T) {
	var lines []string
	for i := 0; i < 10000; i++ {
		lines = append(lines, strings.Repeat("x", 100))
	}
	changes := []types.FileChange{makeFileChange("big.go", lines)}
	result := buildUserPrompt(changes, 100)
	if !strings.Contains(result, "... (truncated)") {
		t.Error("large prompt should be truncated")
	}
}

func TestBuildUserPrompt_OnlyAddedLines(t *testing.T) {
	changes := []types.FileChange{{
		FilePath: "a.go",
		Hunks: []types.Hunk{{
			Lines: []types.Line{
				{Type: " ", Content: " old context line"},
				{Type: "+", Content: "+new added line"},
				{Type: "-", Content: "-old removed line"},
			},
		}},
	}}
	result := buildUserPrompt(changes, 4096)
	if strings.Contains(result, "old context") || strings.Contains(result, "old removed") {
		t.Error("user prompt should only contain added lines")
	}
}

func TestBuildUserPrompt_NoChangesEmpty(t *testing.T) {
	result := buildUserPrompt([]types.FileChange{}, 4096)
	if result != "" {
		t.Errorf("empty changes should produce empty prompt, got %q", result)
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input, expected string
		n               int
	}{
		{"hello", "hello", 100},
		{"hello world this is long", "hello worl...", 10},
		{"", "", 10},
		{"abc", "abc", 3},
		{"abcd", "abc...", 3},
	}
	for _, tc := range tests {
		got := truncateStr(tc.input, tc.n)
		if got != tc.expected {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.expected)
		}
	}
}

func TestWithModel_SetsModelInContext(t *testing.T) {
	m := &mockModel{}
	ctx := WithModel(context.Background(), m)
	got := getLLMModel(ctx)
	if got != m {
		t.Error("expected same model instance from context")
	}
}

func TestGetLLMModel_NoModelInContextReturnsNil(t *testing.T) {
	got := getLLMModel(context.Background())
	if got != nil {
		t.Error("expected nil when no model in context")
	}
}

func TestRecordLLMError_AppendsToState(t *testing.T) {
	gs := graph.State{}
	recordLLMError(gs, "test_error", "something went wrong")
	recordLLMError(gs, "another_error", "more details")
	errors, _ := gs[state.StateKeyLLMErrors].([]types.LLMError)
	if len(errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errors))
	}
	if errors[0].ErrorType != "test_error" {
		t.Errorf("expected test_error, got %q", errors[0].ErrorType)
	}
}

func TestRun_MockMode_LoadsFindings(t *testing.T) {
	changes := []types.FileChange{
		makeFileChange("worker/pool.go", []string{
			"func (p *Pool) ProcessAsync(data []byte) {",
			"	go func() { for { result := heavyProcess(data); p.results <- result } }()",
			"}",
		}),
	}
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: true, MockFindingsPath: "../../testdata/mock_llm_findings.json",
		},
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-mock-1",
	}
	result, err := Run(context.Background(), gs)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyLLMFindings].([]types.Finding)
	if len(findings) < 1 {
		t.Fatalf("expected mock findings for worker/pool.go, got %d", len(findings))
	}
	for _, f := range findings {
		if f.TaskID != "task-mock-1" {
			t.Errorf("findings should have task_id set, got %q", f.TaskID)
		}
		if f.ID == "" {
			t.Error("findings should have ID generated")
		}
	}
}

func TestRun_MockMode_BadPathRecordsError(t *testing.T) {
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: true, MockFindingsPath: "/nonexistent/mock.json",
		},
		state.StateKeyFileChanges:    []types.FileChange{makeFileChange("a.go", []string{"func Foo() {}"})},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-bad-mock",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	errors, _ := finalState[state.StateKeyLLMErrors].([]types.LLMError)
	if len(errors) < 1 {
		t.Error("expected LLM error recorded for bad mock path")
	}
	findings, _ := finalState[state.StateKeyLLMFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("bad mock path should produce empty findings, got %d", len(findings))
	}
}

func TestRun_MockMode_EmptyChangesProducesEmptyFindings(t *testing.T) {
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: true, MockFindingsPath: "../../testdata/mock_llm_findings.json",
		},
		state.StateKeyFileChanges:    []types.FileChange{},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-empty",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyLLMFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("empty changes should produce empty findings, got %d", len(findings))
	}
}

func TestRun_LiveMode_NoModelRecordsError(t *testing.T) {
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: false, ModelName: "gpt-4", MaxTokens: 4096, Temperature: 0.1,
		},
		state.StateKeyFileChanges:    []types.FileChange{makeFileChange("a.go", []string{"func Foo() {}"})},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-no-model",
	}
	ctx := context.Background()
	result, _ := Run(ctx, gs)
	finalState := result.(graph.State)
	errors, _ := finalState[state.StateKeyLLMErrors].([]types.LLMError)
	if len(errors) < 1 {
		t.Error("expected LLM error when no model in context")
	}
	if errors[0].ErrorType != "no_model" {
		t.Errorf("expected no_model error, got %q", errors[0].ErrorType)
	}
}

func TestRun_LiveMode_Success(t *testing.T) {
	findingsJSON, _ := json.Marshal([]types.Finding{
		{ID: "llm-001", Severity: "high", Category: "goroutine_leak", File: "a.go", Line: 5,
			Title: "goroutine leak detected", Confidence: 0.85},
	})
	mockLLM := &mockModel{response: string(findingsJSON)}
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: false, ModelName: "test-model", MaxTokens: 4096, Temperature: 0.1,
		},
		state.StateKeyFileChanges: []types.FileChange{
			makeFileChange("a.go", []string{"func handler() {", "	go func() { for {} }()", "}"}),
		},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-live-1",
	}
	ctx := WithModel(context.Background(), mockLLM)
	result, _ := Run(ctx, gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyLLMFindings].([]types.Finding)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding from live mode, got %d", len(findings))
	}
	f := findings[0]
	if f.Source != "llm" || f.DecisionKind != "heuristic" {
		t.Errorf("finding wrong fields: src=%s kind=%s", f.Source, f.DecisionKind)
	}
}

func TestRun_LiveMode_DefaultConfidence(t *testing.T) {
	findingsJSON, _ := json.Marshal([]types.Finding{
		{ID: "x-1", Severity: "low", Category: "style", File: "a.go", Line: 1,
			Title: "style issue", Confidence: 0},
	})
	mockLLM := &mockModel{response: string(findingsJSON)}
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: false, MaxTokens: 4096, Temperature: 0.1,
		},
		state.StateKeyFileChanges:    []types.FileChange{makeFileChange("a.go", []string{"var x = 1"})},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-conf",
	}
	ctx := WithModel(context.Background(), mockLLM)
	result, _ := Run(ctx, gs)
	finalState := result.(graph.State)
	findings, _ := finalState[state.StateKeyLLMFindings].([]types.Finding)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Confidence != 0.5 {
		t.Errorf("zero confidence should default to 0.5, got %f", findings[0].Confidence)
	}
}

func TestRun_LiveMode_LLMError(t *testing.T) {
	mockLLM := &mockModel{err: context.DeadlineExceeded}
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: false, MaxTokens: 4096, Temperature: 0.1,
		},
		state.StateKeyFileChanges:    []types.FileChange{makeFileChange("a.go", []string{"func Foo() {}"})},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-llm-err",
	}
	ctx := WithModel(context.Background(), mockLLM)
	result, _ := Run(ctx, gs)
	finalState := result.(graph.State)
	errors, _ := finalState[state.StateKeyLLMErrors].([]types.LLMError)
	if len(errors) < 1 {
		t.Error("expected LLM error recorded")
	}
	if errors[0].ErrorType != "llm_failure" {
		t.Errorf("expected llm_failure, got %q", errors[0].ErrorType)
	}
	findings, _ := finalState[state.StateKeyLLMFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("expected empty findings on LLM error, got %d", len(findings))
	}
}

func TestRun_LiveMode_BadJSONResponse(t *testing.T) {
	mockLLM := &mockModel{response: "not valid json {{"}
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: false, MaxTokens: 4096, Temperature: 0.1,
		},
		state.StateKeyFileChanges:    []types.FileChange{makeFileChange("a.go", []string{"func Foo() {}"})},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-bad-json",
	}
	ctx := WithModel(context.Background(), mockLLM)
	result, _ := Run(ctx, gs)
	finalState := result.(graph.State)
	errors, _ := finalState[state.StateKeyLLMErrors].([]types.LLMError)
	if len(errors) < 1 {
		t.Error("expected LLM error for bad JSON response")
	}
}

func TestRun_TimingRecorded(t *testing.T) {
	gs := graph.State{
		state.StateKeyLLMConfig:      types.LLMConfig{MockMode: true, MockFindingsPath: "../../testdata/mock_llm_findings.json"},
		state.StateKeyFileChanges:    []types.FileChange{},
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-timing",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)
	ms, ok := finalState[state.StateKeyNodeLLMAnalyzerMs].(int64)
	if !ok || ms < 0 {
		t.Error("node timing not recorded")
	}
}

func TestAnalyzeWithLLM_ZeroMaxTokensDefaults(t *testing.T) {
	mockLLM := &mockModel{response: `[]`}
	cfg := types.LLMConfig{MaxTokens: 0, Temperature: 0.1}
	changes := []types.FileChange{makeFileChange("a.go", []string{"func Foo() {}"})}
	findings, err := analyzeWithLLM(context.Background(), mockLLM, cfg, changes, nil, nil, "task-defaults")
	if err != nil {
		t.Fatalf("analyzeWithLLM should not fail: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings from empty response, got %d", len(findings))
	}
}

func TestRun_RuleOnlyModeSkipsLLM(t *testing.T) {
	changes := []types.FileChange{
		makeFileChange("a.go", []string{"func Foo() {}"}),
	}
	gs := graph.State{
		state.StateKeyLLMConfig: types.LLMConfig{
			RuleOnly: true,
		},
		state.StateKeyFileChanges:    changes,
		state.StateKeySandboxResults: []types.SandboxResult{},
		state.StateKeyRuleFindings:   []types.Finding{},
		state.StateKeyTaskID:         "task-rule-only",
	}
	result, _ := Run(context.Background(), gs)
	finalState := result.(graph.State)

	// LLM findings must be empty
	findings, _ := finalState[state.StateKeyLLMFindings].([]types.Finding)
	if len(findings) != 0 {
		t.Errorf("rule_only mode should produce 0 LLM findings, got %d", len(findings))
	}

	// No errors should be recorded (skip is intentional, not a failure)
	errors, _ := finalState[state.StateKeyLLMErrors].([]types.LLMError)
	if len(errors) != 0 {
		t.Errorf("rule_only mode should not record errors, got %d", len(errors))
	}
}
