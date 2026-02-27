//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

type Report struct {
	Chat       Tokens
	ToolSearch Tokens
	Total      Tokens

	turnIndexByInvocationID map[string]int
	turnsBySessionID        map[string]map[int]TurnStats
}

func BuildReport(_ BenchmarkConfig, result *evaluation.EvaluationResult, collector *Collector) *Report {
	r := &Report{
		turnIndexByInvocationID: make(map[string]int),
		turnsBySessionID:        make(map[string]map[int]TurnStats),
	}

	if result == nil || result.EvalResult == nil {
		return r
	}

	// Aggregate tokens by walking collector snapshots for all sessions seen in results.
	for _, c := range result.EvalResult.EvalCaseResults {
		if c == nil {
			continue
		}
		sess := strings.TrimSpace(c.SessionID)
		if sess == "" {
			continue
		}
		snap := collector.Snapshot(sess)
		r.turnsBySessionID[sess] = snap
		for _, ts := range snap {
			r.Chat.Prompt += ts.Chat.Prompt
			r.Chat.Completion += ts.Chat.Completion
			r.Chat.Total += ts.Chat.Total
			r.ToolSearch.Prompt += ts.ToolSearch.Prompt
			r.ToolSearch.Completion += ts.ToolSearch.Completion
			r.ToolSearch.Total += ts.ToolSearch.Total
		}
	}

	r.Total.Prompt = r.Chat.Prompt + r.ToolSearch.Prompt
	r.Total.Completion = r.Chat.Completion + r.ToolSearch.Completion
	r.Total.Total = r.Chat.Total + r.ToolSearch.Total
	return r
}

func (r *Report) LookupTurn(sessionID string, invocationID string) TurnStats {
	turnIdx := parseTurnIndex(invocationID)
	if turnIdx <= 0 {
		return TurnStats{}
	}
	m := r.turnsBySessionID[sessionID]
	if m == nil {
		return TurnStats{}
	}
	return m[turnIdx]
}

type SummaryFile struct {
	SchemaVersion int       `json:"schemaVersion"`
	GeneratedAt   time.Time `json:"generatedAt"`

	Config SummaryConfig `json:"config"`
	Result SummaryResult `json:"result"`
	Tokens SummaryTokens `json:"tokens"`
	Cases  []SummaryCase `json:"cases"`
}

type SummaryConfig struct {
	AppName    string `json:"appName"`
	EvalSetID  string `json:"evalSetId"`
	Mode       string `json:"mode"`
	ModelName  string `json:"modelName"`
	EmbedModel string `json:"embedModel,omitempty"`
	MaxTools   int    `json:"maxTools"`
	NumRuns    int    `json:"numRuns"`
	DataDir    string `json:"dataDir"`
	OutputDir  string `json:"outputDir"`
}

type SummaryResult struct {
	OverallStatus   string `json:"overallStatus"`
	ExecutionTimeMs int64  `json:"executionTimeMs"`
	WallTimeMs      int64  `json:"wallTimeMs"`

	EvalSetResultID string `json:"evalSetResultId,omitempty"`
}

type SummaryTokens struct {
	Chat       Tokens `json:"chat"`
	ToolSearch Tokens `json:"toolsearch"`
	Total      Tokens `json:"total"`
}

type SummaryCase struct {
	EvalCaseID    string       `json:"evalCaseId"`
	OverallStatus string       `json:"overallStatus"`
	Runs          []SummaryRun `json:"runs"`
}

type SummaryRun struct {
	RunID           int           `json:"runId"`
	FinalEvalStatus string        `json:"finalEvalStatus"`
	SessionID       string        `json:"sessionId"`
	Turns           []SummaryTurn `json:"turns"`
}

type SummaryTurn struct {
	TurnID           string   `json:"turnId"`
	MetricScore      float64  `json:"metricScore"`
	ExpectedTool     string   `json:"expectedTool"`
	ActualTools      []string `json:"actualTools"`
	DurationMs       int64    `json:"durationMs"`
	ChatTokens       int      `json:"chatTokens"`
	ToolSearchTokens int      `json:"toolsearchTokens"`
}

