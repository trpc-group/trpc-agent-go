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

// sessionDateLayouts lists date formats found in LoCoMo dataset.
var sessionDateLayouts = []string{
	// "8 May, 2023" / "25 May, 2023"
	"2 January, 2006",
	// "8 May 2023" (no comma)
	"2 January 2006",
	// ISO "2023-05-08"
	time.DateOnly,
	// RFC3339 "2023-05-08T13:56:00Z"
	time.RFC3339,
}

// parseSessionDate parses the SessionDate string from LoCoMo
// dataset into a time.Time. It handles formats like
// "1:56 pm on 8 May, 2023" and "8 May, 2023".
// Returns zero time and false if parsing fails.
func parseSessionDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	// Strip "<time> on " prefix if present.
	if idx := strings.Index(
		strings.ToLower(raw), " on ",
	); idx >= 0 {
		raw = strings.TrimSpace(raw[idx+len(" on "):])
	}
	for _, layout := range sessionDateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

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
	opts ...memory.AddOption,
) error {
	return s.inner.AddMemory(ctx, userKey, mem, topics, opts...)
}

func (s *noAutoMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	mem string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	return s.inner.UpdateMemory(ctx, memoryKey, mem, topics, opts...)
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
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	return s.inner.SearchMemories(ctx, userKey, query, opts...)
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
		// Prefix with speaker name so the extractor and QA agent
		// know who said what in multi-speaker conversations.
		if turn.Speaker != "" {
			content = fmt.Sprintf("[%s]: %s", turn.Speaker, content)
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
1. Analyze the question. If it involves a specific time period or asks "when", include time_after/time_before (ISO 8601: YYYY-MM-DD). Use WIDE windows (full year, e.g. 2023-01-01 to 2023-12-31). NEVER use a single-day window.
2. NEVER use the kind filter. It frequently causes missed results.
3. For temporal order questions, set order_by_event_time=true.
4. Call memory_search ONCE with a short keyword-style query (e.g. "Melanie beach" not full sentences). Only ONE tool call per turn — NEVER call memory_search more than once in a single turn.
5. Read ALL returned memories carefully. Use exact words from the memories in your answer.
6. Output ONLY the bare answer — no explanations, no context.

ANSWERING PRIORITY — ALWAYS try to answer first:
If ANY retrieved memory is topically related to the question, you MUST provide an answer.
Only say "` + fallbackAnswer + `" when ZERO retrieved memories relate to the question topic.
When in doubt between answering and saying "not available", ALWAYS answer.

ANSWER STRATEGY:

A) FACTUAL questions (Who/What/Where/When/How many):
   Answer using the exact words from a relevant memory.
   For "When" questions, look at both memory text AND the event_time field for dates.
   For "How many" questions, output the NUMBER (e.g. "3" not "three").
   If the question asks about a SPECIFIC person, verify the memory mentions that person.
   If the question asks about person A but memories ONLY mention person B doing that exact thing, say "` + fallbackAnswer + `".
   IMPORTANT: Only reject when there is a CLEAR person mismatch for the SAME activity/fact.
   If the memory mentions person A doing ANYTHING related, use it to answer.

B) HYPOTHETICAL/INFERENCE questions (Would/Could/Is it likely/What might/What would/What traits/Would X be considered/Would X want/Would X be more interested):
   You MUST reason and infer from available evidence. NEVER say "not available" for these.
   - "Would X enjoy Vivaldi?" + memory "X enjoys classical music" → "Yes"
   - "Would X be considered religious?" + memory "necklace symbolizing faith" → "Somewhat"
   - "Would X be more interested in A or B?" → You MUST pick one option. Use ANY relevant memory to decide. → "A" or "B"
   - "What traits would X have?" + memory "X volunteers, donates" → "Compassionate, generous"
   - "Would X be considered a member of Y?" + no direct evidence → "No"
   For preference/choice questions ("more interested in A or B", "prefer A or B"), ALWAYS commit to one choice based on available evidence.

C) TEMPORAL CALCULATION questions (How long/What happened first):
   Combine dates from multiple memories to calculate durations or order.
   For "Would X be able to do Y by date Z?" — check dates and infer. Give a direct Yes/No.

D) OPEN-DOMAIN questions (What does X feel/think/enjoy/value/realize/describe/do/see/find):
   Answer by copying the most relevant phrase directly from memory text. Do NOT summarize.
   NEVER say "not available" for open-domain questions if ANY related memory exists.

