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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
)

func newSessionService(cfg Config) session.Service {
	return sessioninmemory.NewSessionService(
		sessioninmemory.WithSessionEventLimit(cfg.SessionEventLimit),
	)
}

const (
	seedAgentName        = "memory-eval-seed"
	defaultAgentName     = "memory-eval-agent"
	seedSessionDateLabel = "SessionDate"
)

// noAutoMemoryService wraps a memory service and disables auto extraction.
// This prevents QA interactions from contaminating the memory store.
type noAutoMemoryService struct {
	inner memory.Service
}

func (s *noAutoMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	mem string,
	topics []string,
) error {
	return s.inner.AddMemory(ctx, userKey, mem, topics)
}

func (s *noAutoMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	mem string,
	topics []string,
) error {
	return s.inner.UpdateMemory(ctx, memoryKey, mem, topics)
}

func (s *noAutoMemoryService) DeleteMemory(
	ctx context.Context,
	memoryKey memory.Key,
) error {
	return s.inner.DeleteMemory(ctx, memoryKey)
}

func (s *noAutoMemoryService) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	return s.inner.ClearMemories(ctx, userKey)
}

func (s *noAutoMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	return s.inner.ReadMemories(ctx, userKey, limit)
}

func (s *noAutoMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
) ([]*memory.Entry, error) {
	return s.inner.SearchMemories(ctx, userKey, query)
}

func (s *noAutoMemoryService) Tools() []tool.Tool {
	return s.inner.Tools()
}

func (s *noAutoMemoryService) EnqueueAutoMemoryJob(
	_ context.Context,
	_ *session.Session,
) error {
	return nil
}

func (s *noAutoMemoryService) Close() error {
	return s.inner.Close()
}

func (s *noAutoMemoryService) AddMemoryWithEpisodic(
	ctx context.Context,
	userKey memory.UserKey,
	mem string,
	topics []string,
	ep *memory.EpisodicFields,
) error {
	return s.inner.AddMemoryWithEpisodic(ctx, userKey, mem, topics, ep)
}

func (s *noAutoMemoryService) UpdateMemoryWithEpisodic(
	ctx context.Context,
	memoryKey memory.Key,
	mem string,
	topics []string,
	ep *memory.EpisodicFields,
) error {
	return s.inner.UpdateMemoryWithEpisodic(ctx, memoryKey, mem, topics, ep)
}

func (s *noAutoMemoryService) SearchMemoriesWithOptions(
	ctx context.Context,
	userKey memory.UserKey,
	opts memory.SearchOptions,
) ([]*memory.Entry, error) {
	return s.inner.SearchMemoriesWithOptions(ctx, userKey, opts)
}

// seedAgent is a minimal agent used to trigger Runner's auto memory enqueue.
// It does not call an LLM and produces a deterministic response.
type seedAgent struct{}

func (seedAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		if invocation == nil {
			return
		}
		rsp := &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("OK.")},
			},
		}
		_ = event.EmitEvent(ctx, ch, event.NewResponseEvent(
			invocation.InvocationID,
			seedAgentName,
			rsp,
		))
	}()
	return ch, nil
}

func (seedAgent) Tools() []tool.Tool {
	return nil
}

func (seedAgent) Info() agent.Info {
	return agent.Info{Name: seedAgentName, Description: "Seed agent for benchmarks."}
}

func (seedAgent) SubAgents() []agent.Agent {
	return nil
}

func (seedAgent) FindSubAgent(_ string) agent.Agent {
	return nil
}