func BuildSummaryFile(cfg BenchmarkConfig, result *evaluation.EvaluationResult, report *Report, wall time.Duration) *SummaryFile {
	s := &SummaryFile{
		SchemaVersion: 1,
		GeneratedAt:   time.Now(),
		Config: SummaryConfig{
			AppName:    cfg.AppName,
			EvalSetID:  cfg.EvalSetID,
			Mode:       string(cfg.Mode),
			ModelName:  cfg.ModelName,
			EmbedModel: cfg.EmbedModel,
			MaxTools:   cfg.MaxTools,
			NumRuns:    cfg.NumRuns,
			DataDir:    cfg.DataDir,
			OutputDir:  cfg.OutputDir,
		},
		Tokens: SummaryTokens{},
		Cases:  nil,
	}

	if report != nil {
		s.Tokens.Chat = report.Chat
		s.Tokens.ToolSearch = report.ToolSearch
		s.Tokens.Total = report.Total
	}

	if result == nil {
		return s
	}

	s.Result.OverallStatus = string(result.OverallStatus)
	s.Result.ExecutionTimeMs = result.ExecutionTime.Milliseconds()
	s.Result.WallTimeMs = wall.Milliseconds()
	if result.EvalResult != nil {
		s.Result.EvalSetResultID = result.EvalResult.EvalSetResultID
	}

	// Per-case/per-run/per-turn summary.
	cases := make([]SummaryCase, 0, len(result.EvalCases))
	for _, c := range result.EvalCases {
		if c == nil {
			continue
		}
		cs := SummaryCase{
			EvalCaseID:    c.EvalCaseID,
			OverallStatus: string(c.OverallStatus),
			Runs:          make([]SummaryRun, 0, len(c.EvalCaseResults)),
		}
		for _, r := range c.EvalCaseResults {
			if r == nil {
				continue
			}
			rs := SummaryRun{
				RunID:           r.RunID,
				FinalEvalStatus: string(r.FinalEvalStatus),
				SessionID:       r.SessionID,
				Turns:           make([]SummaryTurn, 0, len(r.EvalMetricResultPerInvocation)),
			}
			// Prefer per-invocation details.
			for _, invRes := range r.EvalMetricResultPerInvocation {
				if invRes == nil {
					continue
				}
				turnID := ""
				expected := ""
				actualTools := []string(nil)
				if invRes.ExpectedInvocation != nil {
					turnID = invRes.ExpectedInvocation.InvocationID
					expected = FirstToolName(invRes.ExpectedInvocation)
				}
				if invRes.ActualInvocation != nil {
					actualTools = ToolNames(invRes.ActualInvocation)
				}
				turnStats := TurnStats{}
				if report != nil {
					turnStats = report.LookupTurn(r.SessionID, turnID)
				}
				rs.Turns = append(rs.Turns, SummaryTurn{
					TurnID:           turnID,
					MetricScore:      firstMetricScore(invRes),
					ExpectedTool:     expected,
					ActualTools:      actualTools,
					DurationMs:       turnStats.Duration.Milliseconds(),
					ChatTokens:       turnStats.Chat.Total,
					ToolSearchTokens: turnStats.ToolSearch.Total,
				})
			}
			cs.Runs = append(cs.Runs, rs)
		}
		cases = append(cases, cs)
	}
	// Keep stable ordering as evaluation gives.
	s.Cases = cases
	return s
}

func WriteSummaryFile(cfg BenchmarkConfig, result *evaluation.EvaluationResult, report *Report, wall time.Duration) (string, error) {
	payload := BuildSummaryFile(cfg, result, report, wall)

	name := fmt.Sprintf("%s_%s_%s_summary.json", cfg.AppName, cfg.EvalSetID, cfg.Mode)
	if payload.Result.EvalSetResultID != "" {
		name = fmt.Sprintf("%s_%s.summary.json", payload.Result.EvalSetResultID, cfg.Mode)
	}
	outPath := filepath.Join(cfg.OutputDir, name)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return "", err
	}
	return outPath, nil
}

func parseTurnIndex(invocationID string) int {
	// invocationID is expected to end with a numeric suffix, e.g.
	// - "trig-01"
	// - "pow-09"
	// - "log-exp-01" (note: multiple '-' in prefix)
	// We parse the last hyphen-separated segment as the turn index.
	invocationID = strings.TrimSpace(invocationID)
	if invocationID == "" {
		return 0
	}
	idx := strings.LastIndex(invocationID, "-")
	if idx < 0 || idx == len(invocationID)-1 {
		return 0
	}
	suffix := invocationID[idx+1:]
	// tolerate leading zeros
	var n int
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

func ToolNames(inv *evalset.Invocation) []string {
	if inv == nil {
		return nil
	}
	out := make([]string, 0, len(inv.Tools))
	for _, t := range inv.Tools {
		if t == nil {
			continue
		}
		if t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
}

func FirstToolName(inv *evalset.Invocation) string {
	names := ToolNames(inv)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
