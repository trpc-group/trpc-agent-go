//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmanalyzer implements the LLMAnalyzer GraphAgent node.
// Uses model.GenerateContent() for semantic code review analysis.
package llmanalyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Run is the LLMAnalyzer GraphAgent node.
// Reads file_changes, sandbox_results, and rule_findings from state,
// writes llm_findings and llm_errors (for monitoring LLM outages).
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() { gs[state.StateKeyNodeLLMAnalyzerMs] = time.Since(start).Milliseconds() }()

	cfg, _ := gs[state.StateKeyLLMConfig].(types.LLMConfig)
	changes, _ := gs[state.StateKeyFileChanges].([]types.FileChange)
	results, _ := gs[state.StateKeySandboxResults].([]types.SandboxResult)
	ruleFindings, _ := gs[state.StateKeyRuleFindings].([]types.Finding)
	taskID, _ := gs[state.StateKeyTaskID].(string)

	// Initialize LLM error tracking so downstream nodes can distinguish
	// "no findings" from "LLM failed".
	gs[state.StateKeyLLMErrors] = []types.LLMError{}

	if len(changes) == 0 {
		gs[state.StateKeyLLMFindings] = []types.Finding{}
		return gs, nil
	}

	// ── Dry-run mode: load mock findings matching current diff files ──
	if cfg.MockMode {
		mockPath := cfg.MockFindingsPath
		if mockPath == "" {
			mockPath = "testdata/mock_llm_findings.json"
		}
		// Collect changed file paths for mock matching
		changedFiles := make(map[string]bool)
		for _, fc := range changes {
			changedFiles[fc.FilePath] = true
		}
		findings, err := loadMockFindings(mockPath, changedFiles)
		if err != nil {
			recordLLMError(gs, "mock_load", err.Error())
			gs[state.StateKeyLLMFindings] = []types.Finding{}
			return gs, nil
		}
		for i := range findings {
			findings[i].TaskID = taskID
			if findings[i].ID == "" {
				findings[i].ID = uuid.New().String()
			}
		}
		gs[state.StateKeyLLMFindings] = findings
		return gs, nil
	}
	_ = len(changes) // live mode: use real LLM

	// ── Live mode: call model.GenerateContent() ──
	llm := getLLMModel(ctx)
	if llm == nil {
		recordLLMError(gs, "no_model", "LLM model is nil — check model provider configuration")
		gs[state.StateKeyLLMFindings] = []types.Finding{}
		return gs, nil
	}
	findings, err := analyzeWithLLM(ctx, llm, cfg, changes, results, ruleFindings, taskID)
	if err != nil {
		// LLM failure is non-fatal per error contract, but MUST be surfaced
		// through llm_errors for monitoring (Issue #2004 — CodeRabbit review).
		recordLLMError(gs, "llm_failure", err.Error())
		gs[state.StateKeyLLMFindings] = []types.Finding{}
		return gs, nil
	}

	gs[state.StateKeyLLMFindings] = findings
	return gs, nil
}

// recordLLMError appends an LLM error entry to the state so StorageWriter
// can persist it as an exception row for monitoring.
func recordLLMError(gs graph.State, errorType, detail string) {
	errors, _ := gs[state.StateKeyLLMErrors].([]types.LLMError)
	gs[state.StateKeyLLMErrors] = append(errors, types.LLMError{
		ErrorType: errorType,
		Detail:    detail,
	})
}

