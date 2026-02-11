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
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	ragAppName = "memory-eval-rag"

	ragMaxTokens = 500

	ragUnknownDate      = "unknown"
	ragDatePrefixFormat = "[DATE: %s] "
)

func ragWithDatePrefix(sessionDate, content string) string {
	date := strings.TrimSpace(sessionDate)
	if date == "" {
		date = ragUnknownDate
	}
	return fmt.Sprintf(ragDatePrefixFormat, date) + content
}

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

// Evaluate runs evaluation on a sample by searching memories and
// calling the model directly with a single system prompt.
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
			if e.config.Verbose {
				log.Printf(
					"Warning: evaluate QA %s failed: %v",
					qa.QuestionID, err,
				)
			}
			qaResult = &QAResult{
				QuestionID: qa.QuestionID,
				Question:   qa.Question,
				Category:   qa.Category,
				Expected:   qa.Answer,
				Predicted:  fallbackAnswer,
				Metrics:    metrics.QAMetrics{F1: 0, BLEU: 0},
			}
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
		AppName: ragAppName,
		UserID:  sample.SampleID,
	}
	// Clear existing memories.
	_ = e.memoryService.ClearMemories(ctx, userKey)
	switch e.config.RAGMode {
	case RAGModeObservation:
		return e.populateObservations(ctx, userKey, sample)
	case RAGModeSummary:
		return e.populateSummaries(ctx, userKey, sample)
	case RAGModeFallback:
		return e.populateFallback(ctx, userKey, sample)
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
		if strings.TrimSpace(sess.SessionDate) != "" {
			topics = append(topics, "date:"+sess.SessionDate)
		}
		content := ragWithDatePrefix(sess.SessionDate, sess.Observation)
		if err := e.memoryService.AddMemory(ctx, userKey, content, topics); err != nil {
			return fmt.Errorf(
				"add observation for session %s: %w",
				sess.SessionID,
				err,
			)
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
		if strings.TrimSpace(sess.SessionDate) != "" {
			topics = append(topics, "date:"+sess.SessionDate)
		}
		content := ragWithDatePrefix(sess.SessionDate, sess.Summary)
		if err := e.memoryService.AddMemory(ctx, userKey, content, topics); err != nil {
			return fmt.Errorf(
				"add summary for session %s: %w",
				sess.SessionID,
				err,
			)
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
		var b strings.Builder
		for _, turn := range sess.Turns {
			if turn.Text == "" {
				continue
			}
			b.WriteString(turn.Speaker)
			b.WriteString(": ")
			b.WriteString(turn.Text)
			b.WriteString("\n")
		}
		content := b.String()
		if content == "" {
			continue
		}
		content = ragWithDatePrefix(sess.SessionDate, content)
		topics := []string{"session:" + sess.SessionID}
		if strings.TrimSpace(sess.SessionDate) != "" {
			topics = append(topics, "date:"+sess.SessionDate)
		}
		if err := e.memoryService.AddMemory(ctx, userKey, content, topics); err != nil {
			return fmt.Errorf(
				"add dialog for session %s: %w",
				sess.SessionID,
				err,
			)
		}
	}
	return nil
}

func (e *RAGMemoryEvaluator) populateFallback(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	// Fallback mode uses observation, summary, or full dialog content.
	for _, sess := range sample.Conversation {
		content := sess.Observation
		if content == "" {
			content = sess.Summary
		}
		if content == "" {
			var b strings.Builder
			for _, turn := range sess.Turns {
				if turn.Text == "" {
					continue
				}
				b.WriteString(turn.Speaker)
				b.WriteString(": ")
				b.WriteString(turn.Text)
				b.WriteString("\n")
			}
			content = b.String()
		}
		if content == "" {
			continue
		}
		content = ragWithDatePrefix(sess.SessionDate, content)
		topics := []string{"session:" + sess.SessionID, "fallback"}
		if strings.TrimSpace(sess.SessionDate) != "" {
			topics = append(topics, "date:"+sess.SessionDate)
		}
		if err := e.memoryService.AddMemory(ctx, userKey, content, topics); err != nil {
			return fmt.Errorf(
				"add fallback memory for session %s: %w",
				sess.SessionID,
				err,
			)
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
	userKey := memory.UserKey{
		AppName: ragAppName, UserID: sample.SampleID,
	}

	memories, err := e.memoryService.SearchMemories(
		ctx, userKey, qa.Question,
	)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	ragContext := e.buildRAGContext(memories, e.config.TopK)
	prompt := buildRAGQAPrompt(ragContext, qa.Question)

	maxTokens := ragMaxTokens
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

	predicted, err := runModelWithRateLimitRetry(
		ctx, e.model, req,
	)
	if err != nil {
		return nil, fmt.Errorf("model generate: %w", err)
	}

	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(
			ctx, qa.Question, qa.Answer, predicted,
		)
		if err == nil && judgeResult.Correct {
			m.LLMScore = judgeResult.Confidence
		}
	}

	tokensUsed := e.tokenCounter.Count(prompt) +
		e.tokenCounter.Count(predicted)
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
	memories []*memory.Entry, topK int,
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

func buildRAGQAPrompt(ragCtx, question string) string {
	var b strings.Builder
	b.WriteString("Based on the following retrieved memories, ")
	b.WriteString("answer the question.\n\n")
	b.WriteString("## Retrieved Memories\n")
	b.WriteString(ragCtx)
	b.WriteString("\n\n## Question\n")
	b.WriteString(question)
	b.WriteString("\n\n## Instructions\n")
	b.WriteString("- Answer based ONLY on the retrieved memories.\n")
	b.WriteString("- If the answer cannot be found, say \"")
	b.WriteString(fallbackAnswer)
	b.WriteString("\" exactly.\n")
	b.WriteString("- Use date information in the memory text ")
	b.WriteString("(e.g. \"[DATE: ...]\").\n")
	b.WriteString("- Do NOT use memory database timestamps ")
	b.WriteString("(CreatedAt/UpdatedAt) or the current system date.\n")
	b.WriteString("- If the question asks about time and a memory ")
	b.WriteString("uses a relative phrase (like \"last year\"), ")
	b.WriteString("resolve it using explicit dates in the memories.\n")
	b.WriteString("- If contradictions exist, prioritize the memory ")
	b.WriteString("with the latest \"[DATE: ...]\".\n")
	b.WriteString("- Do NOT roleplay. Do NOT ask questions. ")
	b.WriteString("Do NOT add explanations.\n")
	b.WriteString("- The answer should be less than 5-6 words.\n\n")
	b.WriteString("Answer:")
	return b.String()
}