func sessionMessages(sample *dataset.LoCoMoSample, sess dataset.Session) []model.Message {
	msgs := make([]model.Message, 0, len(sess.Turns)+1)
	if strings.TrimSpace(sess.SessionDate) != "" {
		msgs = append(msgs, model.NewSystemMessage(
			fmt.Sprintf("%s: %s", seedSessionDateLabel, sess.SessionDate),
		))
	}

	primarySpeaker := ""
	secondarySpeaker := ""
	if sample != nil {
		if len(sample.Speakers) > 0 {
			primarySpeaker = sample.Speakers[0]
		}
		if len(sample.Speakers) > 1 {
			secondarySpeaker = sample.Speakers[1]
		}
	}

	for _, turn := range sess.Turns {
		role := model.RoleUser
		speakerLower := strings.ToLower(turn.Speaker)
		if secondarySpeaker != "" && turn.Speaker == secondarySpeaker {
			role = model.RoleAssistant
		} else if primarySpeaker != "" && turn.Speaker == primarySpeaker {
			role = model.RoleUser
		} else if strings.Contains(speakerLower, "assistant") {
			role = model.RoleAssistant
		} else if speakerLower == "user2" {
			role = model.RoleAssistant
		}

		content := strings.TrimSpace(turn.Text)
		if content == "" {
			continue
		}
		msgs = append(msgs, model.Message{Role: role, Content: content})
	}
	return msgs
}

// buildHistoryMessages constructs the most recent k conversation
// turns (messages) from the sample's full conversation. It walks
// sessions in order and collects all turns, then returns the
// trailing k messages. Returns nil when k <= 0.
func buildHistoryMessages(
	sample *dataset.LoCoMoSample, k int,
) []model.Message {
	if k <= 0 || sample == nil {
		return nil
	}
	// Collect all conversation turns into messages.
	var all []model.Message
	for _, sess := range sample.Conversation {
		msgs := sessionMessages(sample, sess)
		all = append(all, msgs...)
	}
	if len(all) <= k {
		return all
	}
	return all[len(all)-k:]
}

const fallbackAnswer = "The information is not available."

// qaSingleSearchInstruction is a strict instruction for the QA agent
// to produce concise answers using memory_search tool.
const qaSingleSearchInstruction = `You are a memory retrieval assistant. Your ONLY job is to search memories and output a short factual answer.

WORKFLOW:
1. Analyze the question. If it involves a specific time period or asks "when" something happened, include time_after and/or time_before parameters (ISO 8601: YYYY-MM-DD) in your memory_search call.
2. If the question targets a specific type of information, use kind="episode" for events or kind="fact" for personal attributes.
3. If the question asks about temporal order (what happened first, next, before/after something), set order_by_event_time=true to get results sorted chronologically.
4. Call memory_search with the question as query (and optional filters above).
5. Read the returned memories. Prefer facts that include an explicit date prefix like "[DATE: ...]" or have event_time set.
6. Output ONLY the answer - no explanations, no context, no questions.

RULES:
- Your answer MUST be 1-8 words maximum.
- For time questions, use the absolute date/year that appears in the memory text (e.g. "[DATE: 7 May 2023]" or "2022") or from the event_time field.
- Do NOT use memory database timestamps (CreatedAt/UpdatedAt) or the current system date.
- If a memory uses a relative phrase (like "last year"), resolve it ONLY using explicit dates found in the memories (e.g. the session date).
- If memories contradict each other, prefer the one with the latest "[DATE: ...]" or event_time.
- If no relevant memory is found, output "` + fallbackAnswer + `" exactly.
- Do NOT ask follow-up questions. Do NOT say "Could you provide more context".
- Do NOT explain your reasoning. Do NOT add any prefix like "The answer is" or "Based on".
- Output the bare answer only.

EXAMPLES of good answers: "Paris", "2021", "7 May 2023", "Toyota Camry", "` + fallbackAnswer + `"`

