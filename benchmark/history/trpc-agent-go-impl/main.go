//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides a comprehensive evaluation tool for history tools
// effectiveness when session summarization is enabled.
//
// It extends the summary benchmark with a third mode (summary+history)
// to prove that history tools recover information lost by summarization.
//
// Evaluation Dimensions (adjusted weights for history tools):
//   - Information Retention: Key information preservation check (40%).
//   - Response Consistency: Pass^k evaluation for semantic equivalence (30%).
//   - Token Efficiency: Measures token savings from summarization (30%).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/history/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/history/trpc-agent-go-impl/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/history/trpc-agent-go-impl/evaluation/evaluator/comparator"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/history/trpc-agent-go-impl/evaluation/evaluator/passhatk"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/history/trpc-agent-go-impl/evaluation/evaluator/retention"
	evalsummary "trpc.group/trpc-go/trpc-agent-go/benchmark/history/trpc-agent-go-impl/evaluation/evaluator/summary"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Command-line flags.
var (
	flagModel   = flag.String("model", "", "Model name (env MODEL_NAME or gpt-4o-mini)")
	flagDataset = flag.String("dataset", "../data/mt-bench-101", "Dataset path (MT-Bench-101)")
	flagTask    = flag.String(
		"task",
		"",
		"Filter MT-Bench-101 entries by task code (e.g., CM, SI, PI)",
	)
	flagNumCases = flag.Int("num-cases", 0, "Number of test cases (0=all)")
	flagNumRuns  = flag.Int("num-runs", 1, "Runs per case for Pass^k consistency")
	flagOutput   = flag.String("output", "../results", "Output directory")
	flagEvents   = flag.Int("events", 2, "Event threshold for summarization")

	flagUseLLMEval = flag.Bool(
		"llm-eval",
		false,
		"Use LLM for semantic evaluation",
	)
	flagVerbose = flag.Bool("verbose", false, "Print full conversation content")
	flagResume  = flag.Bool("resume", false, "Resume from previous checkpoint")

	flagConsistencyThreshold = flag.Float64(
		"consistency-threshold",
		0.7,
		"Threshold for consistency pass/fail (0.0-1.0)",
	)
	flagRetentionThreshold = flag.Float64(
		"retention-threshold",
		0.7,
		"Threshold for retention pass/fail (0.0-1.0)",
	)
	flagKValues = flag.String(
		"k-values",
		"1,2,4",
		"Pass^k k values (comma-separated)",
	)

	// history-only mode: only run summary-history, reuse
	// benchmark/summary results for baseline & summary.
	flagMode = flag.String(
		"mode",
		"history-only",
		"Evaluation mode: all (run 3 groups) or "+
			"history-only (reuse summary benchmark results)",
	)
	flagSummaryResults = flag.String(
		"summary-results",
		"",
		"Path to benchmark/summary results dir containing "+
			"per-task subdirs with results.json (required "+
			"for history-only mode)",
	)
)

// RunMode indicates the agent configuration for a run.
type RunMode string

const (
	modeBaseline       RunMode = "baseline"
	modeSummary        RunMode = "summary"
	modeSummaryHistory RunMode = "summary-history"
)

// Evaluation weights (adjusted for history tools: retention is key).
const (
	weightRetention   = 0.40
	weightConsistency = 0.30
	weightTokens      = 0.30
)

// suppressHistoryToolInstruction tells the model not to use
// history tools in the summary-only mode, so we can measure the
// pure summary baseline without history tool assistance.
const suppressHistoryToolInstruction = "IMPORTANT: Do NOT use the " +
	"search_history or get_history_events tools. " +
	"Answer only based on the information visible in this conversation."

var mtBench101TaskCodeSet = map[string]struct{}{
	"AR": {},
	"CC": {},
	"CM": {},
	"CR": {},
	"FR": {},
	"GR": {},
	"IC": {},
	"MR": {},
	"PI": {},
	"SA": {},
	"SC": {},
	"SI": {},
	"TS": {},
}

const (
	// evalModeAll runs all three groups (baseline, summary,
	// summary-history).
	evalModeAll = "all"
	// evalModeHistoryOnly runs only summary-history, reusing
	// benchmark/summary results for baseline and summary.
	evalModeHistoryOnly = "history-only"
)

// SummaryBenchmarkCaseResult mirrors the per-case JSON structure
// produced by benchmark/summary.
type SummaryBenchmarkCaseResult struct {
	CaseID          string          `json:"caseId"`
	TokenEfficiency json.RawMessage `json:"tokenEfficiency"`
	Consistency     json.RawMessage `json:"consistency"`
	Retention       json.RawMessage `json:"retention"`
	BaselineRuns    json.RawMessage `json:"baselineRuns"`
	SummaryRuns     json.RawMessage `json:"summaryRuns"`
}

// SummaryBenchmarkResults mirrors the top-level JSON from
// benchmark/summary results.json.
type SummaryBenchmarkResults struct {
	CaseResults []SummaryBenchmarkCaseResult `json:"caseResults"`
}

// summaryRefData holds pre-parsed baseline/summary metrics for one
// case, loaded from benchmark/summary results.
type summaryRefData struct {
	TokenEfficiency *TokenEfficiency
	Consistency     *ConsistencyResult
	Retention       *RetentionResult
	BaselineRuns    []*RunResult
	SummaryRuns     []*RunResult
}

// summaryBenchTokenEfficiency mirrors the tokenEfficiency JSON
// produced by benchmark/summary, where the test group is named
// "summary" instead of "test".
type summaryBenchTokenEfficiency struct {
	BaselineTokens           int     `json:"baselineTokens"`
	SummaryTokens            int     `json:"summaryTokens"`
	TokensSaved              int     `json:"tokensSaved"`
	BaselinePromptTokens     int     `json:"baselinePromptTokens"`
	SummaryPromptTokens      int     `json:"summaryPromptTokens"`
	PromptTokensSaved        int     `json:"promptTokensSaved"`
	BaselineCompletionTokens int     `json:"baselineCompletionTokens"`
	SummaryCompletionTokens  int     `json:"summaryCompletionTokens"`
	BaselineLastPrompt       int     `json:"baselineLastPrompt"`
	SummaryLastPrompt        int     `json:"summaryLastPrompt"`
	SavingsPercentage        float64 `json:"savingsPercentage"`
	PromptSavingsPercentage  float64 `json:"promptSavingsPercentage"`
	CompressionRatio         float64 `json:"compressionRatio"`
}

// toTokenEfficiency converts the summary-named fields to the
// generic test-named TokenEfficiency used internally.
func (s *summaryBenchTokenEfficiency) toTokenEfficiency() *TokenEfficiency {
	return &TokenEfficiency{
		BaselineTokens:           s.BaselineTokens,
		TestTokens:               s.SummaryTokens,
		TokensSaved:              s.TokensSaved,
		BaselinePromptTokens:     s.BaselinePromptTokens,
		TestPromptTokens:         s.SummaryPromptTokens,
		PromptTokensSaved:        s.PromptTokensSaved,
		BaselineCompletionTokens: s.BaselineCompletionTokens,
		TestCompletionTokens:     s.SummaryCompletionTokens,
		BaselineLastPrompt:       s.BaselineLastPrompt,
		TestLastPrompt:           s.SummaryLastPrompt,
		SavingsPercentage:        s.SavingsPercentage,
		PromptSavingsPercentage:  s.PromptSavingsPercentage,
		CompressionRatio:         s.CompressionRatio,
	}
}

