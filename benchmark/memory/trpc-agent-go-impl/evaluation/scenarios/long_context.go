//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package scenarios provides evaluation scenarios for memory benchmark.
package scenarios

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// ScenarioType represents the evaluation scenario type.
type ScenarioType string

// Scenario types.
const (
	ScenarioLongContext ScenarioType = "long_context"
	ScenarioRAGMemory   ScenarioType = "rag_memory"
	ScenarioAgentic     ScenarioType = "agentic"
	ScenarioAuto        ScenarioType = "auto"
)

// RAGMode represents the RAG retrieval mode.
type RAGMode string

// RAG modes.
const (
	RAGModeFull        RAGMode = "full"
	RAGModeObservation RAGMode = "observation"
	RAGModeSummary     RAGMode = "summary"
	RAGModeFallback    RAGMode = "fallback"
)

// Config holds scenario evaluation configuration.
type Config struct {
	Scenario          ScenarioType
	RAGMode           RAGMode
	MaxContext        int
	TopK              int
	EnableLLMJudge    bool
	Verbose           bool
	SessionEventLimit int
	QAWithHistory     bool

	// Debug options (primarily for benchmark diagnosis).
	DebugDumpMemories bool
	DebugMemLimit     int
	DebugQALimit      int
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		Scenario:          ScenarioLongContext,
		RAGMode:           RAGModeFull,
		MaxContext:        128000,
		TopK:              5,
		EnableLLMJudge:    false,
		Verbose:           false,
		SessionEventLimit: 1000,
		QAWithHistory:     false,
		DebugDumpMemories: false,
		DebugMemLimit:     0,
		DebugQALimit:      0,
	}
}

// QAResult holds the result of a single QA evaluation.
type QAResult struct {
	QuestionID string            `json:"question_id"`
	Question   string            `json:"question"`
	Category   string            `json:"category"`
	Expected   string            `json:"expected"`
	Predicted  string            `json:"predicted"`
	Metrics    metrics.QAMetrics `json:"metrics"`
	LatencyMs  int64             `json:"latency_ms"`
	TokensUsed int               `json:"tokens_used,omitempty"`
}

// SampleResult holds evaluation results for a single sample.
type SampleResult struct {
	SampleID    string                             `json:"sample_id"`
	QAResults   []*QAResult                        `json:"qa_results"`
	ByCategory  map[string]metrics.CategoryMetrics `json:"by_category"`
	Overall     metrics.CategoryMetrics            `json:"overall"`
	TotalTimeMs int64                              `json:"total_time_ms"`
}

// Evaluator is the interface for scenario evaluators.
type Evaluator interface {
	// Evaluate runs evaluation on a sample.
	Evaluate(ctx context.Context, sample *dataset.LoCoMoSample) (*SampleResult, error)
	// Name returns the evaluator name.
	Name() string
}

const (
	longContextAppName = "memory-eval-long-context"

	longContextMaxTokens = 500
)

var longContextInstruction = "Answer the question based ONLY on the conversation history. " +
	"If the answer cannot be found in the conversation, say \"The information is not available in the conversation.\". " +
	"Be concise and direct. Output ONLY the answer."

// LongContextEvaluator evaluates using full conversation context.
// The full conversation is persisted into session via agent.WithMessages(...).
// Each QA is evaluated in an isolated session to avoid cross-QA contamination.
type LongContextEvaluator struct {
	model        model.Model
	evalModel    model.Model
	config       Config
	llmJudge     *metrics.LLMJudge
	tokenCounter *metrics.TokenCounter
}

// NewLongContextEvaluator creates a new long context evaluator.
func NewLongContextEvaluator(m, evalModel model.Model, cfg Config) *LongContextEvaluator {
	e := &LongContextEvaluator{
		model:        m,
		evalModel:    evalModel,
		config:       cfg,
		tokenCounter: metrics.NewTokenCounter(),
	}
	if cfg.EnableLLMJudge && evalModel != nil {
		e.llmJudge = metrics.NewLLMJudge(evalModel)
	}
	return e
}

// Name returns the evaluator name.
func (e *LongContextEvaluator) Name() string {
	return "long_context"
}

// Evaluate runs evaluation on a sample using runner -> agent -> model.
func (e *LongContextEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	seed := fullConversationMessages(sample)
	seedTokens := estimateTokens(e.tokenCounter, seed)

	ag := newLongContextAgent(e.model)
	r := runner.NewRunner(
		longContextAppName,
		ag,
		runner.WithSessionService(newSessionService(e.config)),
	)
	defer r.Close()

	result := &SampleResult{
		SampleID:  sample.SampleID,
		QAResults: make([]*QAResult, 0, len(sample.QA)),
	}
	catAgg := metrics.NewCategoryAggregator()

	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(ctx, r, sample.SampleID, seed, seedTokens, qa)
		if err != nil {
			return nil, fmt.Errorf("evaluate QA %s: %w", qa.QuestionID, err)
		}
		result.QAResults = append(result.QAResults, qaResult)
		catAgg.Add(qa.Category, qaResult.Metrics)
	}

	result.ByCategory = catAgg.GetCategoryMetrics()
	result.Overall = catAgg.GetOverall()
	result.TotalTimeMs = time.Since(startTime).Milliseconds()
	return result, nil
}

func newLongContextAgent(m model.Model) agent.Agent {
	genConfig := model.GenerationConfig{Stream: false, MaxTokens: intPtr(longContextMaxTokens)}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(genConfig),
	)
}

func (e *LongContextEvaluator) evaluateQA(
	ctx context.Context,
	r runner.Runner,
	userID string,
	seed []model.Message,
	seedTokens int,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()
	sessionID := fmt.Sprintf("qa-%s", qa.QuestionID)
	msg := model.NewUserMessage(qa.Question)

	predicted, err := runWithRateLimitRetry(ctx, func() (<-chan *event.Event, error) {
		return r.Run(
			ctx,
			userID,
			sessionID,
			msg,
			agent.WithMessages(seed),
			agent.WithInstruction(longContextInstruction),
		)
	})
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}

	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(ctx, qa.Question, qa.Answer, predicted)
		if err == nil {
			if judgeResult.Correct {
				m.LLMScore = judgeResult.Confidence
			} else {
				m.LLMScore = 0
			}
		}
	}

	tokensUsed := seedTokens + e.tokenCounter.Count(qa.Question) + e.tokenCounter.Count(predicted)
	return &QAResult{
		QuestionID: qa.QuestionID,
		Question:   qa.Question,
		Category:   qa.Category,
		Expected:   qa.Answer,
		Predicted:  predicted,
		Metrics:    m,
		LatencyMs:  time.Since(start).Milliseconds(),
		TokensUsed: tokensUsed,
	}, nil
}

func fullConversationMessages(sample *dataset.LoCoMoSample) []model.Message {
	if sample == nil {
		return nil
	}

	msgs := make([]model.Message, 0, 64)
	for _, sess := range sample.Conversation {
		msgs = append(msgs, sessionMessages(sample, sess)...)
	}
	return msgs
}

func estimateTokens(counter *metrics.TokenCounter, msgs []model.Message) int {
	if counter == nil {
		return 0
	}

	total := 0
	for _, m := range msgs {
		if m.Content == "" {
			continue
		}
		total += counter.Count(m.Content)
	}
	return total
}

func intPtr(i int) *int {
	return &i
}
