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
	ScenarioAgentic     ScenarioType = "agentic"
	ScenarioAuto        ScenarioType = "auto"
)

// Config holds scenario evaluation configuration.
type Config struct {
	Scenario          ScenarioType
	MaxContext        int
	EnableLLMJudge    bool
	Verbose           bool
	SessionEventLimit int

	// QAHistoryTurns controls how many recent conversation
	// turns (across all sessions) are injected as context
	// when answering QA questions. 0 means no history
	// (default, pure memory retrieval).
	// Only applies to agentic and auto scenarios.
	QAHistoryTurns int

	// Debug options (primarily for benchmark diagnosis).
	DebugDumpMemories bool
	DebugMemLimit     int
	DebugQALimit      int
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		Scenario:          ScenarioLongContext,
		MaxContext:        128000,
		EnableLLMJudge:    false,
		Verbose:           false,
		SessionEventLimit: 1000,
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
	TokenUsage *TokenUsage       `json:"token_usage,omitempty"`
}

// SampleResult holds evaluation results for a single sample.
type SampleResult struct {
	SampleID    string                             `json:"sample_id"`
	QAResults   []*QAResult                        `json:"qa_results"`
	ByCategory  map[string]metrics.CategoryMetrics `json:"by_category"`
	Overall     metrics.CategoryMetrics            `json:"overall"`
	TotalTimeMs int64                              `json:"total_time_ms"`
	TokenUsage  *TokenUsage                        `json:"token_usage,omitempty"`
}

// Evaluator is the interface for scenario evaluators.
type Evaluator interface {
	// Evaluate runs evaluation on a sample.
	Evaluate(ctx context.Context, sample *dataset.LoCoMoSample) (*SampleResult, error)
	// Name returns the evaluator name.
	Name() string
}

const longContextMaxTokens = 500

// longContextPromptTemplate is the prompt template aligned with mem0's
// evaluation approach. The entire conversation transcript and question
// are inlined into a single system message so the model treats the
// transcript as reference material rather than a chat to continue.
const longContextPromptTemplate = `You are an intelligent memory assistant tasked with retrieving accurate information from a conversation transcript.

# CONTEXT:
You have access to the full conversation transcript between speakers.
The transcript contains timestamped sessions that may be relevant to answering the question.

# INSTRUCTIONS:
1. Carefully analyze the entire conversation transcript.
2. Pay special attention to the SessionDate lines to determine when events occurred.
3. If the question asks about a specific event or fact, look for direct evidence in the transcript.
4. If the transcript contains contradictory information, prioritize the most recent information.
5. If there is a question about time references (like "last year", "two months ago", etc.),
   calculate the actual date based on the SessionDate. For example, if a session from
   4 May 2022 mentions "went to India last year", then the trip occurred in 2021.
6. CRITICAL: Always convert relative time references to ABSOLUTE dates, months, or years.
   - "last year" -> "2022" (not "Last year")
   - "this month" -> "July 2023" (not "This month")
   - "next month" -> "August 2023" (not "Next month")
   - "seven years" -> "Since 2016" or "7 years"
   NEVER output relative time words as the answer.
7. Focus only on the content of the transcript. Do not confuse character
   names mentioned in the transcript with real-world individuals.
8. The answer should be less than 5-6 words.
9. If the answer cannot be found in the transcript, reply with "%s" exactly.

# APPROACH (Think step by step):
1. First, examine all parts of the transcript that contain information related to the question.
2. Examine the SessionDate and content of these parts carefully.
3. Look for explicit mentions of dates, times, locations, or events that answer the question.
4. If the answer requires calculation (e.g., converting relative time references), show your work.
5. Formulate a precise, concise answer based solely on the evidence in the transcript.
6. Double-check that your answer directly addresses the question asked.
7. Ensure your final answer uses ABSOLUTE dates/years, never relative words like "last year" or "this month".

# TRANSCRIPT:

%s

Question: %s
Answer:`

// LongContextEvaluator evaluates using full conversation context.
// It calls the model directly with a single system message containing
// the entire transcript and question, aligned with mem0's approach.
type LongContextEvaluator struct {
	model        model.Model
	evalModel    model.Model
	config       Config
	llmJudge     *metrics.LLMJudge
	tokenCounter *metrics.TokenCounter
}

// NewLongContextEvaluator creates a new long context evaluator.
func NewLongContextEvaluator(
	m, evalModel model.Model, cfg Config,
) *LongContextEvaluator {
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

// Evaluate runs evaluation on a sample by calling the model directly.
func (e *LongContextEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	transcript := buildTranscript(sample)
	transcriptTokens := e.tokenCounter.Count(transcript)

	result := &SampleResult{
		SampleID:  sample.SampleID,
		QAResults: make([]*QAResult, 0, len(sample.QA)),
	}
	catAgg := metrics.NewCategoryAggregator()
	var sampleUsage TokenUsage

	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(
			ctx, transcript, transcriptTokens, qa,
		)
		if err != nil {
			return nil, fmt.Errorf("evaluate QA %s: %w", qa.QuestionID, err)
		}
		result.QAResults = append(result.QAResults, qaResult)
		catAgg.Add(qa.Category, qaResult.Metrics)
		if qaResult.TokenUsage != nil {
			sampleUsage.Add(*qaResult.TokenUsage)
		}
	}

	result.ByCategory = catAgg.GetCategoryMetrics()
	result.Overall = catAgg.GetOverall()
	result.TotalTimeMs = time.Since(startTime).Milliseconds()
	result.TokenUsage = &sampleUsage
	return result, nil
}

