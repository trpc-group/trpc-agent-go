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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	ragAppName = "memory-eval-rag"

	ragMaxTokens = 500
)

var ragAnswerInstruction = "Answer the question based ONLY on the retrieved memories. " +
	"If the answer cannot be found in the memories, say \"The information is not available.\". " +
	"Be concise and direct. Output ONLY the answer."

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

	ag := newRAGAgent(e.model)
	r := runner.NewRunner(
		ragAppName,
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
		qaResult, err := e.evaluateQA(ctx, r, sample, qa)
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

func newRAGAgent(m model.Model) agent.Agent {
	genConfig := model.GenerationConfig{Stream: false, MaxTokens: intPtr(ragMaxTokens)}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithInstruction(ragAnswerInstruction),
	)
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
		topics := []string{"session:" + sess.SessionID, "fallback"}
		if err := e.memoryService.AddMemory(ctx, userKey, content, topics); err != nil {
			return fmt.Errorf("add fallback memory for session %s: %w", sess.SessionID, err)
		}
	}
	return nil
}

func (e *RAGMemoryEvaluator) evaluateQA(
	ctx context.Context,
	r runner.Runner,
	sample *dataset.LoCoMoSample,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()
	userKey := memory.UserKey{AppName: ragAppName, UserID: sample.SampleID}

	memories, err := e.memoryService.SearchMemories(ctx, userKey, qa.Question)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}

	ragContext := e.buildRAGContext(memories, e.config.TopK)
	prompt := buildRAGQAPrompt(ragContext, qa.Question)

	sessionID := fmt.Sprintf("qa-%s", qa.QuestionID)
	ch, err := r.Run(
		ctx,
		sample.SampleID,
		sessionID,
		model.NewUserMessage(prompt),
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}

	predicted, err := collectFinalText(ch)
	if err != nil {
		return nil, err
	}

	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(ctx, qa.Question, qa.Answer, predicted)
		if err == nil && judgeResult.Correct {
			m.LLMScore = judgeResult.Confidence
		}
	}

	tokensUsed := e.tokenCounter.Count(prompt) + e.tokenCounter.Count(predicted)
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

func (e *RAGMemoryEvaluator) buildRAGContext(memories []*memory.Entry, topK int) string {
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

func buildRAGQAPrompt(context, question string) string {
	var b strings.Builder
	b.WriteString("Based on the following retrieved memories, answer the question.\n\n")
	b.WriteString("## Retrieved Memories\n")
	b.WriteString(context)
	b.WriteString("\n\n## Question\n")
	b.WriteString(question)
	b.WriteString("\n\n## Instructions\n")
	b.WriteString("- Answer the question based ONLY on the information in the retrieved memories.\n")
	b.WriteString("- If the answer cannot be found in the memories, say \"The information is not available.\".\n")
	b.WriteString("- Be concise and direct in your answer.\n\n")
	b.WriteString("Answer:")
	return b.String()
}