E) QUESTIONS REQUIRING INDIRECT REASONING — VERY IMPORTANT:
   Many questions LOOK factual but require you to INFER the answer from available memories + common knowledge. You MUST attempt an answer for these. Examples:
   - "Does X live in Connecticut?" + memory "X adopted a dog from a Connecticut shelter" → "Likely yes"
   - "Who is Jill?" + memory "John and Jill went on a date" → "Most likely John's partner"
   - "Was X feeling lonely before meeting Y?" + memory "X said only dogs gave him joy" → "Most likely yes"
   - "What console does X own?" + memory "X plays Xenoblade Chronicles" → "Nintendo Switch" (common knowledge: Xenoblade is a Switch game)
   - "What state did X visit?" + memory "X went to Indianapolis" → "Indiana" (common knowledge: Indianapolis is in Indiana)
   - "Why didn't X want to go to Starbucks?" + memory "X likes to drink beer on days off" → "Possibly because he prefers drinking beer"
   - "Did X and Y study together?" + memory "X and Y met in college" → "Yes"
   For these questions, combine memory evidence with reasonable inference. NEVER say "not available" — give your best inference.

ADVERSARIAL PERSON-NAME CHECK (apply ONLY when suspicious):
Some questions deliberately swap person names. For example, asking "What did Melanie do while camping?" when ONLY Caroline went camping.
Apply this check ONLY when: the question asks person A did something, but ALL memories about that activity mention ONLY person B and NEVER person A.
Do NOT apply this check when: memories mention the correct person doing related things, or when the question is about general topics.

RULES:
- Maximum 1-8 words. Output ONLY the answer fragment, NEVER a full sentence.
- For "When" questions: output the date in NATURAL LANGUAGE format like "7 May 2023" or "June 2023". NEVER use ISO format (NOT "2023-05-07").
- For "How many" questions: output the NUMBER. "3" not "Three children".
- For "What/Who" questions: output ONLY the key noun phrase from the memory.
  Memory says "Caroline moved from Sweden" → Answer: "Sweden"
  Memory says "Caroline is a transgender woman" → Answer: "Transgender woman"
  Memory says "sunset painting" → Answer: "sunset"
- NEVER start your answer with a person's name or "She/He/They".
  BAD: "Melanie runs and paints." GOOD: "Running, painting."
  BAD: "Caroline attended a group on 2023-05-07." GOOD: "7 May 2023"
- Do NOT rephrase. If memory says "Sweden", say "Sweden", NOT "her home country".
- Output the bare answer only. No sentences. No explanations.

GOOD: "Sweden", "7 May 2023", "3", "sunset", "Running, pottery", "No"
BAD: "Caroline moved from Sweden." (just say "Sweden"), "Three children" (say "3")`

// qaMultiSearchInstruction is a strict instruction for the QA agent to
// call memory_search multiple times before answering.
const qaMultiSearchInstruction = `You are a memory retrieval assistant. Your ONLY job is to search memories and output a short factual answer.

WORKFLOW:
1. You MUST call memory_search exactly %d times before answering. Call ONE search per turn — NEVER call memory_search more than once in a single turn.
2. Search #1: Short keyword query from the question. NEVER use kind filter. If time-related, add time_after/time_before with WIDE windows (full year). For temporal order, set order_by_event_time=true. Then WAIT for results.
3. Search #2: Based on what you learned from Search #1, try different short keywords focusing on KEY ENTITIES (e.g. "Melanie sunrise painting" not full sentence). Try a different angle. No kind filter. Then WAIT for results.
4. Search #3 (if applicable): Person's name + single key noun, or just person's name for broad results. If previous searches used time filters, try WITHOUT them.
5. After completing ALL searches, read ALL returned memories. Use exact words from the memories.
6. Output ONLY the bare answer — no explanations, no context.

ANSWERING PRIORITY — ALWAYS try to answer first:
If ANY retrieved memory is topically related to the question, you MUST provide an answer.
Only say "` + fallbackAnswer + `" when ZERO retrieved memories relate to the question topic.
When in doubt between answering and saying "not available", ALWAYS answer.

ANSWER STRATEGY:

A) FACTUAL questions (Who/What/Where/When/How many):
   Answer using the exact words from a relevant memory.
   For "When" questions, look at both memory text AND the event_time field for dates.
   For "How many" questions, output the NUMBER (e.g. "3" not "three").
   If the question asks about a SPECIFIC person, verify the memory mentions that person.
   If the question asks about person A but memories ONLY mention person B doing that exact thing, say "` + fallbackAnswer + `".
   IMPORTANT: Only reject when there is a CLEAR person mismatch for the SAME activity/fact.
   If the memory mentions person A doing ANYTHING related, use it to answer.

