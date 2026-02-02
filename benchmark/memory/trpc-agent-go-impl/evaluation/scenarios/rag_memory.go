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
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// RAGMemoryEvaluator evaluates using RAG with memory service.
type RAGMemoryEvaluator struct {
	model         model.Model
	evalModel     model.Model
	memoryService memory.Service
	config        Config
	llmJudge      *metrics.LLMJudge
	tokenCounter  *metrics.TokenCounter
}

// NewRAGMemoryEvaluator creates a new RAG memory evaluator.
func NewRAGMemoryEvaluator(
	m, evalModel model.Model,
	memSvc memory.Service,
	cfg Config,
) *RAGMemoryEvaluator {
	e := &RAGMemoryEvaluator{
		model:         m,
		evalModel:     evalModel,
		memoryService: memSvc,
		config:        cfg,
		tokenCounter:  metrics.NewTokenCounter(),
	}
	if cfg.EnableLLMJudge && evalModel != nil {
		e.llmJudge = metrics.NewLLMJudge(evalModel)
	}
	return e
}

// Name returns the evaluator name.
func (e *RAGMemoryEvaluator) Name() string {
	return fmt.Sprintf("rag_memory_%s", e.config.RAGMode)
}

// Evaluate runs evaluation on a sample.
func (e *RAGMemoryEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	// Populate memories based on RAG mode.
	if err := e.populateMemories(ctx, sample); err != nil {
		return nil, fmt.Errorf("populate memories: %w", err)
	}
	result := &SampleResult{
		SampleID:  sample.SampleID,
		QAResults: make([]*QAResult, 0, len(sample.QA)),
	}
	catAgg := metrics.NewCategoryAggregator()
	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(ctx, sample, qa)
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

func (e *RAGMemoryEvaluator) populateMemories(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) error {
	userKey := memory.UserKey{
		AppName: "memory-eval",
		UserID:  sample.SampleID,
	}
	// Clear existing memories.
	_ = e.memoryService.ClearMemories(ctx, userKey)
	switch e.config.RAGMode {
	case RAGModeObservation:
		return e.populateObservations(ctx, userKey, sample)
	case RAGModeSummary:
		return e.populateSummaries(ctx, userKey, sample)
	case RAGModeAutoExtract:
		return e.populateAutoExtract(ctx, userKey, sample)
	default:
		// Full mode: store all dialog turns.
		return e.populateFullDialog(ctx, userKey, sample)
	}
}

func (e *RAGMemoryEvaluator) populateObservations(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	for _, sess := range sample.Conversation {
		if sess.Observation == "" {
			continue
		}
		topics := []string{"session:" + sess.SessionID}
		if err := e.memoryService.AddMemory(ctx, userKey, sess.Observation, topics); err != nil {
			return fmt.Errorf("add observation for session %s: %w", sess.SessionID, err)
		}
	}
	return nil
}

func (e *RAGMemoryEvaluator) populateSummaries(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	for _, sess := range sample.Conversation {
		if sess.Summary == "" {
			continue
		}
		topics := []string{"session:" + sess.SessionID}
		if err := e.memoryService.AddMemory(ctx, userKey, sess.Summary, topics); err != nil {
			return fmt.Errorf("add summary for session %s: %w", sess.SessionID, err)
		}
	}
	return nil
}

func (e *RAGMemoryEvaluator) populateFullDialog(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	for _, sess := range sample.Conversation {
		// Store each session as a memory.
		var content string
		for _, turn := range sess.Turns {
			content += fmt.Sprintf("%s: %s\n", turn.Speaker, turn.Text)
		}
		if content == "" {
			continue
		}
		topics := []string{"session:" + sess.SessionID}
		if sess.SessionDate != "" {
			topics = append(topics, "date:"+sess.SessionDate)
		}
		if err := e.memoryService.AddMemory(ctx, userKey, content, topics); err != nil {
			return fmt.Errorf("add dialog for session %s: %w", sess.SessionID, err)
		}
	}
	return nil
}

func (e *RAGMemoryEvaluator) populateAutoExtract(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	// For auto-extract mode, we simulate what the extractor would produce.
	// This extracts key facts from each session.
	for _, sess := range sample.Conversation {
		// Use observation if available, otherwise use summary.
		content := sess.Observation
		if content == "" {
			content = sess.Summary
		}
		if content == "" {
			// Build content from turns.
			for _, turn := range sess.Turns {
				content += fmt.Sprintf("%s: %s\n", turn.Speaker, turn.Text)
			}
		}
		if content == "" {
			continue
		}
		topics := []string{"session:" + sess.SessionID, "auto_extracted"}
		if err := e.memoryService.AddMemory(ctx, userKey, content, topics); err != nil {
			return fmt.Errorf("add extracted memory for session %s: %w", sess.SessionID, err)
		}
	}
	return nil
}

func (e *RAGMemoryEvaluator) evaluateQA(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()
	// Retrieve relevant memories.
	userKey := memory.UserKey{
		AppName: "memory-eval",
		UserID:  sample.SampleID,
	}
	memories, err := e.memoryService.SearchMemories(ctx, userKey, qa.Question)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	// Build context from retrieved memories.
	ragContext := e.buildRAGContext(memories, e.config.TopK)
	prompt := buildRAGQAPrompt(ragContext, qa.Question)
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

func (e *RAGMemoryEvaluator) buildRAGContext(
	memories []*memory.Entry,
	topK int,
) string {
	if len(memories) == 0 {
		return "No relevant memories found."
	}
	if len(memories) > topK {
		memories = memories[:topK]
	}
	var b strings.Builder
	for i, mem := range memories {
		fmt.Fprintf(&b, "[Memory %d]\n%s\n\n", i+1, mem.Memory.Memory)
	}
	return b.String()
}

func (e *RAGMemoryEvaluator) generateAnswer(
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

func buildRAGQAPrompt(context, question string) string {
	return fmt.Sprintf(`Based on the following retrieved memories, answer the question.

## Retrieved Memories
%s

## Question
%s

## Instructions
- Answer the question based ONLY on the information in the retrieved memories.
- If the answer cannot be found in the memories, say "The information is not available."
- Be concise and direct in your answer.

Answer:`, context, question)
}
