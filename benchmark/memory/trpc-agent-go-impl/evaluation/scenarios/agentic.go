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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const agenticAppName = "memory-eval-agentic"

const (
	agenticWriteMaxToolIterations = 20
	agenticReadMaxToolIterations  = 8
)

// AgenticEvaluator evaluates using an agent that can explicitly call memory
// tools (add/search/etc.).
type AgenticEvaluator struct {
	model         model.Model
	evalModel     model.Model
	memoryService memory.Service
	config        Config
	llmJudge      *metrics.LLMJudge
}

// NewAgenticEvaluator creates a new agentic evaluator.
func NewAgenticEvaluator(
	m, evalModel model.Model,
	memSvc memory.Service,
	cfg Config,
) *AgenticEvaluator {
	e := &AgenticEvaluator{
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
func (e *AgenticEvaluator) Name() string {
	return "agentic"
}

// Evaluate runs evaluation on a sample using runner -> agent -> model.
func (e *AgenticEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	userKey := memory.UserKey{AppName: agenticAppName, UserID: sample.SampleID}

	_ = e.memoryService.ClearMemories(ctx, userKey)

	memSvc := &noAutoMemoryService{inner: e.memoryService}
	ag := newAgenticAgent(e.model, memSvc.Tools())
	r := runner.NewRunner(
		agenticAppName,
		ag,
		runner.WithSessionService(newSessionService(e.config)),
		runner.WithMemoryService(memSvc),
	)
	defer r.Close()

	if err := e.processConversation(ctx, r, userKey, sample); err != nil {
		return nil, fmt.Errorf("process conversation: %w", err)
	}

	result := &SampleResult{SampleID: sample.SampleID}
	result.QAResults = make([]*QAResult, 0, len(sample.QA))
	catAgg := metrics.NewCategoryAggregator()

	seed := fullConversationMessages(sample)
	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(ctx, r, userKey, seed, qa)
		if err != nil {
			if e.config.Verbose {
				log.Printf("Warning: evaluate QA %s failed: %v", qa.QuestionID, err)
			}
			qaResult = &QAResult{
				QuestionID: qa.QuestionID,
				Question:   qa.Question,
				Category:   qa.Category,
				Expected:   qa.Answer,
				Predicted:  fallbackAnswer,
				Metrics:    metrics.QAMetrics{F1: 0, BLEU: 0},
				LatencyMs:  0,
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

func newAgenticAgent(m model.Model, tools []tool.Tool) agent.Agent {
	genConfig := model.GenerationConfig{Stream: false, MaxTokens: intPtr(1000)}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithMaxToolIterations(agenticWriteMaxToolIterations),
	)
}

func (e *AgenticEvaluator) processConversation(
	ctx context.Context,
	r runner.Runner,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	writeInstruction := "Extract important facts from the conversation and store them using memory_add. " +
		"Store personal facts, events (with dates), preferences, and plans. " +
		"After storing all memories, reply with 'Done.' only."

	for _, sess := range sample.Conversation {
		msgs := sessionMessages(sample, sess)
		sessionID := fmt.Sprintf("seed-%s", sess.SessionID)
		ch, err := runner.RunWithMessages(
			ctx,
			r,
			userKey.UserID,
			sessionID,
			msgs,
			agent.WithInstruction(writeInstruction),
		)
		if err != nil {
			if e.config.Verbose {
				log.Printf("Warning: failed to process session %s: %v", sess.SessionID, err)
			}
			continue
		}
		_, _ = collectFinalText(ch)
	}
	return nil
}

func (e *AgenticEvaluator) evaluateQA(
	ctx context.Context,
	r runner.Runner,
	userKey memory.UserKey,
	seed []model.Message,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()
	readInstruction := "Use memory_search to find relevant facts. " +
		"Answer in English only. Output ONLY the answer. " +
		"If no relevant memory exists, output 'The information is not available.'"

	sessionID := fmt.Sprintf("qa-%s", qa.QuestionID)
	msg := model.NewUserMessage(qa.Question)
	opts := []agent.RunOption{agent.WithInstruction(readInstruction)}
	if e.config.QAWithHistory {
		opts = append(opts, agent.WithMessages(seed))
	}
	predicted, err := runWithRateLimitRetry(ctx, func() (<-chan *event.Event, error) {
		return r.Run(ctx, userKey.UserID, sessionID, msg, opts...)
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
