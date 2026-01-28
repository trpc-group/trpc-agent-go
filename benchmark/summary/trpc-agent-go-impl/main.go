//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides a comprehensive evaluation tool for session
// summarization effectiveness, inspired by τ-bench and τ²-bench methodologies.
//
// Evaluation Dimensions:
//   - Token Efficiency: Measures token savings from summarization (30%).
//   - Response Consistency: Pass^k evaluation for semantic equivalence (50%).
//   - Information Retention: Key information preservation check (20%).
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

	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator/comparator"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator/passhatk"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator/retention"
	evalsummary "trpc.group/trpc-go/trpc-agent-go/benchmark/summary/trpc-agent-go-impl/evaluation/evaluator/summary"

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
		"Filter MT-Bench-101 entries by task code (e.g., CM, GR)",
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
)

// RunMode indicates whether summarization is enabled.
type RunMode string

const (
	modeBaseline RunMode = "baseline"
	modeSummary  RunMode = "summary"
)

// Evaluation weights (τ-bench inspired).
const (
	weightConsistency = 0.50
	weightTokens      = 0.30
	weightRetention   = 0.20
)

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

func validMTBench101TaskCodes() []string {
	return []string{
		"AR",
		"CC",
		"CM",
		"CR",
		"FR",
		"GR",
		"IC",
		"MR",
		"PI",
		"SA",
		"SC",
		"SI",
		"TS",
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

func validateFlags() {
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
		log.Fatalf("Invalid -retention-threshold: %.3f", *flagRetentionThreshold)
	}
	_ = parseKValues(*flagKValues)
}

// CaseResult stores evaluation results for a single test case.
type CaseResult struct {
	CaseID string `json:"caseId"`

	// Token efficiency metrics.
	TokenEfficiency *TokenEfficiency `json:"tokenEfficiency"`

	// Consistency metrics (Pass^k style).
	Consistency *ConsistencyResult `json:"consistency"`

	// Information retention metrics.
	Retention *RetentionResult `json:"retention"`

	// Raw run data for logging.
	BaselineRuns []*RunResult `json:"baselineRuns,omitempty"`
	SummaryRuns  []*RunResult `json:"summaryRuns,omitempty"`
}

// TokenEfficiency measures token usage differences.
type TokenEfficiency struct {
	// Total tokens (prompt + completion) across all turns.
	BaselineTokens int `json:"baselineTokens"`
	SummaryTokens  int `json:"summaryTokens"`
	TokensSaved    int `json:"tokensSaved"`

	// Prompt tokens only (input context size).
	BaselinePromptTokens int `json:"baselinePromptTokens"`
	SummaryPromptTokens  int `json:"summaryPromptTokens"`
	PromptTokensSaved    int `json:"promptTokensSaved"`

	// Completion tokens only (output size).
	BaselineCompletionTokens int `json:"baselineCompletionTokens"`
	SummaryCompletionTokens  int `json:"summaryCompletionTokens"`

	// Last turn prompt tokens (most relevant for summary effectiveness).
	BaselineLastPrompt int `json:"baselineLastPrompt"`
	SummaryLastPrompt  int `json:"summaryLastPrompt"`

	// Savings metrics.
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

	// PerTurn is the per-turn retention rate in a single run.
	PerTurn []float64 `json:"perTurn,omitempty"`

	// PerRun is the per-run retention rate across multiple runs.
	PerRun []float64 `json:"perRun,omitempty"`
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

	// Detailed token usage per turn.
	TokenUsagePerTurn []*TokenUsage `json:"tokenUsagePerTurn"`

	// Aggregated token counts.
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

	// Aggregated metrics.
	AvgTokenSavings  float64 `json:"avgTokenSavings"`
	AvgPromptSavings float64 `json:"avgPromptSavings"`
	AvgConsistency   float64 `json:"avgConsistency"`
	AvgRetention     float64 `json:"avgRetention"`
	OverallScore     float64 `json:"overallScore"`
}

func main() {
	flag.Parse()
	validateFlags()

	modelName := getModelName()
	outputDir := *flagOutput

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	log.Printf("=== Summary Evaluation (τ-bench inspired) ===")
	log.Printf("Model: %s", modelName)
	log.Printf("Output: %s", outputDir)
	log.Printf("Event Threshold: %d", *flagEvents)
	log.Printf("Runs per mode: %d", *flagNumRuns)
	log.Printf("LLM Evaluation: %v", *flagUseLLMEval)
	log.Printf("Resume: %v", *flagResume)
	log.Printf("Consistency Threshold: %.2f", *flagConsistencyThreshold)
	log.Printf("Retention Threshold: %.2f", *flagRetentionThreshold)
	log.Printf("K Values: %s", *flagKValues)
	log.Printf("Weights: Consistency %.0f%%, Tokens %.0f%%, Retention %.0f%%",
		weightConsistency*100, weightTokens*100, weightRetention*100)

	// Load test cases.
	if *flagTask != "" {
		log.Printf("Filtering MT-Bench-101 by task: %s", *flagTask)
	}
	cases, err := loadTestCases(*flagDataset, *flagNumCases, *flagTask)
	if err != nil {
		log.Fatalf("Failed to load test cases: %v", err)
	}
	log.Printf("Loaded %d test cases", len(cases))

	// Create evaluator.
	evaluator := newEvaluator(modelName)

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
			log.Printf("Resumed from checkpoint: %d cases completed",
				len(completedCases))
		}
	}

	startTime := time.Now()

	for i, tc := range cases {
		// Skip completed cases when resuming.
		if completedCases[tc.EvalID] {
			log.Printf("[%d/%d] Case: %s - SKIPPED (already completed)",
				i+1, len(cases), tc.EvalID)
			continue
		}

		caseStart := time.Now()
		log.Printf("")
		log.Printf("[%d/%d] Case: %s (%d turns)", i+1, len(cases), tc.EvalID,
			len(tc.Conversation))

		caseResult, err := evaluator.evaluateCase(context.Background(), tc)
		if err != nil {
			log.Printf("  Error: %v", err)
			continue
		}

		results.CaseResults = append(results.CaseResults, caseResult)
		saveCaseLog(outputDir, modelName, caseResult)

		// Save checkpoint after each case.
		saveCheckpoint(outputDir, results)

		// Print case summary.
		log.Printf("  Duration: %v", time.Since(caseStart).Round(time.Millisecond))
		log.Printf("  Tokens (total): %d -> %d (%.1f%% saved)",
			caseResult.TokenEfficiency.BaselineTokens,
			caseResult.TokenEfficiency.SummaryTokens,
			caseResult.TokenEfficiency.SavingsPercentage)
		log.Printf("  Tokens (prompt): %d -> %d (%.1f%% saved)",
			caseResult.TokenEfficiency.BaselinePromptTokens,
			caseResult.TokenEfficiency.SummaryPromptTokens,
			caseResult.TokenEfficiency.PromptSavingsPercentage)
		log.Printf("  Last turn prompt: %d -> %d",
			caseResult.TokenEfficiency.BaselineLastPrompt,
			caseResult.TokenEfficiency.SummaryLastPrompt)
		log.Printf("  Consistency: %.2f (Pass^1=%.2f, %d/%d passed)",
			caseResult.Consistency.Score,
			caseResult.Consistency.PassHat1,
			caseResult.Consistency.SuccessCount,
			caseResult.Consistency.TotalRuns)
		log.Printf("  Retention: %.2f (%d/%d key info)",
			caseResult.Retention.RetentionRate,
			caseResult.Retention.RetainedCount,
			caseResult.Retention.KeyInfoCount)

		// Print progress.
		elapsed := time.Since(startTime)
		avgPerCase := elapsed / time.Duration(i+1)
		remaining := avgPerCase * time.Duration(len(cases)-i-1)
		log.Printf("  Progress: %d/%d | Elapsed: %v | ETA: %v",
			i+1, len(cases),
			elapsed.Round(time.Second),
			remaining.Round(time.Second))
	}

	// Aggregate and output results.
	aggregateResults(results)
	printResults(results)
	saveResults(outputDir, results)
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