// qaMultiSearchInstruction is a strict instruction for the QA agent to
// call memory_search multiple times before answering.
const qaMultiSearchInstruction = `You are a memory retrieval assistant. Your ONLY job is to search memories and output a short factual answer.

WORKFLOW:
1. You MUST call memory_search exactly %d times before answering.
2. Search #1: Call memory_search with the full question as query. If the question involves a specific time period or asks "when", include time_after/time_before (ISO 8601: YYYY-MM-DD). Use kind="episode" for events, kind="fact" for personal attributes. For temporal order questions (what happened first/next/before/after), set order_by_event_time=true.
3. For the remaining searches: rewrite the query to maximize recall.
   - Keep named entities (people, places), numbers, and dates.
   - Remove filler words.
   - Prefer short, keyword-like phrases.
   - Try different time ranges or kind filters if previous searches returned no relevant results.
4. Read the returned memories from ALL searches. Prefer facts that include an explicit date prefix like "[DATE: ...]" or have event_time set.
5. Output ONLY the answer - no explanations, no context, no questions.

RULES:
- Your answer MUST be 1-8 words maximum.
- For time questions, use the absolute date/year that appears in the memory text (e.g. "[DATE: 7 May 2023]" or "2022") or from the event_time field.
- Do NOT use memory database timestamps (CreatedAt/UpdatedAt) or the current system date.
- If a memory uses a relative phrase (like "last year"), resolve it ONLY using explicit dates found in the memories (e.g. the session date).
- If memories contradict each other, prefer the one with the latest "[DATE: ...]" or event_time.
- If no relevant memory is found, output "` + fallbackAnswer + `" exactly.
- Do NOT ask follow-up questions. Do NOT say "Could you provide more context".
- Do NOT explain your reasoning. Do NOT add any prefix like "The answer is" or "Based on".
- Output the bare answer only.

EXAMPLES of good answers: "Paris", "2021", "7 May 2023", "Toyota Camry", "` + fallbackAnswer + `"`

func qaMemorySearchInstruction(searchPasses int) string {
	if searchPasses <= 1 {
		return qaSingleSearchInstruction
	}
	return fmt.Sprintf(qaMultiSearchInstruction, searchPasses)
}

const (
	rateLimitCode              = "\"code\":\"4029\""
	maxRateLimitRetries        = 10
	rateLimitInitialBackoff    = 2 * time.Second
	rateLimitMaxBackoff        = 90 * time.Second
	rateLimitBackoffMultiplier = 2
)

// collectResult holds the output of collecting events from a runner.
type collectResult struct {
	text  string
	usage TokenUsage
}

func collectFinalTextAndUsage(
	eventChan <-chan *event.Event,
) (collectResult, error) {
	var res collectResult
	for ev := range eventChan {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			return res, fmt.Errorf(
				"runner event error: %s", ev.Error.Message,
			)
		}
		if ev.Response != nil {
			if len(ev.Response.Choices) > 0 {
				msg := ev.Response.Choices[0].Message
				if msg.Role == model.RoleAssistant &&
					msg.Content != "" {
					res.text = msg.Content
				}
			}
			if ev.Response.Usage != nil {
				res.usage.PromptTokens +=
					ev.Response.Usage.PromptTokens
				res.usage.CompletionTokens +=
					ev.Response.Usage.CompletionTokens
				res.usage.TotalTokens +=
					ev.Response.Usage.TotalTokens
				res.usage.LLMCalls++
			}
		}
		if ev.IsFinalResponse() || ev.IsRunnerCompletion() {
			break
		}
	}
	res.text = strings.TrimSpace(res.text)
	return res, nil
}

func collectFinalText(eventChan <-chan *event.Event) (string, error) {
	res, err := collectFinalTextAndUsage(eventChan)
	return res.text, err
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "429 Too Many Requests") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "Rate limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "Too Many Requests") ||
		strings.Contains(msg, "rate_limit_exceeded") ||
		strings.Contains(msg, "server_busy") ||
		strings.Contains(msg, rateLimitCode)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func runWithRateLimitRetry(
	ctx context.Context,
	run func() (<-chan *event.Event, error),
) (collectResult, error) {
	backoff := rateLimitInitialBackoff
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		ch, err := run()
		if err != nil {
			if isRateLimitError(err) {
				if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
					return collectResult{}, sleepErr
				}
				backoff = minDuration(backoff*time.Duration(rateLimitBackoffMultiplier), rateLimitMaxBackoff)
				continue
			}
			return collectResult{}, err
		}

		res, err := collectFinalTextAndUsage(ch)
		if err != nil {
			if isRateLimitError(err) {
				if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
					return collectResult{}, sleepErr
				}
				backoff = minDuration(backoff*time.Duration(rateLimitBackoffMultiplier), rateLimitMaxBackoff)
				continue
			}
			return collectResult{}, err
		}
		return res, nil
	}
	return collectResult{}, fmt.Errorf("rate limit retry exceeded")
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
