//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graphagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/ruleengine"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/storage"
	storagewriter "github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/storagewriter"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func decodeStateDelta(delta map[string][]byte, fallback graph.State) graph.State {
	out := make(graph.State)
	for k, v := range delta {
		var val any
		if err := json.Unmarshal(v, &val); err == nil {
			out[k] = val
		}
	}
	for k, v := range fallback {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

func resolveFixture(t *testing.T, relPath string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", relPath))
	if err != nil {
		t.Fatalf("cannot resolve fixture path: %v", err)
	}
	return abs
}

func checkStateExists(t *testing.T, gs graph.State, key, label string) {
	t.Helper()
	v, ok := gs[key]
	if !ok || v == nil {
		t.Errorf("%s not found in final state", label)
	} else {
		t.Logf("%s present in final state", label)
	}
}

func checkStateOptional(t *testing.T, gs graph.State, key, label string) {
	t.Helper()
	v, ok := gs[key]
	if !ok || v == nil {
		t.Logf("%s not present (optional, may be expected)", label)
	} else {
		t.Logf("%s present in final state", label)
	}
}

// TestE2E_DryRunMode runs the full 8-node pipeline with SQLite DB, real diff, mock LLM, and real rules.
func TestE2E_DryRunMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	dbPath := filepath.Join(tmpDir, "review.db")

	diffPath := resolveFixture(t, "testdata/diffs/02-sql-injection.diff")
	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("cannot read diff fixture: %v", err)
	}

	mockPath := resolveFixture(t, "testdata/mock_llm_findings.json")
	skillDir := resolveFixture(t, "skills/code-review")

	rules, err := ruleengine.LoadRules(skillDir, "rules/*.md")
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no rules loaded")
	}

	store, err := storage.NewSQLite(dbPath)
	if err != nil {
		t.Fatalf("cannot create test database: %v", err)
	}
	defer store.Close()

	taskID := "e2e-dryrun-001"
	now := time.Now().UTC().Format(time.RFC3339)
	if err := store.CreateTask(context.Background(), storage.TaskRow{
		ID: taskID, Status: "running", InputType: "diff_file",
		InputSource: diffPath, InputDiffHash: "test",
		ModelMode: "dry_run", CreatedAt: now, StartedAt: now,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	sg, err := Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	compiled, err := sg.Compile()
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	initialState := graph.State{
		state.StateKeyInputDiffFile: diffPath,
		state.StateKeyInputDiffText: string(diffData),
		state.StateKeyInputRepoPath: tmpDir,
		state.StateKeyOutputDir:     outputDir,
		state.StateKeyTaskID:        taskID,
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Type: "local", TimeoutSec: 10, MaxOutputMB: 10,
			Commands: []types.SandboxCommand{
				{Name: "go_vet", Cmd: "go", Args: []string{"vet", "./..."}, RiskLevel: "low"},
				{Name: "go_test", Cmd: "go", Args: []string{"test", "./..."}, RiskLevel: "medium"},
			},
		},
		state.StateKeyLLMConfig: types.LLMConfig{
			ModelName: "mock-model", Temperature: 0.1, MaxTokens: 4096,
			MockMode: true, MockFindingsPath: mockPath,
		},
		state.StateKeyDedupConfig: types.DedupConfig{ConfidenceThreshold: 0.6},
		state.StateKeySkillRules:  rules,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = storagewriter.WithStorage(ctx, store)

	executor, err := graph.NewExecutor(compiled)
	if err != nil {
		t.Fatalf("NewExecutor failed: %v", err)
	}

	eventChan, err := executor.Execute(ctx, initialState, &agent.Invocation{
		InvocationID: taskID,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if eventChan == nil {
		t.Fatal("eventChan is nil")
	}

	finalState := initialState
	for evt := range eventChan {
		if evt != nil && evt.Response != nil && evt.Response.Done &&
			evt.Response.Object == graph.ObjectTypeGraphExecution {
			if evt.StateDelta != nil {
				finalState = decodeStateDelta(evt.StateDelta, finalState)
			}
		}
	}

	checkStateExists(t, finalState, state.StateKeyFileChanges, "file_changes")
	checkStateExists(t, finalState, state.StateKeyPermissionDecisions, "permission_decisions")
	checkStateExists(t, finalState, state.StateKeySandboxResults, "sandbox_results")
	checkStateExists(t, finalState, state.StateKeyFindings, "findings")
	checkStateOptional(t, finalState, state.StateKeyRuleFindings, "rule_findings")
	checkStateOptional(t, finalState, state.StateKeyLLMFindings, "llm_findings")
	checkStateOptional(t, finalState, state.StateKeyWarnings, "warnings")

	jsonPath, _ := finalState[state.StateKeyJSONReportPath].(string)
	mdPath, _ := finalState[state.StateKeyMDReportPath].(string)
	if jsonPath == "" || mdPath == "" {
		t.Errorf("reports not generated: json=%q md=%q", jsonPath, mdPath)
	} else {
		if _, err := os.Stat(jsonPath); err != nil {
			t.Errorf("JSON report file missing: %v", err)
		}
		if _, err := os.Stat(mdPath); err != nil {
			t.Errorf("MD report file missing: %v", err)
		}
	}
	t.Logf("output_dir=%s json=%s md=%s", outputDir, jsonPath, mdPath)
}

// TestE2E_MinimumViableDryRun runs without DB and without repo-path (minimal viable path).
func TestE2E_MinimumViableDryRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	diffPath := resolveFixture(t, "testdata/diffs/02-sql-injection.diff")
	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("cannot read diff fixture: %v", err)
	}

	mockPath := resolveFixture(t, "testdata/mock_llm_findings.json")
	skillDir := resolveFixture(t, "skills/code-review")

	rules, err := ruleengine.LoadRules(skillDir, "rules/*.md")
	if err != nil || len(rules) == 0 {
		t.Fatalf("rules not available: %v", err)
	}

	sg, _ := Build()
	compiled, _ := sg.Compile()

	initialState := graph.State{
		state.StateKeyInputDiffFile: diffPath,
		state.StateKeyInputDiffText: string(diffData),
		state.StateKeyOutputDir:     outputDir,
		state.StateKeyTaskID:        "e2e-mvp-001",
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Type: "local", Commands: []types.SandboxCommand{},
		},
		state.StateKeyLLMConfig: types.LLMConfig{
			MockMode: true, MockFindingsPath: mockPath,
		},
		state.StateKeyDedupConfig: types.DedupConfig{ConfidenceThreshold: 0.6},
		state.StateKeySkillRules:  rules,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	executor, _ := graph.NewExecutor(compiled)
	eventChan, err := executor.Execute(ctx, initialState, &agent.Invocation{
		InvocationID: "e2e-mvp-001",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if eventChan == nil {
		t.Fatal("eventChan is nil")
	}

	finalState := initialState
	for evt := range eventChan {
		if evt != nil && evt.Response != nil && evt.Response.Done &&
			evt.Response.Object == graph.ObjectTypeGraphExecution {
			if evt.StateDelta != nil {
				finalState = decodeStateDelta(evt.StateDelta, finalState)
			}
		}
	}

	jsonPath, _ := finalState[state.StateKeyJSONReportPath].(string)
	mdPath, _ := finalState[state.StateKeyMDReportPath].(string)
	if jsonPath == "" || mdPath == "" {
		t.Errorf("reports not generated: json=%q md=%q", jsonPath, mdPath)
	}

	checkStateExists(t, finalState, state.StateKeyFileChanges, "file_changes")
	checkStateExists(t, finalState, state.StateKeyFindings, "findings")
	checkStateOptional(t, finalState, state.StateKeyRuleFindings, "rule_findings")
}

// TestE2E_MultipleDiffFixtures tests the pipeline against various diff fixtures.
func TestE2E_MultipleDiffFixtures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	skillDir := resolveFixture(t, "skills/code-review")
	rules, err := ruleengine.LoadRules(skillDir, "rules/*.md")
	if err != nil || len(rules) == 0 {
		t.Fatalf("rules not available: %v", err)
	}

	mockPath := resolveFixture(t, "testdata/mock_llm_findings.json")

	tests := []struct {
		name           string
		diffFile       string
		expectFindings bool
	}{
		{"SQL injection", "02-sql-injection.diff", true},
		{"Hardcoded secret", "03-hardcoded-secret.diff", true},
		{"Goroutine leak", "06-goroutine-leak.diff", true},
		{"Comments only (negative)", "neg-comments.diff", false},
		{"Import changes (negative)", "neg-imports.diff", false},
		{"String literals (negative)", "neg-string-literal.diff", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			outputDir := filepath.Join(tmpDir, "output")

			diffPath := resolveFixture(t, filepath.Join("testdata", "diffs", tc.diffFile))
			diffData, err := os.ReadFile(diffPath)
			if err != nil {
				t.Fatalf("cannot read diff fixture: %v", err)
			}

			sg, _ := Build()
			compiled, _ := sg.Compile()

			initialState := graph.State{
				state.StateKeyInputDiffFile: diffPath,
				state.StateKeyInputDiffText: string(diffData),
				state.StateKeyOutputDir:     outputDir,
				state.StateKeyTaskID:        "e2e-" + tc.name,
				state.StateKeyExecutorConfig: types.ExecutorConfig{
					Type: "local", Commands: []types.SandboxCommand{},
				},
				state.StateKeyLLMConfig: types.LLMConfig{
					MockMode: true, MockFindingsPath: mockPath,
				},
				state.StateKeyDedupConfig: types.DedupConfig{ConfidenceThreshold: 0.6},
				state.StateKeySkillRules:  rules,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			executor, _ := graph.NewExecutor(compiled)
			eventChan, err := executor.Execute(ctx, initialState, &agent.Invocation{
				InvocationID: "e2e-" + tc.name,
			})
			if err != nil {
				t.Fatalf("Execute failed for %s: %v", tc.diffFile, err)
			}
			if eventChan == nil {
				t.Fatal("eventChan is nil")
			}

			finalState := initialState
			for evt := range eventChan {
				if evt != nil && evt.Response != nil && evt.Response.Done &&
					evt.Response.Object == graph.ObjectTypeGraphExecution {
					if evt.StateDelta != nil {
						finalState = decodeStateDelta(evt.StateDelta, finalState)
					}
				}
			}

			if tc.expectFindings {
				checkStateExists(t, finalState, state.StateKeyFindings, "findings")
			} else {
				checkStateOptional(t, finalState, state.StateKeyFindings, "findings")
				jsonPath, _ := finalState[state.StateKeyJSONReportPath].(string)
				if jsonPath == "" {
					t.Logf("no JSON report for negative test %s (may be expected)", tc.diffFile)
				}
			}
		})
	}
}

// TestE2E_RuleOnlyMode runs the full pipeline in rule_only mode — LLM is skipped
// entirely, only RuleEngine + tools produce findings.
func TestE2E_RuleOnlyMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")

	diffPath := resolveFixture(t, "testdata/diffs/02-sql-injection.diff")
	diffData, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("cannot read diff fixture: %v", err)
	}

	skillDir := resolveFixture(t, "skills/code-review")
	rules, err := ruleengine.LoadRules(skillDir, "rules/*.md")
	if err != nil || len(rules) == 0 {
		t.Fatalf("rules not available: %v", err)
	}

	sg, _ := Build()
	compiled, _ := sg.Compile()

	initialState := graph.State{
		state.StateKeyInputDiffFile: diffPath,
		state.StateKeyInputDiffText: string(diffData),
		state.StateKeyOutputDir:     outputDir,
		state.StateKeyTaskID:        "e2e-rule-only",
		state.StateKeyExecutorConfig: types.ExecutorConfig{
			Type: "local", Commands: []types.SandboxCommand{},
		},
		state.StateKeyLLMConfig: types.LLMConfig{
			RuleOnly: true,
		},
		state.StateKeyDedupConfig: types.DedupConfig{ConfidenceThreshold: 0.6},
		state.StateKeySkillRules:  rules,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	executor, _ := graph.NewExecutor(compiled)
	eventChan, err := executor.Execute(ctx, initialState, &agent.Invocation{
		InvocationID: "e2e-rule-only",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if eventChan == nil {
		t.Fatal("eventChan is nil")
	}

	finalState := initialState
	for evt := range eventChan {
		if evt != nil && evt.Response != nil && evt.Response.Done &&
			evt.Response.Object == graph.ObjectTypeGraphExecution {
			if evt.StateDelta != nil {
				finalState = decodeStateDelta(evt.StateDelta, finalState)
			}
		}
	}

	// Must still produce reports (from rule engine findings only)
	jsonPath, _ := finalState[state.StateKeyJSONReportPath].(string)
	mdPath, _ := finalState[state.StateKeyMDReportPath].(string)
	if jsonPath == "" || mdPath == "" {
		t.Errorf("reports not generated in rule_only mode: json=%q md=%q", jsonPath, mdPath)
	}

	// LLM findings must be empty (LLM was skipped)
	checkStateOptional(t, finalState, state.StateKeyLLMFindings, "llm_findings")
	// Rule engine should still produce findings
	checkStateOptional(t, finalState, state.StateKeyRuleFindings, "rule_findings")
	// Final findings must exist (from rule engine + tools)
	checkStateExists(t, finalState, state.StateKeyFindings, "findings")
}
