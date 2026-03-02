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

	autoQAMaxTokens         = 100
	autoQAMaxToolIterations = 8
)

// autoQAInstruction is a strict instruction for the auto QA agent
// to produce concise answers using memory_search tool.
const autoQAInstruction = `You are a memory retrieval assistant. Your ONLY job is to search memories and output a short factual answer.

WORKFLOW:
1. Call memory_search with the question as query.
2. Read the returned memories. Prefer facts that include an explicit date prefix like "[DATE: ...]".
3. Output ONLY the answer - no explanations, no context, no questions.

RULES:
- Your answer MUST be 1-8 words maximum.
- For time questions, use the absolute date/year that appears in the memory text (e.g. "[DATE: 7 May 2023]" or "2022").
- Do NOT use memory database timestamps (CreatedAt/UpdatedAt) or the current system date.
- If a memory uses a relative phrase (like "last year"), resolve it ONLY using explicit dates found in the memories (e.g. the session date).
- If memories contradict each other, prefer the one with the latest "[DATE: ...]".
- If no relevant memory is found, output "` + fallbackAnswer + `" exactly.
- Do NOT ask follow-up questions. Do NOT say "Could you provide more context".
- Do NOT explain your reasoning. Do NOT add any prefix like "The answer is" or "Based on".
- Output the bare answer only.

EXAMPLES of good answers: "Paris", "2021", "7 May 2023", "Toyota Camry", "` + fallbackAnswer + `"`

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
		ch, err := runner.RunWithMessages(
			ctx, seedRunner, userKey.UserID, sessionID, msgs,
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
	qaAgent := newAutoQAAgent(e.model, qaMemSvc.Tools())
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

	for _, qa := range sample.QA {
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
	m model.Model, tools []tool.Tool,
) agent.Agent {
	genConfig := model.GenerationConfig{
		Stream:      false,
		MaxTokens:   intPtr(autoQAMaxTokens),
		Temperature: float64Ptr(0),
	}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(autoQAInstruction),
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