// Evaluator orchestrates the evaluation process.
type Evaluator struct {
	modelName  string
	llm        model.Model
	passHatK   *passhatk.PassHatKEvaluator
	retention  *retention.RetentionEvaluator
	comparator comparator.ConversationComparator
}

func newEvaluator(modelName string) *Evaluator {
	llm := openai.New(modelName)

	// Create comparator - use LLM judge if enabled.
	var conv comparator.ConversationComparator
	if *flagUseLLMEval {
		conv = evalsummary.NewSummaryComparator(llm, *flagConsistencyThreshold)
	} else {
		conv = evalsummary.NewSummaryComparator(nil, *flagConsistencyThreshold)
	}

	kValues := parseKValues(*flagKValues)

	// Create Pass^k evaluator.
	passHatK := passhatk.New(conv, passhatk.WithKValues(kValues))

	// Create retention evaluator.
	var retEval *retention.RetentionEvaluator
	if *flagUseLLMEval {
		retEval = retention.New(llm, retention.WithThreshold(*flagRetentionThreshold))
	} else {
		retEval = retention.New(nil, retention.WithThreshold(*flagRetentionThreshold))
	}

	return &Evaluator{
		modelName:  modelName,
		llm:        llm,
		passHatK:   passHatK,
		retention:  retEval,
		comparator: conv,
	}
}