func (e *LongContextEvaluator) evaluateQA(
	ctx context.Context,
	transcript string,
	transcriptTokens int,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()

	prompt := fmt.Sprintf(
		longContextPromptTemplate,
		fallbackAnswer,
		transcript,
		qa.Question,
	)

	maxTokens := longContextMaxTokens
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(prompt),
		},
		GenerationConfig: model.GenerationConfig{
			Stream:      false,
			MaxTokens:   &maxTokens,
			Temperature: float64Ptr(0),
		},
	}

	result, err := runModelWithRateLimitRetry(
		ctx, e.model, req,
	)
	if err != nil {
		return nil, fmt.Errorf("model generate: %w", err)
	}
	predicted := result.text

	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(
			ctx, qa.Question, qa.Answer, predicted,
		)
		if err == nil {
			if judgeResult.Correct {
				m.LLMScore = judgeResult.Confidence
			} else {
				m.LLMScore = 0
			}
		}
	}

	tokensUsed := transcriptTokens +
		e.tokenCounter.Count(qa.Question) +
		e.tokenCounter.Count(predicted)

	var tu *TokenUsage
	if result.usage != nil {
		tu = &TokenUsage{
			PromptTokens:     result.usage.PromptTokens,
			CompletionTokens: result.usage.CompletionTokens,
			TotalTokens:      result.usage.TotalTokens,
			LLMCalls:         1,
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
		TokenUsage: tu,
	}, nil
}

// runModelResult holds the output of a model call.
type runModelResult struct {
	text  string
	usage *model.Usage
}

// runModelWithRateLimitRetry calls model.GenerateContent with
// rate-limit retry logic.
func runModelWithRateLimitRetry(
	ctx context.Context,
	m model.Model,
	req *model.Request,
) (runModelResult, error) {
	backoff := rateLimitInitialBackoff
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		respCh, err := m.GenerateContent(ctx, req)
		if err != nil {
			if isRateLimitError(err) {
				if sleepErr := sleepWithContext(
					ctx, backoff,
				); sleepErr != nil {
					return runModelResult{}, sleepErr
				}
				backoff = minDuration(
					backoff*time.Duration(rateLimitBackoffMultiplier),
					rateLimitMaxBackoff,
				)
				continue
			}
			return runModelResult{}, err
		}

		var lastContent string
		var lastUsage *model.Usage
		for resp := range respCh {
			if resp == nil {
				continue
			}
			if resp.Error != nil {
				errMsg := resp.Error.Message
				if isRateLimitError(
					fmt.Errorf("%s", errMsg),
				) {
					// Drain remaining responses.
					for range respCh {
					}
					if sleepErr := sleepWithContext(
						ctx, backoff,
					); sleepErr != nil {
						return runModelResult{}, sleepErr
					}
					backoff = minDuration(
						backoff*time.Duration(
							rateLimitBackoffMultiplier,
						),
						rateLimitMaxBackoff,
					)
					lastContent = ""
					lastUsage = nil
					break
				}
				return runModelResult{}, fmt.Errorf(
					"model error: %s", errMsg,
				)
			}
			if len(resp.Choices) > 0 {
				c := resp.Choices[0].Message.Content
				if c != "" {
					lastContent = c
				}
			}
			if resp.Usage != nil {
				lastUsage = resp.Usage
			}
		}
		if lastContent != "" {
			return runModelResult{
				text:  strings.TrimSpace(lastContent),
				usage: lastUsage,
			}, nil
		}
		// Empty response (possibly rate limit or model overload).
		// Apply backoff before retrying.
		if attempt < maxRateLimitRetries {
			if sleepErr := sleepWithContext(
				ctx, backoff,
			); sleepErr != nil {
				return runModelResult{}, sleepErr
			}
			backoff = minDuration(
				backoff*time.Duration(
					rateLimitBackoffMultiplier,
				),
				rateLimitMaxBackoff,
			)
			continue
		}
	}
	return runModelResult{}, fmt.Errorf("model returned empty response after retries")
}

// buildTranscript concatenates all sessions into a single transcript
// string with SessionDate headers and Speaker labels.
func buildTranscript(sample *dataset.LoCoMoSample) string {
	if sample == nil {
		return ""
	}

	b := strings.Builder{}
	for i, sess := range sample.Conversation {
		if i > 0 {
			b.WriteString("\n")
		}
		if strings.TrimSpace(sess.SessionDate) != "" {
			fmt.Fprintf(&b, "%s: %s\n",
				seedSessionDateLabel, sess.SessionDate)
		}
		for _, turn := range sess.Turns {
			content := strings.TrimSpace(turn.Text)
			if content == "" {
				continue
			}
			speaker := strings.TrimSpace(turn.Speaker)
			if speaker == "" {
				speaker = "Unknown"
			}
			fmt.Fprintf(&b, "%s: %s\n", speaker, content)
		}
	}
	return strings.TrimSpace(b.String())
}

func intPtr(i int) *int {
	return &i
}

func float64Ptr(f float64) *float64 {
	return &f
}
