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
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const agenticAppName = "memory-eval-agentic"

const (
	agenticWriteMaxToolIterations = 50
	agenticReadMaxToolIterations  = 8
	agenticQAMaxTokens            = 100

	agenticUnknownDate      = "unknown"
	agenticDatePrefixFormat = "[DATE: %s] "
)

type datePrefixMemoryService struct {
	inner       memory.Service
	sessionDate string
	mu          sync.RWMutex
}

func newDatePrefixMemoryService(inner memory.Service) *datePrefixMemoryService {
	return &datePrefixMemoryService{inner: inner}
}

func (s *datePrefixMemoryService) SetSessionDate(sessionDate string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionDate = strings.TrimSpace(sessionDate)
}

func (s *datePrefixMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	mem string,
	topics []string,
) error {
	return s.inner.AddMemory(ctx, userKey, s.withDatePrefix(mem), topics)
}

func (s *datePrefixMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	mem string,
	topics []string,
) error {
	return s.inner.UpdateMemory(ctx, memoryKey, s.withDatePrefix(mem), topics)
}

func (s *datePrefixMemoryService) DeleteMemory(
	ctx context.Context,
	memoryKey memory.Key,
) error {
	return s.inner.DeleteMemory(ctx, memoryKey)
}

func (s *datePrefixMemoryService) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	return s.inner.ClearMemories(ctx, userKey)
}

func (s *datePrefixMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	return s.inner.ReadMemories(ctx, userKey, limit)
}

func (s *datePrefixMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
) ([]*memory.Entry, error) {
	return s.inner.SearchMemories(ctx, userKey, query)
}

func (s *datePrefixMemoryService) Tools() []tool.Tool {
	return s.inner.Tools()
}

func (s *datePrefixMemoryService) EnqueueAutoMemoryJob(
	ctx context.Context,
	sess *session.Session,
) error {
	return s.inner.EnqueueAutoMemoryJob(ctx, sess)
}

func (s *datePrefixMemoryService) Close() error {
	return s.inner.Close()
}

func (s *datePrefixMemoryService) withDatePrefix(mem string) string {
	trimmed := strings.TrimSpace(mem)
	if strings.HasPrefix(trimmed, "[DATE:") {
		return mem
	}

	s.mu.RLock()
	date := s.sessionDate
	s.mu.RUnlock()
	if date == "" {
		date = agenticUnknownDate
	}
	return fmt.Sprintf(agenticDatePrefixFormat, date) + mem
}

// agenticQAInstruction is a strict instruction for the QA agent
// to produce concise answers using memory_search tool.
const agenticQAInstruction = `You are a memory retrieval assistant. Your ONLY job is to search memories and output a short factual answer.

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

// AgenticEvaluator evaluates using an agent that can explicitly
// call memory tools (add/search/etc.).
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
// Phase 1 writes memories via agentic tool calling.
// Phase 2 answers questions via agent with memory_search tool.
func (e *AgenticEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	userKey := memory.UserKey{
		AppName: agenticAppName, UserID: sample.SampleID,
	}

	_ = e.memoryService.ClearMemories(ctx, userKey)

	// Phase 1: Write memories via agentic tool calling.
	baseMemSvc := &noAutoMemoryService{inner: e.memoryService}
	writeMemSvc := newDatePrefixMemoryService(baseMemSvc)
	ag := newAgenticAgent(e.model, writeMemSvc.Tools())
	r := runner.NewRunner(
		agenticAppName,
		ag,
		runner.WithSessionService(newSessionService(e.config)),
		runner.WithMemoryService(writeMemSvc),
	)
	defer r.Close()

	if err := e.processConversation(
		ctx, r, writeMemSvc, userKey, sample,
	); err != nil {
		return nil, fmt.Errorf("process conversation: %w", err)
	}

	memSvc := baseMemSvc

	// Phase 2: Answer questions via agent with memory_search.
	qaAgent := newAgenticQAAgent(e.model, memSvc.Tools())
	qaRunner := runner.NewRunner(
		agenticAppName,
		qaAgent,
		runner.WithSessionService(newSessionService(e.config)),
		runner.WithMemoryService(memSvc),
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

func newAgenticAgent(m model.Model, tools []tool.Tool) agent.Agent {
	genConfig := model.GenerationConfig{
		Stream: false, MaxTokens: intPtr(1000),
	}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithMaxToolIterations(
			agenticWriteMaxToolIterations,
		),
	)
}

func newAgenticQAAgent(
	m model.Model, tools []tool.Tool,
) agent.Agent {
	genConfig := model.GenerationConfig{
		Stream:      false,
		MaxTokens:   intPtr(agenticQAMaxTokens),
		Temperature: float64Ptr(0),
	}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(agenticQAInstruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithMaxToolIterations(
			agenticReadMaxToolIterations,
		),
	)
}

func (e *AgenticEvaluator) processConversation(
	ctx context.Context,
	r runner.Runner,
	writeMemSvc *datePrefixMemoryService,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	writeInstruction := "You are a memory extraction assistant. " +
		"Extract ALL distinct facts from the conversation " +
		"and store EACH fact as a separate memory_add call.\n\n" +
		"RULES:\n" +
		"- Store one fact per memory_add call.\n" +
		"- Include facts about ALL speakers, not just the " +
		"primary one.\n" +
		"- Store events with specific details " +
		"(what happened, who, where).\n" +
		"- Store personal traits, preferences, relationships, " +
		"plans, and emotions.\n" +
		"- If the conversation mentions a relative time " +
		"(\"last year\", \"next month\"), resolve it to an " +
		"absolute date/year using the SessionDate.\n" +
		"- After storing all memories, reply 'Done.' only.\n\n" +
		"IMPORTANT: Extract as many facts as possible. " +
		"Aim for 3-8 memories per session."

	for _, sess := range sample.Conversation {
		writeMemSvc.SetSessionDate(sess.SessionDate)
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
				log.Printf(
					"Warning: failed to process session %s: %v",
					sess.SessionID, err,
				)
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
