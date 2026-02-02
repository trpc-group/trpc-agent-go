//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scenarios

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// AutoEvaluator evaluates using automatic memory extraction.
// Memories are extracted automatically in background, agent can only search.
type AutoEvaluator struct {
	model         model.Model
	evalModel     model.Model
	memoryService memory.Service
	config        Config
	llmJudge      *metrics.LLMJudge
}

// NewAutoEvaluator creates a new auto evaluator.
func NewAutoEvaluator(
	m, evalModel model.Model,
	memSvc memory.Service,
	cfg Config,
) *AutoEvaluator {
	e := &AutoEvaluator{
		model:         m,
		evalModel:     evalModel,
		memoryService: memSvc,
		config:        cfg,
	}
	if cfg.EnableLLMJudge && evalModel != nil {
		e.llmJudge = metrics.NewLLMJudge(evalModel)
	}
	return e
}

// Name returns the evaluator name.
func (e *AutoEvaluator) Name() string {
	return "auto"
}

// Evaluate runs evaluation on a sample using auto memory extraction.
func (e *AutoEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	userKey := memory.UserKey{
		AppName: "memory-eval-auto",
		UserID:  sample.SampleID,
	}

	// Clear existing memories for this sample.
	_ = e.memoryService.ClearMemories(ctx, userKey)

	// Phase 1: Feed conversation to auto extractor.
	if err := e.triggerAutoExtraction(ctx, userKey, sample); err != nil {
		return nil, fmt.Errorf("trigger auto extraction: %w", err)
	}

	// Wait for async extraction to complete.
	// Use a longer wait time since LLM extraction takes time.
	// Each session may take 2-5 seconds depending on the model.
	numSessions := len(sample.Conversation)
	waitTime := time.Duration(numSessions) * 3 * time.Second
	if waitTime < 5*time.Second {
		waitTime = 5 * time.Second
	}
	if waitTime > 60*time.Second {
		waitTime = 60 * time.Second
	}
	time.Sleep(waitTime)

	// Phase 2: Answer QA questions using search-only agent.
	result := &SampleResult{
		SampleID:  sample.SampleID,
		QAResults: make([]*QAResult, 0, len(sample.QA)),
	}
	catAgg := metrics.NewCategoryAggregator()

	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(ctx, userKey, qa)
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

// triggerAutoExtraction feeds conversation to the auto extractor.
// Each conversation session is enqueued separately to avoid token limits.
func (e *AutoEvaluator) triggerAutoExtraction(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	// Process each conversation session separately to avoid token limits.
	for _, convSession := range sample.Conversation {
		sess := &session.Session{
			ID:        fmt.Sprintf("eval-%s-%s", sample.SampleID, convSession.SessionID),
			AppName:   userKey.AppName,
			UserID:    userKey.UserID,
			Events:    make([]event.Event, 0, len(convSession.Turns)),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Convert conversation turns to events.
		eventTime := time.Now()
		for idx, turn := range convSession.Turns {
			role := model.RoleUser
			if strings.ToLower(turn.Speaker) == "user2" ||
				strings.Contains(strings.ToLower(turn.Speaker), "assistant") {
				role = model.RoleAssistant
			}

			evt := event.Event{
				ID:        fmt.Sprintf("%s-%d", convSession.SessionID, idx),
				Timestamp: eventTime,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    role,
								Content: turn.Text,
							},
						},
					},
				},
			}
			sess.Events = append(sess.Events, evt)
			eventTime = eventTime.Add(time.Millisecond)
		}

		// Enqueue this session for extraction.
		if err := e.memoryService.EnqueueAutoMemoryJob(ctx, sess); err != nil {
			return fmt.Errorf("enqueue auto memory job for session %s: %w",
				convSession.SessionID, err)
		}
	}
	return nil
}

// evaluateQA evaluates a single QA using search-only agent.
func (e *AutoEvaluator) evaluateQA(
	ctx context.Context,
	userKey memory.UserKey,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()

	// Search for relevant memories.
	memories, err := e.memoryService.SearchMemories(ctx, userKey, qa.Question)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	// Build context from retrieved memories.
	var memContext strings.Builder
	if len(memories) == 0 {
		memContext.WriteString("No relevant memories found.")
	} else {
		for i, mem := range memories {
			if i >= e.config.TopK {
				break
			}
			fmt.Fprintf(&memContext, "[Memory %d] %s\n", i+1, mem.Memory.Memory)
		}
	}

	// Generate answer.
	prompt := fmt.Sprintf(`Based on the following automatically extracted memories, answer the question.

## Memories
%s

## Question
%s

## Instructions
- Answer based ONLY on the information in the memories.
- If the answer cannot be found, say "The information is not available."
- Be concise and direct.

Answer:`, memContext.String(), qa.Question)

	predicted, _, err := e.generateAnswer(ctx, prompt)
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
	}, nil
}

// generateAnswer generates an answer using the model.
func (e *AutoEvaluator) generateAnswer(
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
