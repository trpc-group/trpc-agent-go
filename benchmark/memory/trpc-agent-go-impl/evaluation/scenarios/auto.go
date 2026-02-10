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

const (
	autoAppName = "memory-eval-auto"

	autoQAMaxToolIterations = 20
)

var autoFallbackAnswer = "The information is not available."

// AutoEvaluator evaluates using automatic memory extraction.
// Memories are extracted by Runner and stored by the memory service.
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

// Evaluate runs evaluation on a sample using runner-triggered auto extraction.
func (e *AutoEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	userKey := memory.UserKey{AppName: autoAppName, UserID: sample.SampleID}

	_ = e.memoryService.ClearMemories(ctx, userKey)

	// Phase 1: Seed dataset sessions into runner sessions to trigger auto extraction.
	seedRunner := runner.NewRunner(
		autoAppName,
		seedAgent{},
		runner.WithSessionService(newSessionService(e.config)),
		runner.WithMemoryService(e.memoryService),
	)
	defer seedRunner.Close()

	for _, sess := range sample.Conversation {
		sessionID := fmt.Sprintf("seed-%s", sess.SessionID)
		msgs := sessionMessages(sample, sess)
		ch, err := runner.RunWithMessages(ctx, seedRunner, userKey.UserID, sessionID, msgs)
		if err != nil {
			return nil, fmt.Errorf("seed session %s: %w", sess.SessionID, err)
		}
		if _, err := collectFinalText(ch); err != nil {
			return nil, fmt.Errorf("seed session %s: %w", sess.SessionID, err)
		}
	}

	// Wait for async extraction to complete.
	if err := e.waitForAutoExtraction(ctx, userKey, sample); err != nil {
		return nil, fmt.Errorf("wait for auto extraction: %w", err)
	}

	// Phase 2: Answer questions via runner -> agent -> model, using memory tools.
	qaMemSvc := &noAutoMemoryService{inner: e.memoryService}
	qaRunner := runner.NewRunner(
		autoAppName,
		newAutoQAAAgent(e.model, qaMemSvc.Tools()),
		runner.WithSessionService(newSessionService(e.config)),
		runner.WithMemoryService(qaMemSvc),
	)
	defer qaRunner.Close()

	result := &SampleResult{SampleID: sample.SampleID}
	result.QAResults = make([]*QAResult, 0, len(sample.QA))
	catAgg := metrics.NewCategoryAggregator()

	seed := fullConversationMessages(sample)
	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(ctx, qaRunner, userKey, seed, qa)
		if err != nil {
			if e.config.Verbose {
				log.Printf("Warning: evaluate QA %s failed: %v", qa.QuestionID, err)
			}
			qaResult = qaResultFromError(qa, err)
		}
		result.QAResults = append(result.QAResults, qaResult)
		catAgg.Add(qa.Category, qaResult.Metrics)
	}

	result.ByCategory = catAgg.GetCategoryMetrics()
	result.Overall = catAgg.GetOverall()
	result.TotalTimeMs = time.Since(startTime).Milliseconds()
	return result, nil
}

func newAutoQAAAgent(m model.Model, tools []tool.Tool) agent.Agent {
	genConfig := model.GenerationConfig{Stream: false, MaxTokens: intPtr(500)}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(
			"Use memory_search to find relevant facts. "+
				"Call memory_search at most 3 times. "+
				"Answer in English only. Output ONLY the answer.",
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithMaxToolIterations(autoQAMaxToolIterations),
	)
}

func (e *AutoEvaluator) evaluateQA(
	ctx context.Context,
	r runner.Runner,
	userKey memory.UserKey,
	seed []model.Message,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()
	sessionID := fmt.Sprintf("qa-%s", qa.QuestionID)
	msg := model.NewUserMessage(qa.Question)
	opts := make([]agent.RunOption, 0, 1)
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

func qaResultFromError(qa dataset.QAItem, err error) *QAResult {
	_ = err
	m := metrics.QAMetrics{F1: 0, BLEU: 0}
	return &QAResult{
		QuestionID: qa.QuestionID,
		Question:   qa.Question,
		Category:   qa.Category,
		Expected:   qa.Answer,
		Predicted:  autoFallbackAnswer,
		Metrics:    m,
		LatencyMs:  0,
	}
}

func (e *AutoEvaluator) waitForAutoExtraction(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	numSessions := len(sample.Conversation)

	const pollInterval = 5 * time.Second
	const stableRounds = 3
	const readLimit = 10000

	timeout := min(max(time.Duration(numSessions)*15*time.Second, 30*time.Second), 10*time.Minute)
	deadline := time.Now().Add(timeout)

	var (
		lastCount      = -1
		stableCount    = 0
		sawAnyMemories = false
	)

	for {
		if time.Now().After(deadline) {
			return nil
		}

		entries, err := e.memoryService.ReadMemories(ctx, userKey, readLimit)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		cur := len(entries)
		if cur > 0 {
			sawAnyMemories = true
		}
		if cur == lastCount {
			stableCount++
		} else {
			stableCount = 0
			lastCount = cur
		}

		if sawAnyMemories && stableCount >= stableRounds {
			return nil
		}

		time.Sleep(pollInterval)
	}
}