// loadSummaryReference loads benchmark/summary results for the given
// task codes and returns a map keyed by caseId.
func loadSummaryReference(
	resultsDir string,
	taskCodes []string,
) (map[string]*summaryRefData, error) {
	refs := make(map[string]*summaryRefData)

	// Determine which task directories to scan.
	var dirs []string
	if len(taskCodes) > 0 {
		for _, tc := range taskCodes {
			dirs = append(dirs, filepath.Join(resultsDir, tc))
		}
	} else {
		// Scan all valid task-code subdirectories.
		for _, tc := range validMTBench101TaskCodes() {
			dir := filepath.Join(resultsDir, tc)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				dirs = append(dirs, dir)
			}
		}
	}

	for _, dir := range dirs {
		jsonPath := filepath.Join(dir, "results.json")
		data, err := os.ReadFile(jsonPath)
		if err != nil {
			return nil, fmt.Errorf(
				"read summary results %s: %w", jsonPath, err,
			)
		}
		var sbr SummaryBenchmarkResults
		if err := json.Unmarshal(data, &sbr); err != nil {
			return nil, fmt.Errorf(
				"parse summary results %s: %w", jsonPath, err,
			)
		}
		for _, cr := range sbr.CaseResults {
			ref := &summaryRefData{}

			// Parse token efficiency (summary benchmark uses
			// "summaryTokens" instead of "testTokens").
			if len(cr.TokenEfficiency) > 0 {
				var ste summaryBenchTokenEfficiency
				if err := json.Unmarshal(
					cr.TokenEfficiency, &ste,
				); err != nil {
					return nil, fmt.Errorf(
						"parse tokenEfficiency for %s: %w",
						cr.CaseID, err,
					)
				}
				ref.TokenEfficiency = ste.toTokenEfficiency()
			}

			// Parse consistency.
			if len(cr.Consistency) > 0 {
				var c ConsistencyResult
				if err := json.Unmarshal(
					cr.Consistency, &c,
				); err != nil {
					return nil, fmt.Errorf(
						"parse consistency for %s: %w",
						cr.CaseID, err,
					)
				}
				ref.Consistency = &c
			}

			// Parse retention.
			if len(cr.Retention) > 0 {
				var r RetentionResult
				if err := json.Unmarshal(cr.Retention, &r); err != nil {
					return nil, fmt.Errorf(
						"parse retention for %s: %w",
						cr.CaseID, err,
					)
				}
				ref.Retention = &r
			}

			// Parse baseline runs.
			if len(cr.BaselineRuns) > 0 {
				var br []*RunResult
				if err := json.Unmarshal(
					cr.BaselineRuns, &br,
				); err != nil {
					log.Printf(
						"Warning: cannot parse baselineRuns "+
							"for %s: %v", cr.CaseID, err,
					)
				} else {
					ref.BaselineRuns = br
				}
			}

			// Parse summary runs.
			if len(cr.SummaryRuns) > 0 {
				var sr []*RunResult
				if err := json.Unmarshal(
					cr.SummaryRuns, &sr,
				); err != nil {
					log.Printf(
						"Warning: cannot parse summaryRuns "+
							"for %s: %v", cr.CaseID, err,
					)
				} else {
					ref.SummaryRuns = sr
				}
			}

			refs[cr.CaseID] = ref
		}
	}

	if len(refs) == 0 {
		return nil, fmt.Errorf(
			"no summary benchmark results found in %s", resultsDir,
		)
	}
	log.Printf(
		"Loaded %d cases from summary benchmark reference",
		len(refs),
	)
	return refs, nil
}

func validMTBench101TaskCodes() []string {
	return []string{
		"AR", "CC", "CM", "CR", "FR", "GR",
		"IC", "MR", "PI", "SA", "SC", "SI", "TS",
	}
}

func parseTaskFilter(filter string) ([]string, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}

	parts := strings.Split(filter, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.ToUpper(p)
		if p == "" {
			continue
		}
		if _, ok := mtBench101TaskCodeSet[p]; !ok {
			return nil, fmt.Errorf(
				"invalid task code: %s, valid values: %s",
				p,
				strings.Join(validMTBench101TaskCodes(), ","),
			)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf(
			"invalid task filter: %s, valid values: %s",
			filter,
			strings.Join(validMTBench101TaskCodes(), ","),
		)
	}
	return out, nil
}

func parseKValues(input string) []int {
	input = strings.TrimSpace(input)
	if input == "" {
		return []int{1}
	}
	parts := strings.Split(input, ",")
	out := make([]int, 0, len(parts))
	seen := make(map[int]bool)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v := asInt(p)
		if v <= 0 {
			log.Fatalf("Invalid -k-values: %s", input)
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		log.Fatalf("Invalid -k-values: %s", input)
	}
	return out
}

func isHistoryOnlyMode() bool {
	return *flagMode == evalModeHistoryOnly
}

func validateFlags() {
	if *flagMode != evalModeAll && *flagMode != evalModeHistoryOnly {
		log.Fatalf(
			"Invalid -mode: %s, must be %q or %q",
			*flagMode, evalModeAll, evalModeHistoryOnly,
		)
	}
	if isHistoryOnlyMode() && *flagSummaryResults == "" {
		log.Fatalf(
			"-summary-results is required for history-only mode",
		)
	}
	if *flagNumRuns < 1 {
		log.Fatalf("Invalid -num-runs: %d", *flagNumRuns)
	}
	if *flagNumCases < 0 {
		log.Fatalf("Invalid -num-cases: %d", *flagNumCases)
	}
	if *flagEvents < 0 {
		log.Fatalf("Invalid -events: %d", *flagEvents)
	}
	if *flagConsistencyThreshold < 0 || *flagConsistencyThreshold > 1 {
		log.Fatalf(
			"Invalid -consistency-threshold: %.3f",
			*flagConsistencyThreshold,
		)
	}
	if *flagRetentionThreshold < 0 || *flagRetentionThreshold > 1 {
		log.Fatalf(
			"Invalid -retention-threshold: %.3f",
			*flagRetentionThreshold,
		)
	}
	_ = parseKValues(*flagKValues)
}

// CaseResult stores evaluation results for a single test case.
// It has two comparison groups:
//   - Summary vs Baseline
//   - SummaryHistory vs Baseline
type CaseResult struct {
	CaseID string `json:"caseId"`

	// Summary-only vs Baseline.
	SummaryTokenEfficiency *TokenEfficiency   `json:"summaryTokenEfficiency"`
	SummaryConsistency     *ConsistencyResult `json:"summaryConsistency"`
	SummaryRetention       *RetentionResult   `json:"summaryRetention"`

	// Summary+History vs Baseline.
	HistoryTokenEfficiency *TokenEfficiency   `json:"historyTokenEfficiency"`
	HistoryConsistency     *ConsistencyResult `json:"historyConsistency"`
	HistoryRetention       *RetentionResult   `json:"historyRetention"`

	// Incremental metrics.
	RetentionLift float64 `json:"retentionLift"`
	TokenOverhead float64 `json:"tokenOverhead"`

	// Raw run data for logging.
	BaselineRuns []*RunResult `json:"baselineRuns,omitempty"`
	SummaryRuns  []*RunResult `json:"summaryRuns,omitempty"`
	HistoryRuns  []*RunResult `json:"historyRuns,omitempty"`
}

