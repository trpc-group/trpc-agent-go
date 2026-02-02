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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
	RAGModeAutoExtract RAGMode = "auto_extract"
)

// Config holds scenario evaluation configuration.
type Config struct {
	Scenario       ScenarioType
	RAGMode        RAGMode
	MaxContext     int
	TopK           int
	EnableLLMJudge bool
	Verbose        bool
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		Scenario:       ScenarioLongContext,
		RAGMode:        RAGModeFull,
		MaxContext:     128000,
		TopK:           5,
		EnableLLMJudge: false,
		Verbose:        false,
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

// LongContextEvaluator evaluates using full conversation context.
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

// Evaluate runs evaluation on a sample.
func (e *LongContextEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	// Build full conversation context.
	fullConv := sample.BuildFullConversation()
	result := &SampleResult{
		SampleID:  sample.SampleID,
		QAResults: make([]*QAResult, 0, len(sample.QA)),
	}
	catAgg := metrics.NewCategoryAggregator()
	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(ctx, fullConv, qa)
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

func (e *LongContextEvaluator) evaluateQA(
	ctx context.Context,
	context string,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()
	prompt := buildQAPrompt(context, qa.Question)
	predicted, tokensUsed, err := e.generateAnswer(ctx, prompt)
	if err != nil {
		return nil, err
	}
	// Calculate metrics.
	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}
	// LLM judge if enabled.
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(ctx, qa.Question, qa.Answer, predicted)
		if err == nil && judgeResult.Correct {
			m.LLMScore = judgeResult.Confidence
		}
	}
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

func (e *LongContextEvaluator) generateAnswer(
	ctx context.Context,
	prompt string,
) (string, int, error) {
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleUser, Content: prompt},
		},
		GenerationConfig: model.GenerationConfig{
			Stream:    false,
			MaxTokens: intPtr(500),
		},
	}
	respCh, err := e.model.GenerateContent(ctx, req)
	if err != nil {
		return "", 0, fmt.Errorf("generate content: %w", err)
	}
	var answerBuilder strings.Builder
	var totalTokens int
	for resp := range respCh {
		if resp.Error != nil {
			return "", 0, fmt.Errorf("response error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			answerBuilder.WriteString(resp.Choices[0].Message.Content)
		}
		if resp.Usage != nil {
			totalTokens = resp.Usage.TotalTokens
		}
	}
	return answerBuilder.String(), totalTokens, nil
}

func buildQAPrompt(context, question string) string {
	return fmt.Sprintf(`Based on the following conversation history, answer the question.

## Conversation History
%s

## Question
%s

## Instructions
- Answer the question based ONLY on the information in the conversation history.
- If the answer cannot be found in the conversation, say "The information is not available in the conversation."
- Be concise and direct in your answer.

Answer:`, context, question)
}

func intPtr(i int) *int {
	return &i
}