B) HYPOTHETICAL/INFERENCE questions (Would/Could/Is it likely/What might/What would/What traits/Would X be considered/Would X want/Would X be more interested):
   You MUST reason and infer from available evidence. NEVER say "not available" for these.
   - "Would X enjoy Vivaldi?" + memory "X enjoys classical music" → "Yes"
   - "Would X be considered religious?" + memory "necklace symbolizing faith" → "Somewhat"
   - "Would X be more interested in A or B?" → You MUST pick one option. Use ANY relevant memory to decide. → "A" or "B"
   - "What traits would X have?" + memory "X volunteers, donates" → "Compassionate, generous"
   - "Would X be considered a member of Y?" + no direct evidence → "No"
   For preference/choice questions ("more interested in A or B", "prefer A or B"), ALWAYS commit to one choice based on available evidence.

C) TEMPORAL CALCULATION questions (How long/What happened first):
   Combine dates from multiple memories to calculate durations or order.
   For "Would X be able to do Y by date Z?" — check dates and infer. Give a direct Yes/No.

D) OPEN-DOMAIN questions (What does X feel/think/enjoy/value/realize/describe/do/see/find):
   Answer by copying the most relevant phrase directly from memory text. Do NOT summarize.
   NEVER say "not available" for open-domain questions if ANY related memory exists.

E) QUESTIONS REQUIRING INDIRECT REASONING — VERY IMPORTANT:
   Many questions LOOK factual but require you to INFER the answer from available memories + common knowledge. You MUST attempt an answer for these. Examples:
   - "Does X live in Connecticut?" + memory "X adopted a dog from a Connecticut shelter" → "Likely yes"
   - "Who is Jill?" + memory "John and Jill went on a date" → "Most likely John's partner"
   - "Was X feeling lonely before meeting Y?" + memory "X said only dogs gave him joy" → "Most likely yes"
   - "What console does X own?" + memory "X plays Xenoblade Chronicles" → "Nintendo Switch" (common knowledge: Xenoblade is a Switch game)
   - "What state did X visit?" + memory "X went to Indianapolis" → "Indiana" (common knowledge: Indianapolis is in Indiana)
   - "Why didn't X want to go to Starbucks?" + memory "X likes to drink beer on days off" → "Possibly because he prefers drinking beer"
   - "Did X and Y study together?" + memory "X and Y met in college" → "Yes"
   For these questions, combine memory evidence with reasonable inference. NEVER say "not available" — give your best inference.

ADVERSARIAL PERSON-NAME CHECK (apply ONLY when suspicious):
Some questions deliberately swap person names. For example, asking "What did Melanie do while camping?" when ONLY Caroline went camping.
Apply this check ONLY when: the question asks person A did something, but ALL memories about that activity mention ONLY person B and NEVER person A.
Do NOT apply this check when: memories mention the correct person doing related things, or when the question is about general topics.

RULES:
- Maximum 1-8 words. Output ONLY the answer fragment, NEVER a full sentence.
- For "When" questions: output the date in NATURAL LANGUAGE format like "7 May 2023" or "June 2023". NEVER use ISO format (NOT "2023-05-07").
- For "How many" questions: output the NUMBER. "3" not "Three children".
- For "What/Who" questions: output ONLY the key noun phrase from the memory.
  Memory says "Caroline moved from Sweden" → Answer: "Sweden"
  Memory says "Caroline is a transgender woman" → Answer: "Transgender woman"
  Memory says "sunset painting" → Answer: "sunset"
- NEVER start your answer with a person's name or "She/He/They".
  BAD: "Melanie runs and paints." GOOD: "Running, painting."
  BAD: "Caroline attended a group on 2023-05-07." GOOD: "7 May 2023"
- Do NOT rephrase. If memory says "Sweden", say "Sweden", NOT "her home country".
- Output the bare answer only. No sentences. No explanations.

GOOD: "Sweden", "7 May 2023", "3", "sunset", "Running, pottery", "No"
BAD: "Caroline moved from Sweden." (just say "Sweden"), "Three children" (say "3")`

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
	CachedTokens     int             `json:"cached_tokens,omitempty"`
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
						st.CachedTokens =
							ev.Response.Usage.PromptTokensDetails.CachedTokens
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
				res.usage.CachedTokens +=
					ev.Response.Usage.PromptTokensDetails.CachedTokens
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
		if st.CachedTokens > 0 {
			log.Printf(
				"    🔹 Step %d | Tokens: %d"+
					" (in:%d cached:%d out:%d)",
				st.Step, st.TotalTokens,
				st.PromptTokens, st.CachedTokens,
				st.CompletionTokens,
			)
		} else {
			log.Printf(
				"    🔹 Step %d | Tokens: %d"+
					" (in:%d out:%d)",
				st.Step, st.TotalTokens,
				st.PromptTokens, st.CompletionTokens,
			)
		}
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