// TokenEfficiency measures token usage differences.
type TokenEfficiency struct {
	BaselineTokens int `json:"baselineTokens"`
	TestTokens     int `json:"testTokens"`
	TokensSaved    int `json:"tokensSaved"`

	BaselinePromptTokens int `json:"baselinePromptTokens"`
	TestPromptTokens     int `json:"testPromptTokens"`
	PromptTokensSaved    int `json:"promptTokensSaved"`

	BaselineCompletionTokens int `json:"baselineCompletionTokens"`
	TestCompletionTokens     int `json:"testCompletionTokens"`

	BaselineLastPrompt int `json:"baselineLastPrompt"`
	TestLastPrompt     int `json:"testLastPrompt"`

	SavingsPercentage       float64 `json:"savingsPercentage"`
	PromptSavingsPercentage float64 `json:"promptSavingsPercentage"`
	CompressionRatio        float64 `json:"compressionRatio"`
}

// ConsistencyResult stores Pass^k evaluation results.
type ConsistencyResult struct {
	Score            float64        `json:"score"`
	PassHat1         float64        `json:"passHat1"`
	PassHat2         float64        `json:"passHat2,omitempty"`
	PassHat4         float64        `json:"passHat4,omitempty"`
	SuccessCount     int            `json:"successCount"`
	TotalRuns        int            `json:"totalRuns"`
	Variance         float64        `json:"variance"`
	ConsistencyLevel string         `json:"consistencyLevel"`
	Details          map[string]any `json:"details,omitempty"`
}

// RetentionResult stores information retention results.
type RetentionResult struct {
	RetentionRate float64  `json:"retentionRate"`
	KeyInfoCount  int      `json:"keyInfoCount"`
	RetainedCount int      `json:"retainedCount"`
	MissingInfo   []string `json:"missingInfo,omitempty"`
	PerTurn       []float64 `json:"perTurn,omitempty"`
	PerRun        []float64 `json:"perRun,omitempty"`
}

