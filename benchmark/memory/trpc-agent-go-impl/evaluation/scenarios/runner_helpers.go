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
	"encoding/json"
	"fmt"
	"log"
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
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
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

// qaMultiSearchInstruction is a strict instruction for the QA agent to
// call memory_search multiple times before answering.
const qaMultiSearchInstruction = `You are a memory retrieval assistant. Your ONLY job is to search memories and output a short factual answer.

WORKFLOW:
1. You MUST call memory_search exactly %d times before answering.
2. Search #1: Call memory_search with the full question as query.
3. For the remaining searches: rewrite the query to maximize recall.
   - Keep named entities (people, places), numbers, and dates.
   - Remove filler words.
   - Prefer short, keyword-like phrases.
4. Read the returned memories from ALL searches. Prefer facts that include an explicit date prefix like "[DATE: ...]".
5. Output ONLY the answer - no explanations, no context, no questions.

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

// ToolCallTrace records a single tool invocation within a QA step.
type ToolCallTrace struct {
	Name   string `json:"name"`
	Args   string `json:"args,omitempty"`
	Result string `json:"result,omitempty"`
}

// StepTrace records one LLM round-trip (request → response).
type StepTrace struct {
	Step             int             `json:"step"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalTokens      int             `json:"total_tokens"`
	ToolCalls        []ToolCallTrace `json:"tool_calls,omitempty"`
}

// collectResult holds the output of collecting events from a runner.
type collectResult struct {
	text  string
	usage TokenUsage
	steps []StepTrace
}

func collectFinalTextAndUsage(
	eventChan <-chan *event.Event,
) (collectResult, error) {
	var res collectResult
	step := 0
	// pendingCalls tracks tool calls from the latest assistant
	// response that have not yet been matched with results.
	var pendingCalls []ToolCallTrace
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
				// Assistant message with tool calls.
				if len(msg.ToolCalls) > 0 {
					step++
					st := StepTrace{Step: step}
					if ev.Response.Usage != nil {
						st.PromptTokens =
							ev.Response.Usage.PromptTokens
						st.CompletionTokens =
							ev.Response.Usage.CompletionTokens
						st.TotalTokens =
							ev.Response.Usage.TotalTokens
					}
					pendingCalls = make(
						[]ToolCallTrace, 0, len(msg.ToolCalls),
					)
					for _, tc := range msg.ToolCalls {
						pendingCalls = append(pendingCalls,
							ToolCallTrace{
								Name: tc.Function.Name,
								Args: string(tc.Function.Arguments),
							})
					}
					st.ToolCalls = pendingCalls
					res.steps = append(res.steps, st)
				}
				// Tool response event.
				if ev.Response.Object ==
					model.ObjectTypeToolResponse &&
					msg.Role == model.RoleTool {
					content := msg.Content
					// Attach result to the matching pending call.
					matched := false
					for i := range pendingCalls {
						if pendingCalls[i].Result == "" {
							pendingCalls[i].Result = content
							matched = true
							break
						}
					}
					if !matched && len(res.steps) > 0 {
						last := &res.steps[len(res.steps)-1]
						last.ToolCalls = append(last.ToolCalls,
							ToolCallTrace{
								Name:   msg.ToolName,
								Result: content,
							})
					}
				}
				// Final assistant text.
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

// memorySearchResult matches the JSON structure returned by
// memory_search tool for parsing in logs.
type memorySearchResult struct {
	Query   string `json:"query"`
	Results []struct {
		ID       string `json:"id"`
		Memory   string `json:"memory"`
		Score    any    `json:"score"`
		Metadata any    `json:"metadata,omitempty"`
	} `json:"results"`
}

// logQATrace prints detailed per-step tool call traces for a QA.
func logQATrace(
	questionID, question, expected, predicted string,
	m metrics.QAMetrics,
	res collectResult,
	latencyMs int64,
) {
	_ = questionID
	log.Printf("    📋 Question: %s", question)
	log.Printf("    🎯 Expected: %s", expected)
	for _, st := range res.steps {
		log.Printf(
			"    🔹 Step %d | Tokens: %d (in:%d out:%d)",
			st.Step, st.TotalTokens,
			st.PromptTokens, st.CompletionTokens,
		)
		if len(st.ToolCalls) > 0 {
			log.Printf(
				"    🔧 Tool Calls: %d", len(st.ToolCalls),
			)
			for i, tc := range st.ToolCalls {
				log.Printf(
					"      [%d] %s", i+1, tc.Name,
				)
				if tc.Args != "" {
					log.Printf(
						"          Args: %s", tc.Args,
					)
				}
				if tc.Result != "" {
					log.Printf(
						"      ✅ Tool Result [%s]:",
						tc.Name,
					)
					// Special formatting for memory_search.
					if tc.Name == "memory_search" {
						formatMemorySearchResult(tc.Result)
					} else {
						log.Printf("          %s", tc.Result)
					}
				}
			}
		}
	}
	log.Printf(
		"    💬 Predicted: %s", predicted,
	)
	log.Printf(
		"    📊 F1=%.3f BLEU=%.3f LLM=%.3f | %dms",
		m.F1, m.BLEU, m.LLMScore, latencyMs,
	)
}

// formatMemorySearchResult parses and pretty-prints memory_search
// results, showing each recalled memory on its own line.
func formatMemorySearchResult(result string) {
	var msr memorySearchResult
	if err := json.Unmarshal([]byte(result), &msr); err != nil {
		log.Printf("          %s", result)
		return
	}
	if len(msr.Results) == 0 {
		log.Printf("          (no results)")
		return
	}
	for j, r := range msr.Results {
		log.Printf("          [%d] %s", j+1, r.Memory)
	}
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