// analyzeWithLLM calls the model API to analyze code changes.
// Uses StructuredOutput to constrain the response to a Finding array.
func analyzeWithLLM(ctx context.Context, llm model.Model, cfg types.LLMConfig,
	changes []types.FileChange, results []types.SandboxResult,
	ruleFindings []types.Finding, taskID string) ([]types.Finding, error) {

	// Default MaxTokens before building prompts (Issue #2004: avoid
	// truncation logic using uninitialized zero value).
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}

	// Build the system prompt
	systemPrompt := buildSystemPrompt(cfg, results, ruleFindings)

	// Build the user prompt from diff hunks
	userPrompt := buildUserPrompt(changes, cfg.MaxTokens)

	req := &model.Request{
		Messages: []model.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &cfg.Temperature,
			MaxTokens:   &cfg.MaxTokens,
		},
		StructuredOutput: &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
		},
	}

	respCh, err := llm.GenerateContent(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm generate: %w", err)
	}

	var resp *model.Response
	select {
	case r := <-respCh:
		resp = r
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if resp == nil || len(resp.Choices) == 0 {
		return nil, fmt.Errorf("llm returned empty response")
	}

	content := resp.Choices[0].Message.Content
	var findings []types.Finding
	if err := json.Unmarshal([]byte(content), &findings); err != nil {
		return nil, fmt.Errorf("parse llm response: %w", err)
	}

	// Normalize: assign task_id, source, decision_kind
	for i := range findings {
		if findings[i].ID == "" {
			findings[i].ID = uuid.New().String()
		}
		findings[i].TaskID = taskID
		findings[i].Source = "llm"
		findings[i].DecisionKind = "heuristic"
		if findings[i].Confidence == 0 {
			findings[i].Confidence = 0.5
		}
	}

	return findings, nil
}

func buildSystemPrompt(cfg types.LLMConfig, results []types.SandboxResult, ruleFindings []types.Finding) string {
	if cfg.SystemPrompt != "" {
		return cfg.SystemPrompt
	}

	var sb strings.Builder
	sb.WriteString("You are a code review expert analyzing Go code changes.\n\n")
	sb.WriteString("Output a JSON array of findings. Each finding must have these fields:\n")
	sb.WriteString("severity (critical|high|medium|low|warning), category, file, line (int),\n")
	sb.WriteString("title, evidence, recommendation, confidence (0.0-1.0 float).\n\n")
	sb.WriteString("Focus on issues the tools and rule engine may have missed:\n")
	sb.WriteString("- Goroutine leaks, context not passed, missing cancellation\n")
	sb.WriteString("- Business logic errors, incorrect error handling patterns\n")
	sb.WriteString("- Design issues not detectable by regex or static analysis\n")

	if len(ruleFindings) > 0 {
		sb.WriteString("\nRule engine already found these. Do NOT duplicate:\n")
		for _, f := range ruleFindings {
			sb.WriteString(fmt.Sprintf("- %s:%d [%s] %s\n", f.File, f.Line, f.RuleID, f.Title))
		}
	}

	if len(results) > 0 {
		sb.WriteString("\nSandbox tool outputs for context:\n")
		for _, r := range results {
			sb.WriteString(fmt.Sprintf("--- %s (exit=%d) ---\n%s\n", r.Command, r.ExitCode, truncateStr(r.Stdout, 500)))
		}
	}

	return sb.String()
}

func buildUserPrompt(changes []types.FileChange, maxTokens int) string {
	var sb strings.Builder
	for _, fc := range changes {
		sb.WriteString(fmt.Sprintf("### %s\n", fc.FilePath))
		for _, hunk := range fc.Hunks {
			for _, l := range hunk.Lines {
				if l.Type == "+" {
					sb.WriteString(l.Content + "\n")
				}
			}
		}
	}
	result := sb.String()
	// Rough truncation: ~4 chars per token
	if maxTokens > 0 && len(result) > maxTokens*4 {
		result = result[:maxTokens*4] + "\n... (truncated)"
	}
	return result
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ── Mock findings for dry-run mode ──

// mockFinding extends Finding with an optional match_file for diff-based filtering.
type mockFinding struct {
	types.Finding
	MatchFile string `json:"match_file"`
}

func loadMockFindings(path string, changedFiles map[string]bool) ([]types.Finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mock findings: %w", err)
	}
	var all []mockFinding
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parse mock findings: %w", err)
	}

	// Only return findings whose match_file appears in the current diff
	var filtered []types.Finding
	for _, mf := range all {
		if mf.MatchFile == "" || changedFiles[mf.MatchFile] {
			filtered = append(filtered, mf.Finding)
		}
	}
	return filtered, nil
}

// getLLMModel retrieves the LLM model from context.
func getLLMModel(ctx context.Context) model.Model {
	type llmModelCtxKey struct{}
	m, _ := ctx.Value(llmModelCtxKey{}).(model.Model)
	return m
}