// TokenUsage stores detailed token usage for a single LLM call.
type TokenUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// RunResult stores a single agent run result.
type RunResult struct {
	Mode        RunMode               `json:"mode"`
	Invocations []*evalset.Invocation `json:"invocations"`

	TokenUsagePerTurn []*TokenUsage `json:"tokenUsagePerTurn"`

	TotalTokens      int `json:"totalTokens"`
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`

	Duration time.Duration `json:"duration"`
}

// AggregatedResults stores overall evaluation results.
type AggregatedResults struct {
	Timestamp   string        `json:"timestamp"`
	Model       string        `json:"model"`
	NumCases    int           `json:"numCases"`
	NumRuns     int           `json:"numRuns"`
	CaseResults []*CaseResult `json:"caseResults"`

	// Summary-only aggregated metrics.
	SummaryAvgTokenSavings  float64 `json:"summaryAvgTokenSavings"`
	SummaryAvgPromptSavings float64 `json:"summaryAvgPromptSavings"`
	SummaryAvgConsistency   float64 `json:"summaryAvgConsistency"`
	SummaryAvgRetention     float64 `json:"summaryAvgRetention"`
	SummaryOverallScore     float64 `json:"summaryOverallScore"`

	// Summary+History aggregated metrics.
	HistoryAvgTokenSavings  float64 `json:"historyAvgTokenSavings"`
	HistoryAvgPromptSavings float64 `json:"historyAvgPromptSavings"`
	HistoryAvgConsistency   float64 `json:"historyAvgConsistency"`
	HistoryAvgRetention     float64 `json:"historyAvgRetention"`
	HistoryOverallScore     float64 `json:"historyOverallScore"`

	// Incremental: history vs summary.
	AvgRetentionLift float64 `json:"avgRetentionLift"`
	AvgTokenOverhead float64 `json:"avgTokenOverhead"`
}

func main() {
	flag.Parse()
	validateFlags()

	modelName := getModelName()
	outputDir := *flagOutput

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	log.Printf("=== History Tools Evaluation ===")
	log.Printf("Model: %s", modelName)
	log.Printf("Mode: %s", *flagMode)
	log.Printf("Output: %s", outputDir)
	log.Printf("Event Threshold: %d", *flagEvents)
	log.Printf("Runs per mode: %d", *flagNumRuns)
	log.Printf("LLM Evaluation: %v", *flagUseLLMEval)
	log.Printf("Resume: %v", *flagResume)
	log.Printf(
		"Weights: Retention %.0f%%, Consistency %.0f%%, Tokens %.0f%%",
		weightRetention*100, weightConsistency*100, weightTokens*100,
	)
	if isHistoryOnlyMode() {
		log.Printf(
			"Summary reference: %s", *flagSummaryResults,
		)
		log.Printf("Modes: summary-history (reusing summary baseline)")
	} else {
		log.Printf("Modes: baseline / summary / summary-history")
	}

	// Load summary benchmark reference data if history-only.
	var summaryRefs map[string]*summaryRefData
	if isHistoryOnlyMode() {
		taskCodes, err := parseTaskFilter(*flagTask)
		if err != nil {
			log.Fatalf("Invalid task filter: %v", err)
		}
		summaryRefs, err = loadSummaryReference(
			*flagSummaryResults, taskCodes,
		)
		if err != nil {
			log.Fatalf("Failed to load summary reference: %v", err)
		}
	}

	// Load test cases.
	if *flagTask != "" {
		log.Printf("Filtering MT-Bench-101 by task: %s", *flagTask)
	}
	cases, err := loadTestCases(*flagDataset, *flagNumCases, *flagTask)
	if err != nil {
		log.Fatalf("Failed to load test cases: %v", err)
	}

	// In history-only mode, filter to cases that have summary
	// reference data.
	if isHistoryOnlyMode() {
		filtered := make([]*evalset.EvalCase, 0, len(cases))
		for _, tc := range cases {
			if _, ok := summaryRefs[tc.EvalID]; ok {
				filtered = append(filtered, tc)
			}
		}
		log.Printf(
			"Loaded %d test cases (%d matched summary reference)",
			len(cases), len(filtered),
		)
		cases = filtered
	} else {
		log.Printf("Loaded %d test cases", len(cases))
	}

	// Create evaluator.
	eval := newHistoryEvaluator(modelName, summaryRefs)

	// Run evaluation.
	results := &AggregatedResults{
		Timestamp:   time.Now().Format(time.RFC3339),
		Model:       modelName,
		NumCases:    len(cases),
		NumRuns:     *flagNumRuns,
		CaseResults: make([]*CaseResult, 0, len(cases)),
	}

	// Load checkpoint if resuming.
	completedCases := make(map[string]bool)
	if *flagResume {
		if checkpoint := loadCheckpoint(outputDir); checkpoint != nil {
			results.CaseResults = checkpoint.CaseResults
			for _, cr := range checkpoint.CaseResults {
				completedCases[cr.CaseID] = true
			}
			log.Printf(
				"Resumed from checkpoint: %d cases completed",
				len(completedCases),
			)
		}
	}

	startTime := time.Now()

	for i, tc := range cases {
		if completedCases[tc.EvalID] {
			log.Printf(
				"[%d/%d] Case: %s - SKIPPED (already completed)",
				i+1, len(cases), tc.EvalID,
			)
			continue
		}

		caseStart := time.Now()
		log.Printf("")
		log.Printf(
			"[%d/%d] Case: %s (%d turns)",
			i+1, len(cases), tc.EvalID, len(tc.Conversation),
		)

		caseResult, err := eval.evaluateCase(
			context.Background(), tc,
		)
		if err != nil {
			log.Printf("  Error: %v", err)
			continue
		}

		results.CaseResults = append(results.CaseResults, caseResult)
		saveCaseLog(outputDir, modelName, caseResult)
		saveCheckpoint(outputDir, results)

		// Print case summary.
		logCaseSummary(caseResult, caseStart)

		elapsed := time.Since(startTime)
		completed := i + 1
		avgPerCase := elapsed / time.Duration(completed)
		remaining := avgPerCase * time.Duration(len(cases)-completed)
		log.Printf(
			"  Progress: %d/%d | Elapsed: %v | ETA: %v",
			completed, len(cases),
			elapsed.Round(time.Second),
			remaining.Round(time.Second),
		)
	}

	aggregateResults(results)
	printResults(results)
	saveResults(outputDir, results)
}

func logCaseSummary(cr *CaseResult, caseStart time.Time) {
	log.Printf(
		"  Duration: %v",
		time.Since(caseStart).Round(time.Millisecond),
	)
	log.Printf(
		"  [summary]  tokens: %d->%d (%.1f%%), "+
			"consistency: %.2f, retention: %.2f",
		cr.SummaryTokenEfficiency.BaselineTokens,
		cr.SummaryTokenEfficiency.TestTokens,
		cr.SummaryTokenEfficiency.SavingsPercentage,
		cr.SummaryConsistency.Score,
		cr.SummaryRetention.RetentionRate,
	)
	log.Printf(
		"  [history]  tokens: %d->%d (%.1f%%), "+
			"consistency: %.2f, retention: %.2f",
		cr.HistoryTokenEfficiency.BaselineTokens,
		cr.HistoryTokenEfficiency.TestTokens,
		cr.HistoryTokenEfficiency.SavingsPercentage,
		cr.HistoryConsistency.Score,
		cr.HistoryRetention.RetentionRate,
	)
	log.Printf(
		"  [lift]     retention: %+.3f, token overhead: %.1f%%",
		cr.RetentionLift,
		cr.TokenOverhead,
	)
}

func getModelName() string {
	if *flagModel != "" {
		return *flagModel
	}
	if env := os.Getenv("MODEL_NAME"); env != "" {
		return env
	}
	return "gpt-4o-mini"
}

// HistoryEvaluator orchestrates the three-group evaluation.
type HistoryEvaluator struct {
	modelName   string
	llm         model.Model
	passHatK    *passhatk.PassHatKEvaluator
	retention   *retention.RetentionEvaluator
	comparator  comparator.ConversationComparator
	summaryRefs map[string]*summaryRefData
}

func newHistoryEvaluator(
	modelName string,
	summaryRefs map[string]*summaryRefData,
) *HistoryEvaluator {
	llm := openai.New(modelName)

	var conv comparator.ConversationComparator
	if *flagUseLLMEval {
		conv = evalsummary.NewSummaryComparator(
			llm, *flagConsistencyThreshold,
		)
	} else {
		conv = evalsummary.NewSummaryComparator(
			nil, *flagConsistencyThreshold,
		)
	}

	kValues := parseKValues(*flagKValues)
	phk := passhatk.New(conv, passhatk.WithKValues(kValues))

	var retEval *retention.RetentionEvaluator
	if *flagUseLLMEval {
		retEval = retention.New(
			llm, retention.WithThreshold(*flagRetentionThreshold),
		)
	} else {
		retEval = retention.New(
			nil, retention.WithThreshold(*flagRetentionThreshold),
		)
	}

	return &HistoryEvaluator{
		modelName:   modelName,
		llm:         llm,
		passHatK:    phk,
		retention:   retEval,
		comparator:  conv,
		summaryRefs: summaryRefs,
	}
}

func (e *HistoryEvaluator) evaluateCase(
	ctx context.Context,
	evalCase *evalset.EvalCase,
) (*CaseResult, error) {
	historyOnly := isHistoryOnlyMode()

	var baselineRuns, summaryRuns []*RunResult
	var summaryTokenEff, historyTokenEff *TokenEfficiency
	var summaryConsistency, historyConsistency *ConsistencyResult
	var summaryRetention, historyRetention *RetentionResult

	if historyOnly {
		// Reuse baseline/summary data from benchmark/summary.
		ref, ok := e.summaryRefs[evalCase.EvalID]
		if !ok {
			return nil, fmt.Errorf(
				"no summary reference for %s", evalCase.EvalID,
			)
		}
		summaryTokenEff = ref.TokenEfficiency
		summaryConsistency = ref.Consistency
		summaryRetention = ref.Retention
		baselineRuns = ref.BaselineRuns
		summaryRuns = ref.SummaryRuns

		// Fill defaults if reference data is missing.
		if summaryTokenEff == nil {
			summaryTokenEff = &TokenEfficiency{}
		}
		if summaryConsistency == nil {
			summaryConsistency = &ConsistencyResult{}
		}
		if summaryRetention == nil {
			summaryRetention = &RetentionResult{}
		}

		log.Printf(
			"  Loaded summary reference: "+
				"tokens=%d->%d (%.1f%%), con=%.2f, ret=%.2f",
			summaryTokenEff.BaselineTokens,
			summaryTokenEff.TestTokens,
			summaryTokenEff.SavingsPercentage,
			summaryConsistency.Score,
			summaryRetention.RetentionRate,
		)
	} else {
		log.Printf("  Running baseline mode...")
		var err error
		baselineRuns, err = e.runMultiple(
			ctx, evalCase, modeBaseline,
		)
		if err != nil {
			return nil, fmt.Errorf("baseline runs failed: %w", err)
		}

		log.Printf("  Running summary mode...")
		summaryRuns, err = e.runMultiple(
			ctx, evalCase, modeSummary,
		)
		if err != nil {
			return nil, fmt.Errorf("summary runs failed: %w", err)
		}
	}

	log.Printf("  Running summary-history mode...")
	historyRuns, err := e.runMultiple(
		ctx, evalCase, modeSummaryHistory,
	)
	if err != nil {
		return nil, fmt.Errorf("summary-history runs failed: %w", err)
	}

	log.Printf("  Evaluating...")

	if !historyOnly {
		// Evaluate summary-only vs baseline.
		summaryTokenEff = evaluateTokenEfficiency(
			baselineRuns, summaryRuns,
		)
		summaryConsistency, err = e.evaluateConsistency(
			ctx, baselineRuns, summaryRuns,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"summary consistency eval failed: %w", err,
			)
		}
		summaryRetention, err = e.evaluateRetention(
			ctx, baselineRuns, summaryRuns,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"summary retention eval failed: %w", err,
			)
		}
	}

	// Evaluate summary+history vs baseline.
	// In history-only mode, we compare against the same baseline
	// token count from the summary reference.
	if historyOnly {
		// Build a synthetic baseline run to compute token efficiency.
		syntheticBaseline := []*RunResult{{
			TotalTokens:      summaryTokenEff.BaselineTokens,
			PromptTokens:     summaryTokenEff.BaselinePromptTokens,
			CompletionTokens: summaryTokenEff.BaselineCompletionTokens,
			TokenUsagePerTurn: []*TokenUsage{{
				PromptTokens: summaryTokenEff.BaselineLastPrompt,
			}},
		}}
		historyTokenEff = evaluateTokenEfficiency(
			syntheticBaseline, historyRuns,
		)

		// For consistency/retention, compare history runs against
		// the original baseline runs if available, otherwise use
		// LLM eval on history runs alone.
		if len(baselineRuns) > 0 {
			historyConsistency, err = e.evaluateConsistency(
				ctx, baselineRuns, historyRuns,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"history consistency eval failed: %w", err,
				)
			}
			historyRetention, err = e.evaluateRetention(
				ctx, baselineRuns, historyRuns,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"history retention eval failed: %w", err,
				)
			}
		} else {
			// No baseline runs available; copy summary scores as
			// conservative estimate. The retention lift is still
			// meaningful via the LLM evaluation below.
			historyConsistency = &ConsistencyResult{
				Score:            summaryConsistency.Score,
				ConsistencyLevel: summaryConsistency.ConsistencyLevel,
			}
			historyRetention = &RetentionResult{
				RetentionRate: summaryRetention.RetentionRate,
			}
		}
	} else {
		historyTokenEff = evaluateTokenEfficiency(
			baselineRuns, historyRuns,
		)
		historyConsistency, err = e.evaluateConsistency(
			ctx, baselineRuns, historyRuns,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"history consistency eval failed: %w", err,
			)
		}
		historyRetention, err = e.evaluateRetention(
			ctx, baselineRuns, historyRuns,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"history retention eval failed: %w", err,
			)
		}
	}

	// Calculate incremental metrics.
	retentionLift := historyRetention.RetentionRate -
		summaryRetention.RetentionRate
	var tokenOverhead float64
	if summaryTokenEff.TestTokens > 0 {
		tokenOverhead = float64(
			historyTokenEff.TestTokens-summaryTokenEff.TestTokens,
		) / float64(summaryTokenEff.TestTokens) * 100
	}

	return &CaseResult{
		CaseID:                 evalCase.EvalID,
		SummaryTokenEfficiency: summaryTokenEff,
		SummaryConsistency:     summaryConsistency,
		SummaryRetention:       summaryRetention,
		HistoryTokenEfficiency: historyTokenEff,
		HistoryConsistency:     historyConsistency,
		HistoryRetention:       historyRetention,
		RetentionLift:          retentionLift,
		TokenOverhead:          tokenOverhead,
		BaselineRuns:           baselineRuns,
		SummaryRuns:            summaryRuns,
		HistoryRuns:            historyRuns,
	}, nil
}

func (e *HistoryEvaluator) runMultiple(
	ctx context.Context,
	evalCase *evalset.EvalCase,
	mode RunMode,
) ([]*RunResult, error) {
	results := make([]*RunResult, 0, *flagNumRuns)
	for i := range *flagNumRuns {
		if *flagNumRuns > 1 {
			log.Printf("    [%s] run %d/%d...", mode, i+1, *flagNumRuns)
		}
		result, err := e.runOnce(ctx, evalCase, mode, i)
		if err != nil {
			return nil, fmt.Errorf("run %d failed: %w", i, err)
		}
		if *flagNumRuns > 1 {
			log.Printf(
				"    [%s] run %d/%d: %d tokens, %v",
				mode, i+1, *flagNumRuns, result.TotalTokens,
				result.Duration.Round(time.Millisecond),
			)
		}
		results = append(results, result)
	}
	return results, nil
}

func (e *HistoryEvaluator) runOnce(
	ctx context.Context,
	evalCase *evalset.EvalCase,
	mode RunMode,
	runIndex int,
) (*RunResult, error) {
	start := time.Now()

	// Create session service and agent based on mode.
	var sessService *inmemory.SessionService
	var ag *llmagent.LLMAgent

	enableSummary := mode != modeBaseline

	if enableSummary {
		sum := summary.NewSummarizer(e.llm,
			summary.WithChecksAny(
				summary.CheckEventThreshold(*flagEvents),
			),
		)
		sessService = inmemory.NewSessionService(
			inmemory.WithSummarizer(sum),
			inmemory.WithAsyncSummaryNum(1),
			inmemory.WithSummaryQueueSize(10),
			inmemory.WithSummaryJobTimeout(30*time.Second),
		)
	} else {
		sessService = inmemory.NewSessionService()
	}

	agentOpts := []llmagent.Option{
		llmagent.WithModel(e.llm),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    false,
			MaxTokens: intPtr(2000),
		}),
		llmagent.WithAddSessionSummary(enableSummary),
	}

	// In summary-only mode, instruct the model NOT to use history tools.
	// History tools are always registered as framework tools (cannot be
	// removed), so we suppress usage via instruction instead.
	if mode == modeSummary {
		agentOpts = append(agentOpts,
			llmagent.WithInstruction(suppressHistoryToolInstruction),
		)
	}

	ag = llmagent.New("eval-agent", agentOpts...)

	const appName = "eval-app"
	r := runner.NewRunner(
		appName, ag, runner.WithSessionService(sessService),
	)
	defer r.Close()

	const userID = "eval-user"
	sessionID := fmt.Sprintf("session-%s-%s-%d-%d",
		evalCase.EvalID, mode, runIndex, time.Now().UnixNano())

	invocations := make(
		[]*evalset.Invocation, 0, len(evalCase.Conversation),
	)
	tokenUsagePerTurn := make(
		[]*TokenUsage, 0, len(evalCase.Conversation),
	)
	var totalTokens, promptTokens, completionTokens int

	for i, origInv := range evalCase.Conversation {
		if origInv.UserContent == nil {
			continue
		}

		userMsg := origInv.UserContent.Content
		msg := model.NewUserMessage(userMsg)

		if *flagVerbose {
			log.Printf(
				"      Turn %d [User]: %s",
				i+1, truncateStr(userMsg, 200),
			)
		} else {
			log.Printf(
				"      Turn %d: sending message (%d chars)...",
				i+1, len(userMsg),
			)
		}

		evtCh, err := r.Run(ctx, userID, sessionID, msg)
		if err != nil {
			return nil, fmt.Errorf("turn %d failed: %w", i+1, err)
		}

		response, usage := consumeEvents(evtCh)
		tokenUsagePerTurn = append(tokenUsagePerTurn, usage)
		totalTokens += usage.TotalTokens
		promptTokens += usage.PromptTokens
		completionTokens += usage.CompletionTokens

		if *flagVerbose {
			log.Printf(
				"      Turn %d [Assistant]: %s (p=%d, c=%d, t=%d)",
				i+1, truncateStr(response, 200),
				usage.PromptTokens, usage.CompletionTokens,
				usage.TotalTokens,
			)
		} else {
			log.Printf(
				"      Turn %d: response (%d chars, p=%d c=%d t=%d)",
				i+1, len(response),
				usage.PromptTokens, usage.CompletionTokens,
				usage.TotalTokens,
			)
		}

		inv := &evalset.Invocation{
			InvocationID: fmt.Sprintf("%d", i+1),
			UserContent:  origInv.UserContent,
			FinalResponse: &model.Message{
				Role:    model.RoleAssistant,
				Content: response,
			},
		}
		invocations = append(invocations, inv)
	}

	return &RunResult{
		Mode:              mode,
		Invocations:       invocations,
		TokenUsagePerTurn: tokenUsagePerTurn,
		TotalTokens:       totalTokens,
		PromptTokens:      promptTokens,
		CompletionTokens:  completionTokens,
		Duration:          time.Since(start),
	}, nil
}

func consumeEvents(evtCh <-chan *event.Event) (string, *TokenUsage) {
	var response strings.Builder
	usage := &TokenUsage{}

	for evt := range evtCh {
		if evt.Error != nil {
			continue
		}
		if evt.Response == nil {
			continue
		}
		// Accumulate token usage across all LLM calls in the
		// invocation. Each tool-call round-trip triggers a
		// separate LLM call with its own independent usage
		// report, so we sum them to capture the true cost.
		if evt.Response.Usage != nil &&
			evt.Response.Usage.TotalTokens > 0 {
			usage.PromptTokens += evt.Response.Usage.PromptTokens
			usage.CompletionTokens +=
				evt.Response.Usage.CompletionTokens
			usage.TotalTokens += evt.Response.Usage.TotalTokens
		}
		// Skip tool call requests and tool result events;
		// only collect the final assistant text content.
		if evt.Response.IsToolCallResponse() {
			continue
		}
		if evt.Response.IsToolResultResponse() {
			continue
		}
		if evt.Response.Object == model.ObjectTypeToolResponse {
			continue
		}
		if len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			if choice.Message.Content != "" {
				response.WriteString(choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				response.WriteString(choice.Delta.Content)
			}
		}
	}
	return response.String(), usage
}

func evaluateTokenEfficiency(
	baselineRuns, testRuns []*RunResult,
) *TokenEfficiency {
	var baselineTotal, testTotal int
	var baselinePrompt, testPrompt int
	var baselineCompletion, testCompletion int
	var baselineLastPrompt, testLastPrompt int

	for _, r := range baselineRuns {
		baselineTotal += r.TotalTokens
		baselinePrompt += r.PromptTokens
		baselineCompletion += r.CompletionTokens
		if len(r.TokenUsagePerTurn) > 0 {
			last := r.TokenUsagePerTurn[len(r.TokenUsagePerTurn)-1]
			baselineLastPrompt += last.PromptTokens
		}
	}
	for _, r := range testRuns {
		testTotal += r.TotalTokens
		testPrompt += r.PromptTokens
		testCompletion += r.CompletionTokens
		if len(r.TokenUsagePerTurn) > 0 {
			last := r.TokenUsagePerTurn[len(r.TokenUsagePerTurn)-1]
			testLastPrompt += last.PromptTokens
		}
	}

	n := len(baselineRuns)
	baselineAvg := baselineTotal / n
	testAvg := testTotal / n
	baselinePromptAvg := baselinePrompt / n
	testPromptAvg := testPrompt / n
	baselineCompletionAvg := baselineCompletion / n
	testCompletionAvg := testCompletion / n
	baselineLastPromptAvg := baselineLastPrompt / n
	testLastPromptAvg := testLastPrompt / n

	tokensSaved := baselineAvg - testAvg
	promptTokensSaved := baselinePromptAvg - testPromptAvg

	var savingsPercentage, promptSavingsPercentage, compressionRatio float64
	if baselineAvg > 0 {
		savingsPercentage = float64(tokensSaved) /
			float64(baselineAvg) * 100
		if testAvg > 0 {
			compressionRatio = float64(baselineAvg) / float64(testAvg)
		}
	}
	if baselinePromptAvg > 0 {
		promptSavingsPercentage = float64(promptTokensSaved) /
			float64(baselinePromptAvg) * 100
	}

	return &TokenEfficiency{
		BaselineTokens:           baselineAvg,
		TestTokens:               testAvg,
		TokensSaved:              tokensSaved,
		BaselinePromptTokens:     baselinePromptAvg,
		TestPromptTokens:         testPromptAvg,
		PromptTokensSaved:        promptTokensSaved,
		BaselineCompletionTokens: baselineCompletionAvg,
		TestCompletionTokens:     testCompletionAvg,
		BaselineLastPrompt:       baselineLastPromptAvg,
		TestLastPrompt:           testLastPromptAvg,
		SavingsPercentage:        savingsPercentage,
		PromptSavingsPercentage:  promptSavingsPercentage,
		CompressionRatio:         compressionRatio,
	}
}

func (e *HistoryEvaluator) evaluateConsistency(
	ctx context.Context,
	baselineRuns, testRuns []*RunResult,
) (*ConsistencyResult, error) {
	baselineInvs := make([][]*evalset.Invocation, len(baselineRuns))
	for i, r := range baselineRuns {
		baselineInvs[i] = r.Invocations
	}
	testInvs := make([][]*evalset.Invocation, len(testRuns))
	for i, r := range testRuns {
		testInvs[i] = r.Invocations
	}

	result, err := e.passHatK.EvaluateMultiRun(
		ctx, baselineInvs, testInvs, nil,
	)
	if err != nil {
		return nil, err
	}

	var passHat1, passHat2, passHat4, variance float64
	var successCount, totalRuns int
	if details := result.Details; details != nil {
		passHat1 = asFloat64(details["pass_hat_1"])
		passHat2 = asFloat64(details["pass_hat_2"])
		passHat4 = asFloat64(details["pass_hat_4"])
		variance = asFloat64(details["variance"])
		successCount = asInt(details["success_count"])
		totalRuns = asInt(details["total_runs"])
	}

	level := "low"
	if result.OverallScore >= 0.9 {
		level = "high"
	} else if result.OverallScore >= 0.7 {
		level = "medium"
	}

	return &ConsistencyResult{
		Score:            result.OverallScore,
		PassHat1:         passHat1,
		PassHat2:         passHat2,
		PassHat4:         passHat4,
		SuccessCount:     successCount,
		TotalRuns:        totalRuns,
		Variance:         variance,
		ConsistencyLevel: level,
		Details:          result.Details,
	}, nil
}

func (e *HistoryEvaluator) evaluateRetention(
	ctx context.Context,
	baselineRuns, testRuns []*RunResult,
) (*RetentionResult, error) {
	baselineInvs := make([][]*evalset.Invocation, len(baselineRuns))
	for i, r := range baselineRuns {
		baselineInvs[i] = r.Invocations
	}
	testInvs := make([][]*evalset.Invocation, len(testRuns))
	for i, r := range testRuns {
		testInvs[i] = r.Invocations
	}

	// Prefer single-run API to preserve per-turn retention details.
	if len(baselineInvs) == 1 && len(testInvs) == 1 {
		result, err := e.retention.Evaluate(
			ctx, testInvs[0], baselineInvs[0], nil,
		)
		if err != nil {
			return nil, err
		}
		return parseRetentionResult(result), nil
	}

	result, err := e.retention.EvaluateMultiRun(
		ctx, baselineInvs, testInvs, nil,
	)
	if err != nil {
		return nil, err
	}
	return parseRetentionResult(result), nil
}

// Test case loading functions.

func loadTestCases(
	datasetPath string,
	numCases int,
	taskFilter string,
) ([]*evalset.EvalCase, error) {
	if strings.TrimSpace(datasetPath) == "" {
		return nil, fmt.Errorf(
			"dataset path is required, please set -dataset",
		)
	}
	filters, err := parseTaskFilter(taskFilter)
	if err != nil {
		return nil, err
	}
	return loadFromDataset(datasetPath, numCases, filters)
}

func loadFromDataset(
	datasetPath string,
	numCases int,
	taskFilters []string,
) ([]*evalset.EvalCase, error) {
	datasetPath = filepath.Clean(datasetPath)

	info, err := os.Stat(datasetPath)
	if err != nil {
		return nil, fmt.Errorf("dataset path does not exist: %w", err)
	}

	var (
		loader   *dataset.DatasetLoader
		filename string
	)
	if info.Mode().IsRegular() {
		loader = dataset.NewDatasetLoader(filepath.Dir(datasetPath))
		filename = filepath.Base(datasetPath)
	} else {
		mtBenchPath := filepath.Join(
			datasetPath, "subjective/mtbench101.jsonl",
		)
		if _, err := os.Stat(mtBenchPath); err != nil {
			return nil, fmt.Errorf(
				"MT-Bench-101 file not found: %s", mtBenchPath,
			)
		}
		loader = dataset.NewDatasetLoader(filepath.Dir(datasetPath))
		filename = filepath.Base(datasetPath) +
			"/subjective/mtbench101.jsonl"
	}

	log.Printf("Loading MT-Bench-101 dataset...")
	entries, err := loader.LoadMTBench101(filename, taskFilters...)
	if err != nil {
		return nil, fmt.Errorf("load MT-Bench-101: %w", err)
	}
	if len(entries) == 0 {
		if len(taskFilters) > 0 {
			return nil, fmt.Errorf(
				"no entries matched task filter: %s, valid: %s",
				strings.Join(taskFilters, ","),
				strings.Join(validMTBench101TaskCodes(), ","),
			)
		}
		return nil, fmt.Errorf("no MT-Bench-101 entries found")
	}

	cases := dataset.ConvertMTBench101ToEvalCases(entries)
	if numCases > 0 && numCases < len(cases) {
		cases = cases[:numCases]
	}
	log.Printf(
		"Loaded %d cases from MT-Bench-101 (total: %d)",
		len(cases), len(entries),
	)
	return cases, nil
}

// Output functions.

func aggregateResults(results *AggregatedResults) {
	if len(results.CaseResults) == 0 {
		return
	}

	var (
		sTotalSavings, sPromptSavings float64
		sConsistency, sRetention      float64
		hTotalSavings, hPromptSavings float64
		hConsistency, hRetention      float64
		retLift, tokenOverhead        float64
	)
	for _, cr := range results.CaseResults {
		sTotalSavings += cr.SummaryTokenEfficiency.SavingsPercentage
		sPromptSavings += cr.SummaryTokenEfficiency.PromptSavingsPercentage
		sConsistency += cr.SummaryConsistency.Score
		sRetention += cr.SummaryRetention.RetentionRate

		hTotalSavings += cr.HistoryTokenEfficiency.SavingsPercentage
		hPromptSavings += cr.HistoryTokenEfficiency.PromptSavingsPercentage
		hConsistency += cr.HistoryConsistency.Score
		hRetention += cr.HistoryRetention.RetentionRate

		retLift += cr.RetentionLift
		tokenOverhead += cr.TokenOverhead
	}

	n := float64(len(results.CaseResults))
	results.SummaryAvgTokenSavings = sTotalSavings / n
	results.SummaryAvgPromptSavings = sPromptSavings / n
	results.SummaryAvgConsistency = sConsistency / n
	results.SummaryAvgRetention = sRetention / n

	results.HistoryAvgTokenSavings = hTotalSavings / n
	results.HistoryAvgPromptSavings = hPromptSavings / n
	results.HistoryAvgConsistency = hConsistency / n
	results.HistoryAvgRetention = hRetention / n

	results.AvgRetentionLift = retLift / n
	results.AvgTokenOverhead = tokenOverhead / n

	// Compute weighted overall scores.
	sTokenScore := clampScore(results.SummaryAvgTokenSavings / 100)
	results.SummaryOverallScore = weightRetention*results.SummaryAvgRetention +
		weightConsistency*results.SummaryAvgConsistency +
		weightTokens*sTokenScore

	hTokenScore := clampScore(results.HistoryAvgTokenSavings / 100)
	results.HistoryOverallScore = weightRetention*results.HistoryAvgRetention +
		weightConsistency*results.HistoryAvgConsistency +
		weightTokens*hTokenScore
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func printResults(results *AggregatedResults) {
	sep := strings.Repeat("=", 70)
	fmt.Println("\n" + sep)
	fmt.Println("History Tools Evaluation Results")
	fmt.Println(sep)

	fmt.Printf("\nModel: %s\n", results.Model)
	fmt.Printf(
		"Cases: %d, Runs per mode: %d\n",
		results.NumCases, results.NumRuns,
	)

	fmt.Println("\n--- Summary-Only vs Baseline ---")
	fmt.Printf("  Token Savings:  %.1f%% total, %.1f%% prompt\n",
		results.SummaryAvgTokenSavings, results.SummaryAvgPromptSavings)
	fmt.Printf("  Consistency:    %.3f\n", results.SummaryAvgConsistency)
	fmt.Printf("  Retention:      %.3f\n", results.SummaryAvgRetention)
	fmt.Printf("  Overall Score:  %.3f\n", results.SummaryOverallScore)

	fmt.Println("\n--- Summary+History vs Baseline ---")
	fmt.Printf("  Token Savings:  %.1f%% total, %.1f%% prompt\n",
		results.HistoryAvgTokenSavings, results.HistoryAvgPromptSavings)
	fmt.Printf("  Consistency:    %.3f\n", results.HistoryAvgConsistency)
	fmt.Printf("  Retention:      %.3f\n", results.HistoryAvgRetention)
	fmt.Printf("  Overall Score:  %.3f\n", results.HistoryOverallScore)

	fmt.Println("\n--- Incremental: History vs Summary ---")
	fmt.Printf("  Retention Lift: %+.3f\n", results.AvgRetentionLift)
	fmt.Printf("  Token Overhead: %.1f%%\n", results.AvgTokenOverhead)

	fmt.Printf(
		"\nWeights: Retention %.0f%%, Consistency %.0f%%, Tokens %.0f%%\n",
		weightRetention*100, weightConsistency*100, weightTokens*100,
	)

	fmt.Println("\n--- Per-Case Summary ---")
	for _, cr := range results.CaseResults {
		fmt.Printf(
			"  %s: sum[tok=%.0f%%, con=%.2f, ret=%.2f] "+
				"hist[tok=%.0f%%, con=%.2f, ret=%.2f] "+
				"lift=%+.3f\n",
			cr.CaseID,
			cr.SummaryTokenEfficiency.SavingsPercentage,
			cr.SummaryConsistency.Score,
			cr.SummaryRetention.RetentionRate,
			cr.HistoryTokenEfficiency.SavingsPercentage,
			cr.HistoryConsistency.Score,
			cr.HistoryRetention.RetentionRate,
			cr.RetentionLift,
		)
	}
	fmt.Println(sep)
}

func saveCaseLog(outputDir, modelName string, cr *CaseResult) {
	logPath := filepath.Join(outputDir, cr.CaseID+".log")
	f, err := os.Create(logPath)
	if err != nil {
		log.Printf("Failed to create log: %v", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "=== Evaluation Log: %s ===\n", cr.CaseID)
	fmt.Fprintf(f, "Timestamp: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "Model: %s\n\n", modelName)

	// Summary-only metrics.
	writeTokenSection(f, "SUMMARY-ONLY", cr.SummaryTokenEfficiency)
	writeConsistencySection(f, "SUMMARY-ONLY", cr.SummaryConsistency)
	writeRetentionSection(f, "SUMMARY-ONLY", cr.SummaryRetention)

	// Summary+History metrics.
	writeTokenSection(f, "SUMMARY+HISTORY", cr.HistoryTokenEfficiency)
	writeConsistencySection(f, "SUMMARY+HISTORY", cr.HistoryConsistency)
	writeRetentionSection(f, "SUMMARY+HISTORY", cr.HistoryRetention)

	// Incremental.
	fmt.Fprintf(f, "--- INCREMENTAL (history vs summary) ---\n")
	fmt.Fprintf(f, "Retention Lift: %+.3f\n", cr.RetentionLift)
	fmt.Fprintf(f, "Token Overhead: %.1f%%\n\n", cr.TokenOverhead)

	// Conversation logs.
	writeRunLog(f, "BASELINE", cr.BaselineRuns)
	writeRunLog(f, "SUMMARY", cr.SummaryRuns)
	writeRunLog(f, "SUMMARY+HISTORY", cr.HistoryRuns)
}

func writeTokenSection(f *os.File, label string, te *TokenEfficiency) {
	fmt.Fprintf(f, "--- %s TOKEN EFFICIENCY ---\n", label)
	fmt.Fprintf(f, "Total:  %d -> %d (%.1f%% saved)\n",
		te.BaselineTokens, te.TestTokens, te.SavingsPercentage)
	fmt.Fprintf(f, "Prompt: %d -> %d (%.1f%% saved)\n",
		te.BaselinePromptTokens, te.TestPromptTokens,
		te.PromptSavingsPercentage)
	fmt.Fprintf(f, "Compl:  %d -> %d\n",
		te.BaselineCompletionTokens, te.TestCompletionTokens)
	fmt.Fprintf(f, "Last:   %d -> %d\n",
		te.BaselineLastPrompt, te.TestLastPrompt)
	fmt.Fprintf(f, "Ratio:  %.2fx\n\n", te.CompressionRatio)
}

func writeConsistencySection(
	f *os.File, label string, c *ConsistencyResult,
) {
	fmt.Fprintf(f, "--- %s CONSISTENCY ---\n", label)
	fmt.Fprintf(f, "Score: %.3f (%s)\n", c.Score, c.ConsistencyLevel)
	fmt.Fprintf(f, "Pass^1: %.3f\n", c.PassHat1)
	if c.PassHat2 > 0 {
		fmt.Fprintf(f, "Pass^2: %.3f\n", c.PassHat2)
	}
	if c.PassHat4 > 0 {
		fmt.Fprintf(f, "Pass^4: %.3f\n", c.PassHat4)
	}
	fmt.Fprintf(f, "Success: %d/%d\n", c.SuccessCount, c.TotalRuns)
	fmt.Fprintf(f, "Variance: %.4f\n\n", c.Variance)
}

func writeRetentionSection(
	f *os.File, label string, r *RetentionResult,
) {
	fmt.Fprintf(f, "--- %s RETENTION ---\n", label)
	fmt.Fprintf(f, "Rate: %.3f\n", r.RetentionRate)
	fmt.Fprintf(f, "Key Info: %d found, %d retained\n",
		r.KeyInfoCount, r.RetainedCount)
	if len(r.MissingInfo) > 0 {
		fmt.Fprintf(f, "Missing:\n")
		for _, info := range r.MissingInfo {
			fmt.Fprintf(f, "  - %s\n", info)
		}
	}
	fmt.Fprintf(f, "\n")
}

func writeRunLog(f *os.File, label string, runs []*RunResult) {
	if len(runs) == 0 {
		return
	}
	run := runs[0]
	fmt.Fprintf(
		f,
		"--- %s MODE (total=%d, prompt=%d, completion=%d, %v) ---\n",
		label,
		run.TotalTokens, run.PromptTokens, run.CompletionTokens,
		run.Duration.Round(time.Millisecond),
	)
	for i, inv := range run.Invocations {
		fmt.Fprintf(f, "[Turn %s]", inv.InvocationID)
		if i < len(run.TokenUsagePerTurn) {
			u := run.TokenUsagePerTurn[i]
			fmt.Fprintf(f, " (p=%d, c=%d, t=%d)",
				u.PromptTokens, u.CompletionTokens, u.TotalTokens)
		}
		fmt.Fprintf(f, "\n")
		if inv.UserContent != nil {
			fmt.Fprintf(f, "User: %s\n", inv.UserContent.Content)
		}
		if inv.FinalResponse != nil {
			fmt.Fprintf(f, "Assistant: %s\n", inv.FinalResponse.Content)
		}
		fmt.Fprintf(f, "\n")
	}
}

func saveResults(outputDir string, results *AggregatedResults) {
	jsonPath := filepath.Join(outputDir, "results.json")
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal results: %v", err)
		return
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		log.Printf("Failed to write results: %v", err)
		return
	}
	log.Printf("Results saved to: %s", jsonPath)
}

func saveCheckpoint(outputDir string, results *AggregatedResults) {
	checkpointPath := filepath.Join(outputDir, "checkpoint.json")
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal checkpoint: %v", err)
		return
	}
	if err := os.WriteFile(checkpointPath, data, 0644); err != nil {
		log.Printf("Failed to write checkpoint: %v", err)
	}
}

func loadCheckpoint(outputDir string) *AggregatedResults {
	checkpointPath := filepath.Join(outputDir, "checkpoint.json")
	data, err := os.ReadFile(checkpointPath)
	if err != nil {
		return nil
	}
	var results AggregatedResults
	if err := json.Unmarshal(data, &results); err != nil {
		log.Printf("Failed to parse checkpoint: %v", err)
		return nil
	}
	return &results
}

func parseRetentionResult(
	result *evaluator.EvaluateResult,
) *RetentionResult {
	if result == nil {
		return &RetentionResult{}
	}

	r := &RetentionResult{RetentionRate: result.OverallScore}
	if result.Details == nil {
		return r
	}

	details := result.Details
	r.KeyInfoCount = asInt(
		firstNonNil(details["key_info_count"], details["total_key_info"]),
	)
	r.RetainedCount = asInt(
		firstNonNil(details["retained_count"], details["total_retained"]),
	)
	r.MissingInfo = asStringSlice(
		firstNonNil(details["missing_info"], details["unique_missing"]),
	)
	r.PerTurn = asFloat64Slice(details["per_turn_retention"])
	r.PerRun = asFloat64Slice(details["per_run_retention"])
	return r
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		return int(x)
	case float32:
		return int(x)
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return 0
		}
		i, err := strconv.Atoi(x)
		if err != nil {
			return 0
		}
		return i
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return 0
		}
		return int(i)
	default:
		return 0
	}
}

func asFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0
		}
		return f
	default:
		return 0
	}
}

func asStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func asFloat64Slice(v any) []float64 {
	switch x := v.(type) {
	case []float64:
		return x
	case []any:
		out := make([]float64, 0, len(x))
		for _, item := range x {
			out = append(out, asFloat64(item))
		}
		return out
	default:
		return nil
	}
}

func intPtr(i int) *int { return &i }

func truncateStr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