func (e *Evaluator) evaluateCase(
	ctx context.Context,
	evalCase *evalset.EvalCase,
) (*CaseResult, error) {
	log.Printf("  Running baseline mode...")
	// Run baseline mode (no summarization).
	baselineRuns, err := e.runMultiple(ctx, evalCase, modeBaseline)
	if err != nil {
		return nil, fmt.Errorf("baseline runs failed: %w", err)
	}

	log.Printf("  Running summary mode...")
	// Run summary mode (with summarization).
	summaryRuns, err := e.runMultiple(ctx, evalCase, modeSummary)
	if err != nil {
		return nil, fmt.Errorf("summary runs failed: %w", err)
	}

	log.Printf("  Evaluating...")
	// Evaluate token efficiency.
	tokenEff := e.evaluateTokenEfficiency(baselineRuns, summaryRuns)

	// Evaluate consistency using Pass^k.
	consistency, err := e.evaluateConsistency(ctx, baselineRuns, summaryRuns)
	if err != nil {
		return nil, fmt.Errorf("consistency evaluation failed: %w", err)
	}

	// Evaluate information retention.
	retentionResult, err := e.evaluateRetention(ctx, baselineRuns, summaryRuns)
	if err != nil {
		return nil, fmt.Errorf("retention evaluation failed: %w", err)
	}

	return &CaseResult{
		CaseID:          evalCase.EvalID,
		TokenEfficiency: tokenEff,
		Consistency:     consistency,
		Retention:       retentionResult,
		BaselineRuns:    baselineRuns,
		SummaryRuns:     summaryRuns,
	}, nil
}

func (e *Evaluator) runMultiple(
	ctx context.Context,
	evalCase *evalset.EvalCase,
	mode RunMode,
) ([]*RunResult, error) {
	results := make([]*RunResult, 0, *flagNumRuns)
	for i := 0; i < *flagNumRuns; i++ {
		if *flagNumRuns > 1 {
			log.Printf("    [%s] run %d/%d...", mode, i+1, *flagNumRuns)
		}
		result, err := e.runOnce(ctx, evalCase, mode, i)
		if err != nil {
			return nil, fmt.Errorf("run %d failed: %w", i, err)
		}
		if *flagNumRuns > 1 {
			log.Printf("    [%s] run %d/%d: %d tokens, %v",
				mode, i+1, *flagNumRuns, result.TotalTokens,
				result.Duration.Round(time.Millisecond))
		}
		results = append(results, result)
	}
	return results, nil
}

