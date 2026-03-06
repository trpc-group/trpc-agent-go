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
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	autoAppName = "memory-eval-auto"

	autoQAMaxTokens         = 80
	autoQAMaxToolIterations = 10
)

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

// Evaluate runs evaluation on a sample using runner-triggered
// auto extraction.
func (e *AutoEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	userKey := memory.UserKey{
		AppName: autoAppName, UserID: sample.SampleID,
	}

	_ = e.memoryService.ClearMemories(ctx, userKey)

	// Phase 1: Seed sessions to trigger auto extraction.
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
		// Set reference date so the extractor resolves
		// relative time expressions correctly.
		seedCtx := ctx
		if t, ok := parseSessionDate(
			sess.SessionDate,
		); ok {
			seedCtx = extractor.WithReferenceDate(
				seedCtx, t,
			)
		}
		ch, err := runner.RunWithMessages(
			seedCtx, seedRunner,
			userKey.UserID, sessionID, msgs,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"seed session %s: %w", sess.SessionID, err,
			)
		}
		if _, err := collectFinalText(ch); err != nil {
			return nil, fmt.Errorf(
				"seed session %s: %w", sess.SessionID, err,
			)
		}
	}

	// Wait for async extraction to complete.
	if err := e.waitForAutoExtraction(
		ctx, userKey, sample,
	); err != nil {
		return nil, fmt.Errorf(
			"wait for auto extraction: %w", err,
		)
	}

	// Phase 2: Answer questions via agent with memory_search.
	qaMemSvc := &noAutoMemoryService{inner: e.memoryService}
	qaAgent := newAutoQAAgent(
		e.model,
		qaMemSvc.Tools(),
		e.config.QASearchPasses,
	)
	qaRunner := runner.NewRunner(
		autoAppName,
		qaAgent,
		runner.WithSessionService(newSessionService(e.config)),
		runner.WithMemoryService(qaMemSvc),
	)
	defer qaRunner.Close()

	result := &SampleResult{SampleID: sample.SampleID}
	result.QAResults = make([]*QAResult, 0, len(sample.QA))
	catAgg := metrics.NewCategoryAggregator()
	var sampleUsage TokenUsage

	historyMsgs := buildHistoryMessages(
		sample, e.config.QAHistoryTurns,
	)

	for i, qa := range sample.QA {
		qaResult, err := e.evaluateQA(
			ctx, qaRunner, userKey, qa, historyMsgs,
		)
		if err != nil {
			if e.config.Verbose {
				log.Printf(
					"Warning: evaluate QA %s failed: %v",
					qa.QuestionID, err,
				)
			}
			qaResult = qaResultFromError(qa, err)
		}
		if e.config.Verbose {
			log.Printf("  [QA %d/%d] %s (%s)",
				i+1, len(sample.QA),
				qa.QuestionID, qa.Category,
			)
			if qaResult.Steps != nil {
				logQATrace(
					qa.QuestionID, qa.Question,
					qa.Answer, qaResult.Predicted,
					qaResult.Metrics, collectResult{
						steps: qaResult.Steps,
					},
					qaResult.LatencyMs,
				)
			}
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

func newAutoQAAgent(
	m model.Model,
	tools []tool.Tool,
	searchPasses int,
) agent.Agent {
	genConfig := model.GenerationConfig{
		Stream:      false,
		MaxTokens:   intPtr(autoQAMaxTokens),
		Temperature: float64Ptr(0),
	}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(
			qaMemorySearchInstruction(searchPasses),
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
	qa dataset.QAItem,
	historyMsgs []model.Message,
) (*QAResult, error) {
	start := time.Now()
	sessionID := fmt.Sprintf("qa-%s", qa.QuestionID)
	msg := model.NewUserMessage(qa.Question)

	var runOpts []agent.RunOption
	if len(historyMsgs) > 0 {
		runOpts = append(runOpts,
			agent.WithInjectedContextMessages(historyMsgs),
		)
	}

	res, err := runWithRateLimitRetry(
		ctx, func() (<-chan *event.Event, error) {
			return r.Run(
				ctx, userKey.UserID, sessionID, msg,
				runOpts...,
			)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	predicted := res.text

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

	tu := res.usage
	return &QAResult{
		QuestionID: qa.QuestionID,
		Question:   qa.Question,
		Category:   qa.Category,
		Expected:   qa.Answer,
		Predicted:  predicted,
		Metrics:    m,
		LatencyMs:  time.Since(start).Milliseconds(),
		TokenUsage: &tu,
		Steps:      res.steps,
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
		Predicted:  fallbackAnswer,
		Metrics:    m,
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

	timeout := min(
		max(
			time.Duration(numSessions)*15*time.Second,
			30*time.Second,
		),
		10*time.Minute,
	)
	deadline := time.Now().Add(timeout)

	var (
		lastCount           = -1
		lastLatestUpdatedAt time.Time
		stableCount         = 0
		sawAnyMemories      = false
	)

	for {
		if time.Now().After(deadline) {
			return nil
		}

		entries, err := e.memoryService.ReadMemories(
			ctx, userKey, readLimit,
		)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		cur := len(entries)
		var latestUpdatedAt time.Time
		if cur > 0 {
			latestUpdatedAt = entries[0].UpdatedAt
		}
		if cur > 0 {
			sawAnyMemories = true
		}

		if cur == lastCount &&
			latestUpdatedAt.Equal(lastLatestUpdatedAt) {
			stableCount++
		} else {
			stableCount = 0
			lastCount = cur
			lastLatestUpdatedAt = latestUpdatedAt
		}

		if sawAnyMemories && stableCount >= stableRounds {
			return nil
		}

		time.Sleep(pollInterval)
	}
}
