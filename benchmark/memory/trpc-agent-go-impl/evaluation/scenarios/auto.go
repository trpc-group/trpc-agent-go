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
	// The extractor runs asynchronously; if we start QA too early, retrieval will miss
	// facts and evaluation will be artificially low.
	if err := e.waitForAutoExtraction(ctx, userKey, sample); err != nil {
		return nil, fmt.Errorf("wait for auto extraction: %w", err)
	}

	if e.config.DebugDumpMemories {
		limit := e.config.DebugMemLimit
		if limit <= 0 {
			limit = 200
		}
		entries, err := e.memoryService.ReadMemories(ctx, userKey, limit)
		if err == nil {
			fmt.Printf("[debug] extracted memories: %d (showing up to %d)\n", len(entries), limit)
			for i, ent := range entries {
				if ent == nil || ent.Memory == nil {
					continue
				}
				fmt.Printf("[debug] memory[%d] id=%s: %s\n", i, ent.ID, ent.Memory.Memory)
			}
		} else {
			fmt.Printf("[debug] failed to read memories: %v\n", err)
		}
	}

	// Phase 2: Answer QA questions using search-only agent.
	result := &SampleResult{
		SampleID:  sample.SampleID,
		QAResults: make([]*QAResult, 0, len(sample.QA)),
	}
	catAgg := metrics.NewCategoryAggregator()

	for i, qa := range sample.QA {
		debugQA := e.config.DebugQALimit > 0 && i < e.config.DebugQALimit
		qaResult, err := e.evaluateQA(ctx, userKey, qa, debugQA)
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

func (e *AutoEvaluator) waitForAutoExtraction(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	// Heuristic: wait until the number of extracted memories becomes stable.
	// We cap total wait time to avoid hanging forever on extractor issues.
	numSessions := len(sample.Conversation)

	// Poll interval and stability criteria.
	const pollInterval = 5 * time.Second
	const stableRounds = 3
	const readLimit = 10000

	// Timeout heuristic: 15s per session, clamped to [30s, 10m].
	timeout := time.Duration(numSessions) * 15 * time.Second
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)

	var (
		lastCount      = -1
		stableCount    = 0
		sawAnyMemories = false
	)

	for {
		if time.Now().After(deadline) {
			// Best-effort: continue evaluation even if we couldn't observe stability.
			return nil
		}

		entries, err := e.memoryService.ReadMemories(ctx, userKey, readLimit)
		if err != nil {
			// Transient DB/read issues shouldn't hard-fail the eval.
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

		// Don't consider extraction "stable" until we've observed at least one memory.
		if sawAnyMemories && stableCount >= stableRounds {
			return nil
		}

		time.Sleep(pollInterval)
	}
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

		// Parse session date to use as event timestamp.
		// This allows the framework to inject correct time context.
		eventTime := parseSessionDate(convSession.SessionDate)
		if eventTime.IsZero() {
			eventTime = time.Now()
		}

		// Convert conversation turns to events with speaker info in content.
		for idx, turn := range convSession.Turns {
			role := model.RoleUser
			if strings.ToLower(turn.Speaker) == "user2" ||
				strings.Contains(strings.ToLower(turn.Speaker), "assistant") {
				role = model.RoleAssistant
			}

			// Include session date and speaker in content.
			// LoCoMo QA expects absolute dates; providing the raw session date here
			// helps the extractor preserve/emit absolute dates rather than relative ones.
			content := fmt.Sprintf("[SessionDate: %s][%s]: %s", convSession.SessionDate, turn.Speaker, turn.Text)

			evt := event.Event{
				ID:        fmt.Sprintf("%s-%d", convSession.SessionID, idx),
				Timestamp: eventTime,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    role,
								Content: content,
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

// parseSessionDate parses session date string like "1:56 pm on 8 May, 2023".
func parseSessionDate(dateStr string) time.Time {
	if dateStr == "" {
		return time.Time{}
	}
	// Try common formats from LoCoMo dataset.
	formats := []string{
		"3:04 pm on 2 January, 2006",
		"3:04 pm on 2 Jan, 2006",
		"3:04 PM on 2 January, 2006",
		"3:04 PM on 2 Jan, 2006",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t
		}
	}
	return time.Time{}
}

// evaluateQA evaluates a single QA using search-only agent.
func (e *AutoEvaluator) evaluateQA(
	ctx context.Context,
	userKey memory.UserKey,
	qa dataset.QAItem,
	debug bool,
) (*QAResult, error) {
	start := time.Now()

	// Search for relevant memories.
	memories, err := e.memoryService.SearchMemories(ctx, userKey, qa.Question)
	if debug {
		fmt.Printf("[debug] QA %s\n", qa.QuestionID)
		fmt.Printf("[debug] Q: %s\n", qa.Question)
		fmt.Printf("[debug] retrieved=%d\n", len(memories))
		for i, mem := range memories {
			if i >= e.config.TopK {
				break
			}
			if mem == nil || mem.Memory == nil {
				continue
			}
			fmt.Printf("[debug] hit[%d] id=%s: %s\n", i, mem.ID, mem.Memory.Memory)
		}
	}
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
	prompt := fmt.Sprintf(`Based on the following memories, answer the question.

## Memories
%s

## Question
%s

## Instructions
- Answer in English only.
- Extract the answer from the memories above.
- Prefer exact strings (dates/names/numbers) as written in the memories.
- Be concise. Output ONLY the answer, no explanations, no extra commentary.
- Only say "The information is not available." if NO memory contains ANY relevant information.

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
	// We store a calibrated correctness score in [0,1]: confidence when judged
	// correct, otherwise 0. This makes category/overall averages meaningful.
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