func (e *Evaluator) runOnce(
	ctx context.Context,
	evalCase *evalset.EvalCase,
	mode RunMode,
	runIndex int,
) (*RunResult, error) {
	start := time.Now()

	// Create session service based on mode.
	var sessService *inmemory.SessionService
	withSummary := mode == modeSummary

	if withSummary {
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

	// Create agent and runner.
	ag := llmagent.New(
		"eval-agent",
		llmagent.WithModel(e.llm),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    false,
			MaxTokens: intPtr(2000),
		}),
		llmagent.WithAddSessionSummary(withSummary),
	)

	appName := "eval-app"
	r := runner.NewRunner(appName, ag, runner.WithSessionService(sessService))
	defer r.Close()

	userID := "eval-user"
	sessionID := fmt.Sprintf("session-%s-%s-%d-%d",
		evalCase.EvalID, mode, runIndex, time.Now().UnixNano())

	// Execute conversation turns.
	invocations := make([]*evalset.Invocation, 0, len(evalCase.Conversation))
	tokenUsagePerTurn := make([]*TokenUsage, 0, len(evalCase.Conversation))
	var totalTokens, promptTokens, completionTokens int

	for i, origInv := range evalCase.Conversation {
		if origInv.UserContent == nil {
			continue
		}

		userMsg := origInv.UserContent.Content
		msg := model.NewUserMessage(userMsg)

		// Print user message.
		if *flagVerbose {
			log.Printf("      Turn %d [User]: %s", i+1, truncateStr(userMsg, 200))
		} else {
			log.Printf("      Turn %d: sending message (%d chars)...",
				i+1, len(userMsg))
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

		// Print assistant response.
		if *flagVerbose {
			log.Printf("      Turn %d [Assistant]: %s (p=%d, c=%d, t=%d)",
				i+1, truncateStr(response, 200),
				usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
		} else {
			log.Printf("      Turn %d: received response (%d chars, p=%d c=%d t=%d)",
				i+1, len(response),
				usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
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
		if evt.Response != nil {
			if evt.Response.Usage != nil {
				usage.PromptTokens = evt.Response.Usage.PromptTokens
				usage.CompletionTokens = evt.Response.Usage.CompletionTokens
				usage.TotalTokens = evt.Response.Usage.TotalTokens
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
	}
	return response.String(), usage
}

func (e *Evaluator) evaluateTokenEfficiency(
	baselineRuns, summaryRuns []*RunResult,
) *TokenEfficiency {
	var baselineTotal, summaryTotal int
	var baselinePrompt, summaryPrompt int
	var baselineCompletion, summaryCompletion int
	var baselineLastPrompt, summaryLastPrompt int

	for _, r := range baselineRuns {
		baselineTotal += r.TotalTokens
		baselinePrompt += r.PromptTokens
		baselineCompletion += r.CompletionTokens
		// Get last turn's prompt tokens.
		if len(r.TokenUsagePerTurn) > 0 {
			baselineLastPrompt += r.TokenUsagePerTurn[len(r.TokenUsagePerTurn)-1].PromptTokens
		}
	}
	for _, r := range summaryRuns {
		summaryTotal += r.TotalTokens
		summaryPrompt += r.PromptTokens
		summaryCompletion += r.CompletionTokens
		// Get last turn's prompt tokens.
		if len(r.TokenUsagePerTurn) > 0 {
			summaryLastPrompt += r.TokenUsagePerTurn[len(r.TokenUsagePerTurn)-1].PromptTokens
		}
	}

	n := len(baselineRuns)
	baselineAvg := baselineTotal / n
	summaryAvg := summaryTotal / n
	baselinePromptAvg := baselinePrompt / n
	summaryPromptAvg := summaryPrompt / n
	baselineCompletionAvg := baselineCompletion / n
	summaryCompletionAvg := summaryCompletion / n
	baselineLastPromptAvg := baselineLastPrompt / n
	summaryLastPromptAvg := summaryLastPrompt / n

	tokensSaved := baselineAvg - summaryAvg
	promptTokensSaved := baselinePromptAvg - summaryPromptAvg

	var savingsPercentage, promptSavingsPercentage, compressionRatio float64
	if baselineAvg > 0 {
		savingsPercentage = float64(tokensSaved) / float64(baselineAvg) * 100
		if summaryAvg > 0 {
			compressionRatio = float64(baselineAvg) / float64(summaryAvg)
		}
	}
	if baselinePromptAvg > 0 {
		promptSavingsPercentage = float64(promptTokensSaved) /
			float64(baselinePromptAvg) * 100
	}

	return &TokenEfficiency{
		BaselineTokens:           baselineAvg,
		SummaryTokens:            summaryAvg,
		TokensSaved:              tokensSaved,
		BaselinePromptTokens:     baselinePromptAvg,
		SummaryPromptTokens:      summaryPromptAvg,
		PromptTokensSaved:        promptTokensSaved,
		BaselineCompletionTokens: baselineCompletionAvg,
		SummaryCompletionTokens:  summaryCompletionAvg,
		BaselineLastPrompt:       baselineLastPromptAvg,
		SummaryLastPrompt:        summaryLastPromptAvg,
		SavingsPercentage:        savingsPercentage,
		PromptSavingsPercentage:  promptSavingsPercentage,
		CompressionRatio:         compressionRatio,
	}
}

func (e *Evaluator) evaluateConsistency(
	ctx context.Context,
	baselineRuns, summaryRuns []*RunResult,
) (*ConsistencyResult, error) {
	// Convert to invocation slices for Pass^k evaluator.
	baselineInvs := make([][]*evalset.Invocation, len(baselineRuns))
	for i, r := range baselineRuns {
		baselineInvs[i] = r.Invocations
	}
	summaryInvs := make([][]*evalset.Invocation, len(summaryRuns))
	for i, r := range summaryRuns {
		summaryInvs[i] = r.Invocations
	}

	result, err := e.passHatK.EvaluateMultiRun(ctx, baselineInvs, summaryInvs, nil)
	if err != nil {
		return nil, err
	}

	// Extract Pass^k values from details.
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

	// Determine consistency level.
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

func (e *Evaluator) evaluateRetention(
	ctx context.Context,
	baselineRuns, summaryRuns []*RunResult,
) (*RetentionResult, error) {
	baselineInvs := make([][]*evalset.Invocation, len(baselineRuns))
	for i, r := range baselineRuns {
		baselineInvs[i] = r.Invocations
	}
	summaryInvs := make([][]*evalset.Invocation, len(summaryRuns))
	for i, r := range summaryRuns {
		summaryInvs[i] = r.Invocations
	}

	// Prefer single-run API to preserve per-turn retention details.
	if len(baselineInvs) == 1 && len(summaryInvs) == 1 {
		result, err := e.retention.Evaluate(ctx, summaryInvs[0], baselineInvs[0], nil)
		if err != nil {
			return nil, err
		}
		return parseRetentionResult(result), nil
	}

	result, err := e.retention.EvaluateMultiRun(ctx, baselineInvs, summaryInvs, nil)
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
		return nil, fmt.Errorf("dataset path is required, please set -dataset")
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
	// Clean the path to remove trailing slashes for consistent filepath operations.
	datasetPath = filepath.Clean(datasetPath)

	info, err := os.Stat(datasetPath)
	if err != nil {
		return nil, fmt.Errorf("dataset path does not exist: %w", err)
	}

	// Only MT-Bench-101 is supported in this benchmark.
	var (
		loader   *dataset.DatasetLoader
		filename string
	)
	if info.Mode().IsRegular() {
		loader = dataset.NewDatasetLoader(filepath.Dir(datasetPath))
		filename = filepath.Base(datasetPath)
	} else {
		mtBenchPath := filepath.Join(datasetPath, "subjective/mtbench101.jsonl")
		if _, err := os.Stat(mtBenchPath); err != nil {
			return nil, fmt.Errorf("MT-Bench-101 file not found: %s", mtBenchPath)
		}
		loader = dataset.NewDatasetLoader(filepath.Dir(datasetPath))
		filename = filepath.Base(datasetPath) + "/subjective/mtbench101.jsonl"
	}

	log.Printf("Loading MT-Bench-101 dataset...")
	entries, err := loader.LoadMTBench101(filename, taskFilters...)
	if err != nil {
		return nil, fmt.Errorf("load MT-Bench-101: %w", err)
	}
	if len(entries) == 0 {
		if len(taskFilters) > 0 {
			return nil, fmt.Errorf(
				"no MT-Bench-101 entries matched task filter: %s, valid values: %s",
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
		len(cases),
		len(entries),
	)
	return cases, nil
}

// Output functions.

func aggregateResults(results *AggregatedResults) {
	if len(results.CaseResults) == 0 {
		return
	}

	var totalSavings, promptSavings, totalConsistency, totalRetention float64
	for _, cr := range results.CaseResults {
		totalSavings += cr.TokenEfficiency.SavingsPercentage
		promptSavings += cr.TokenEfficiency.PromptSavingsPercentage
		totalConsistency += cr.Consistency.Score
		totalRetention += cr.Retention.RetentionRate
	}

	n := float64(len(results.CaseResults))
	results.AvgTokenSavings = totalSavings / n
	results.AvgPromptSavings = promptSavings / n
	results.AvgConsistency = totalConsistency / n
	results.AvgRetention = totalRetention / n

	// Overall score: weighted combination.
	// Token score: normalize savings percentage to 0-1.
	tokenScore := results.AvgTokenSavings / 100
	if tokenScore > 1 {
		tokenScore = 1
	}
	if tokenScore < 0 {
		tokenScore = 0
	}

	results.OverallScore = weightConsistency*results.AvgConsistency +
		weightTokens*tokenScore +
		weightRetention*results.AvgRetention
}

func printResults(results *AggregatedResults) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("Summary Evaluation Results")
	fmt.Println(strings.Repeat("=", 60))

	fmt.Printf("\nModel: %s\n", results.Model)
	fmt.Printf("Cases: %d, Runs per mode: %d\n", results.NumCases, results.NumRuns)

	fmt.Println("\n--- Token Efficiency (30%) ---")
	fmt.Printf("Average Total Savings: %.1f%%\n", results.AvgTokenSavings)
	fmt.Printf("Average Prompt Savings: %.1f%%\n", results.AvgPromptSavings)

	fmt.Println("\n--- Response Consistency (50%) - Pass^k ---")
	fmt.Printf("Average Score: %.3f\n", results.AvgConsistency)

	fmt.Println("\n--- Information Retention (20%) ---")
	fmt.Printf("Average Retention: %.3f\n", results.AvgRetention)

	fmt.Println("\n--- Overall Score ---")
	fmt.Printf("%.3f (Consistency: %.0f%%, Tokens: %.0f%%, Retention: %.0f%%)\n",
		results.OverallScore,
		weightConsistency*100, weightTokens*100, weightRetention*100)

	fmt.Println("\n--- Per-Case Summary ---")
	for _, cr := range results.CaseResults {
		fmt.Printf("  %s: total=%.0f%%, prompt=%.0f%%, consistency=%.2f, "+
			"retention=%.2f\n",
			cr.CaseID,
			cr.TokenEfficiency.SavingsPercentage,
			cr.TokenEfficiency.PromptSavingsPercentage,
			cr.Consistency.Score,
			cr.Retention.RetentionRate)
	}
	fmt.Println(strings.Repeat("=", 60))
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

	// Token efficiency.
	fmt.Fprintf(f, "--- TOKEN EFFICIENCY ---\n")
	fmt.Fprintf(f, "Total Tokens:\n")
	fmt.Fprintf(f, "  Baseline: %d tokens\n", cr.TokenEfficiency.BaselineTokens)
	fmt.Fprintf(f, "  Summary:  %d tokens\n", cr.TokenEfficiency.SummaryTokens)
	fmt.Fprintf(f, "  Saved:    %d tokens (%.1f%%)\n",
		cr.TokenEfficiency.TokensSaved, cr.TokenEfficiency.SavingsPercentage)
	fmt.Fprintf(f, "Prompt Tokens:\n")
	fmt.Fprintf(f, "  Baseline: %d tokens\n", cr.TokenEfficiency.BaselinePromptTokens)
	fmt.Fprintf(f, "  Summary:  %d tokens\n", cr.TokenEfficiency.SummaryPromptTokens)
	fmt.Fprintf(f, "  Saved:    %d tokens (%.1f%%)\n",
		cr.TokenEfficiency.PromptTokensSaved,
		cr.TokenEfficiency.PromptSavingsPercentage)
	fmt.Fprintf(f, "Completion Tokens:\n")
	fmt.Fprintf(f, "  Baseline: %d tokens\n", cr.TokenEfficiency.BaselineCompletionTokens)
	fmt.Fprintf(f, "  Summary:  %d tokens\n", cr.TokenEfficiency.SummaryCompletionTokens)
	fmt.Fprintf(f, "Last Turn Prompt (most relevant for summary):\n")
	fmt.Fprintf(f, "  Baseline: %d tokens\n", cr.TokenEfficiency.BaselineLastPrompt)
	fmt.Fprintf(f, "  Summary:  %d tokens\n", cr.TokenEfficiency.SummaryLastPrompt)
	fmt.Fprintf(f, "Compression: %.2fx\n\n", cr.TokenEfficiency.CompressionRatio)

	// Consistency.
	fmt.Fprintf(f, "--- CONSISTENCY (Pass^k) ---\n")
	fmt.Fprintf(f, "Score: %.3f (%s)\n", cr.Consistency.Score,
		cr.Consistency.ConsistencyLevel)
	fmt.Fprintf(f, "Pass^1: %.3f\n", cr.Consistency.PassHat1)
	if cr.Consistency.PassHat2 > 0 {
		fmt.Fprintf(f, "Pass^2: %.3f\n", cr.Consistency.PassHat2)
	}
	if cr.Consistency.PassHat4 > 0 {
		fmt.Fprintf(f, "Pass^4: %.3f\n", cr.Consistency.PassHat4)
	}
	fmt.Fprintf(f, "Success: %d/%d runs\n", cr.Consistency.SuccessCount,
		cr.Consistency.TotalRuns)
	fmt.Fprintf(f, "Variance: %.4f\n\n", cr.Consistency.Variance)

	// Retention.
	fmt.Fprintf(f, "--- INFORMATION RETENTION ---\n")
	fmt.Fprintf(f, "Retention Rate: %.3f\n", cr.Retention.RetentionRate)
	fmt.Fprintf(f, "Key Info: %d found, %d retained\n",
		cr.Retention.KeyInfoCount, cr.Retention.RetainedCount)
	if len(cr.Retention.MissingInfo) > 0 {
		fmt.Fprintf(f, "Missing Info:\n")
		for _, info := range cr.Retention.MissingInfo {
			fmt.Fprintf(f, "  - %s\n", info)
		}
	}
	fmt.Fprintf(f, "\n")

	// Detailed conversation logs.
	if len(cr.BaselineRuns) > 0 {
		run := cr.BaselineRuns[0]
		fmt.Fprintf(f, "--- BASELINE MODE (total=%d, prompt=%d, completion=%d, %v) ---\n",
			run.TotalTokens, run.PromptTokens, run.CompletionTokens,
			run.Duration.Round(time.Millisecond))
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

	if len(cr.SummaryRuns) > 0 {
		run := cr.SummaryRuns[0]
		fmt.Fprintf(f, "--- SUMMARY MODE (total=%d, prompt=%d, completion=%d, %v) ---\n",
			run.TotalTokens, run.PromptTokens, run.CompletionTokens,
			run.Duration.Round(time.Millisecond))
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

// saveCheckpoint saves the current progress to a checkpoint file.
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

// loadCheckpoint loads the previous checkpoint if it exists.
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

func parseRetentionResult(result *evaluator.EvaluateResult) *RetentionResult {
	if result == nil {
		return &RetentionResult{}
	}

	r := &RetentionResult{RetentionRate: result.OverallScore}
	if result.Details == nil {
		return r
	}

	details := result.Details
	r.KeyInfoCount = asInt(firstNonNil(details["key_info_count"], details["total_key_info"]))
	r.RetainedCount = asInt(firstNonNil(details["retained_count"], details["total_retained"]))
	r.MissingInfo = asStringSlice(firstNonNil(details["missing_info"], details["unique_missing"]))
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

// truncateStr truncates a string to maxLen characters, replacing newlines.
func truncateStr(s string, maxLen int) string {
	// Replace newlines with spaces for single-line display.
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// Collapse multiple spaces.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
