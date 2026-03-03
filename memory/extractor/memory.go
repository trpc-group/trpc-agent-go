//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Common metadata field keys.
const (
	metadataKeyModelName      = "model_name"
	metadataKeyModelAvailable = "model_available"
)

// sessionDatePrefix is the prefix used to identify session date system
// messages in the conversation. When a message starts with this prefix,
// the date portion is extracted and used as <session_date> in the
// extraction prompt instead of the current system time.
const sessionDatePrefix = "SessionDate:"

// memoryExtractor implements the MemoryExtractor interface.
type memoryExtractor struct {
	model    model.Model
	prompt   string
	checkers []Checker

	enabledTools map[string]struct{}

	// modelCallbacks configures before/after model callbacks for extraction.
	modelCallbacks *model.Callbacks
}

// Option is a function that configures a MemoryExtractor.
type Option func(*memoryExtractor)

// WithPrompt sets the custom prompt for memory extraction.
// The prompt will be used as the system message when calling the LLM.
func WithPrompt(prompt string) Option {
	return func(e *memoryExtractor) {
		if prompt != "" {
			e.prompt = prompt
		}
	}
}

// WithChecker adds an extraction checker.
// Multiple calls append checkers, combined with AND logic by default.
// When checkers are configured, ShouldExtract returns true only if all
// checkers pass.
func WithChecker(c Checker) Option {
	return func(e *memoryExtractor) {
		if c != nil {
			e.checkers = append(e.checkers, c)
		}
	}
}

// WithModelCallbacks sets model callbacks for memory extraction.
// Only structured callbacks are supported.
func WithModelCallbacks(callbacks *model.Callbacks) Option {
	return func(e *memoryExtractor) {
		e.modelCallbacks = callbacks
	}
}

// WithCheckersAny sets checkers with OR logic.
// Any checker passing will trigger extraction.
// This replaces any previously configured checkers.
func WithCheckersAny(checks ...Checker) Option {
	return func(e *memoryExtractor) {
		if len(checks) > 0 {
			e.checkers = []Checker{ChecksAny(checks...)}
		}
	}
}

// NewExtractor creates a new memory extractor.
func NewExtractor(m model.Model, opts ...Option) MemoryExtractor {
	e := &memoryExtractor{
		model:  m,
		prompt: defaultPrompt,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Extract analyzes the conversation and returns memory operations.
func (e *memoryExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*Operation, error) {
	if e.model == nil {
		return nil, errors.New("no model configured for memory extraction")
	}
	if len(messages) == 0 {
		return nil, nil
	}

	// Build request with tool declarations.
	tools := backgroundTools
	if len(e.enabledTools) > 0 {
		tools = filterTools(backgroundTools, e.enabledTools)
	}
	req := &model.Request{
		Messages: e.buildMessages(messages, existing),
		Tools:    tools,
	}

	// Call model.
	ctx, rspChan, err := e.runBeforeModelCallbacks(ctx, req)
	if err != nil {
		return nil, err
	}
	if rspChan == nil {
		rspChan, err = e.model.GenerateContent(ctx, req)
		if err != nil {
			log.WarnfContext(ctx, "extractor: model call failed: %v", err)
			return nil, fmt.Errorf("model call failed: %w", err)
		}
	}

	// Parse tool calls into operations.
	var ops []*Operation
	for rsp := range rspChan {
		ctx, rsp, err = e.runAfterModelCallbacks(ctx, req, rsp)
		if err != nil {
			return nil, err
		}
		if rsp == nil {
			continue
		}
		if rsp.Error != nil {
			return nil, fmt.Errorf("model error: %s", rsp.Error.Message)
		}
		if len(rsp.Choices) == 0 {
			continue
		}
		for _, call := range rsp.Choices[0].Message.ToolCalls {
			op := e.parseToolCall(ctx, call)
			if op != nil {
				ops = append(ops, op)
			}
		}
	}
	return ops, nil
}

// SetPrompt updates the extractor's prompt dynamically.
func (e *memoryExtractor) SetPrompt(prompt string) {
	if prompt != "" {
		e.prompt = prompt
	}
}

// SetModel updates the extractor's model dynamically.
func (e *memoryExtractor) SetModel(m model.Model) {
	if m != nil {
		e.model = m
	}
}

// SetEnabledTools updates the enabled tool flags for background
// operations. The map is defensively copied to prevent external
// mutation.
func (e *memoryExtractor) SetEnabledTools(
	enabled map[string]struct{},
) {
	e.enabledTools = maps.Clone(enabled)
}

// ShouldExtract checks if extraction should be triggered based on context.
// Returns true if extraction should proceed, false to skip.
// When no checkers are configured, always returns true.
func (e *memoryExtractor) ShouldExtract(ctx *ExtractionContext) bool {
	if len(e.checkers) == 0 {
		return true
	}
	// All checkers must pass (AND logic).
	for _, check := range e.checkers {
		if !check(ctx) {
			return false
		}
	}
	return true
}

// Metadata returns metadata about the extractor configuration.
func (e *memoryExtractor) Metadata() map[string]any {
	var modelName string
	modelAvailable := false
	if e.model != nil {
		modelName = e.model.Info().Name
		modelAvailable = true
	}
	return map[string]any{
		metadataKeyModelName:      modelName,
		metadataKeyModelAvailable: modelAvailable,
	}
}

// buildMessages builds messages for auto memory extraction.
func (e *memoryExtractor) buildMessages(
	messages []model.Message,
	existing []*memory.Entry,
) []model.Message {
	result := make([]model.Message, 0, len(messages)+1)

	// Extract session date from conversation messages if available.
	sessionDate := extractSessionDateFromMessages(messages)

	// Add system prompt with existing memories.
	result = append(result, model.NewSystemMessage(
		e.buildSystemPrompt(existing, sessionDate),
	))

	// Add conversation messages.
	result = append(result, messages...)

	return result
}

// extractSessionDateFromMessages scans messages for a system message
// starting with sessionDatePrefix (e.g. "SessionDate: 7 May 2023").
// Returns the trimmed date string if found, or "" if not present.
func extractSessionDateFromMessages(messages []model.Message) string {
	for _, msg := range messages {
		if msg.Role != model.RoleSystem {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if strings.HasPrefix(content, sessionDatePrefix) {
			return strings.TrimSpace(
				strings.TrimPrefix(content, sessionDatePrefix),
			)
		}
	}
	return ""
}

// buildSystemPrompt builds the system prompt with existing memories
// and available actions based on enabled tools.
// sessionDate overrides the <session_date> tag when non-empty;
// otherwise the current system date is used as fallback.
func (e *memoryExtractor) buildSystemPrompt(
	existing []*memory.Entry,
	sessionDate string,
) string {
	var sb strings.Builder
	sb.WriteString(e.prompt)

	// Inject session date so the LLM can resolve relative time
	// expressions (e.g. "yesterday", "last week") to absolute dates.
	// Prefer an explicit session date from the conversation when
	// available; fall back to the current system date.
	date := sessionDate
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	fmt.Fprintf(&sb, "\n<session_date>%s</session_date>\n", date)

	// Append available actions.
	sb.WriteString("\n<available_actions>\n")
	sb.WriteString(e.availableActionsBlock())
	sb.WriteString("</available_actions>\n")

	// Append existing memories with episodic metadata so the LLM can
	// properly deduplicate and avoid re-creating the same episodes.
	if len(existing) > 0 {
		sb.WriteString("\n<existing_memories>\n")
		for _, entry := range existing {
			if entry.Memory != nil {
				sb.WriteString(formatExistingMemory(entry))
			}
		}
		sb.WriteString("</existing_memories>\n")
	}

	return sb.String()
}

// toolActionDescriptions maps background tool names to their
// one-line descriptions shown in the system prompt.
var toolActionDescriptions = map[string]string{
	memory.AddToolName: "Add a new memory " +
		"(only if genuinely new information).",
	memory.UpdateToolName: "Update an existing memory " +
		"with new or corrected information. " +
		"Prefer updating over adding a near-duplicate.",
	memory.DeleteToolName: "Delete a memory " +
		"when the user explicitly asks to forget something.",
	memory.ClearToolName: "Clear all memories " +
		"only when the user explicitly asks to forget everything.",
}

// toolActionOrder controls the deterministic output order.
var toolActionOrder = []string{
	memory.AddToolName,
	memory.UpdateToolName,
	memory.DeleteToolName,
	memory.ClearToolName,
}

// availableActionsBlock returns the text lines describing which
// memory tools the model is allowed to call.
func (e *memoryExtractor) availableActionsBlock() string {
	var sb strings.Builder
	for _, name := range toolActionOrder {
		// Skip tools that are disabled.
		if e.enabledTools != nil {
			if _, ok := e.enabledTools[name]; !ok {
				continue
			}
		}
		desc, ok := toolActionDescriptions[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&sb, "- %s: %s\n", name, desc)
	}
	if sb.Len() == 0 {
		sb.WriteString("No actions available.\n")
	}
	return sb.String()
}

// parseToolCall parses a tool call and returns a memory operation.
func (e *memoryExtractor) parseToolCall(ctx context.Context, call model.ToolCall) *Operation {
	var args map[string]any
	if err := json.Unmarshal(call.Function.Arguments, &args); err != nil {
		log.WarnfContext(ctx, "extractor: failed to parse tool args: %v", err)
		return nil
	}
	return parseToolCallArgs(call.Function.Name, args)
}

func (e *memoryExtractor) runBeforeModelCallbacks(
	ctx context.Context,
	request *model.Request,
) (context.Context, <-chan *model.Response, error) {
	if e.modelCallbacks == nil {
		return ctx, nil, nil
	}

	result, err := e.modelCallbacks.RunBeforeModel(
		ctx,
		&model.BeforeModelArgs{Request: request},
	)
	if err != nil {
		return ctx, nil, fmt.Errorf("before model callback failed: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result == nil || result.CustomResponse == nil {
		return ctx, nil, nil
	}

	customChan := make(chan *model.Response, 1)
	customChan <- result.CustomResponse
	close(customChan)
	return ctx, customChan, nil
}

func modelErrFromResponse(resp *model.Response) error {
	if resp == nil || resp.Error == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", resp.Error.Type, resp.Error.Message)
}

func (e *memoryExtractor) runAfterModelCallbacks(
	ctx context.Context,
	request *model.Request,
	response *model.Response,
) (context.Context, *model.Response, error) {
	if e.modelCallbacks == nil {
		return ctx, response, nil
	}

	result, err := e.modelCallbacks.RunAfterModel(
		ctx,
		&model.AfterModelArgs{
			Request:  request,
			Response: response,
			Error:    modelErrFromResponse(response),
		},
	)
	if err != nil {
		return ctx, nil, fmt.Errorf("after model callback failed: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		response = result.CustomResponse
	}
	return ctx, response, nil
}

// formatExistingMemory formats a single memory entry for inclusion in the
// system prompt. For episodic memories it appends kind, event_time,
// participants and location so the LLM can properly deduplicate.
func formatExistingMemory(entry *memory.Entry) string {
	m := entry.Memory
	base := fmt.Sprintf("- [%s] %s", entry.ID, m.Memory)
	if m.Kind == "" || m.Kind == memory.MemoryKindFact {
		return base + "\n"
	}
	// Append episodic metadata.
	var meta []string
	meta = append(meta, fmt.Sprintf("kind=%s", m.Kind))
	if m.EventTime != nil {
		meta = append(meta, fmt.Sprintf("event_time=%s", m.EventTime.Format("2006-01-02")))
	}
	if len(m.Participants) > 0 {
		meta = append(meta, fmt.Sprintf("participants=%s", strings.Join(m.Participants, ",")))
	}
	if m.Location != "" {
		meta = append(meta, fmt.Sprintf("location=%s", m.Location))
	}
	return fmt.Sprintf("%s (%s)\n", base, strings.Join(meta, "; "))
}

// defaultPrompt is the default system prompt for memory extraction.
const defaultPrompt = `You are a Memory Manager for an AI Assistant.
Your task is to analyze the conversation and manage user memories.
You must distinguish between two types of memories: facts and episodes.

<instructions>
1. Analyze the conversation to identify TWO types of information:
   a. **Facts** (memory_kind="fact"): Stable personal attributes, preferences,
      or background that are generally true about the user.
      Example: "User is a software engineer at Google."
   b. **Episodes** (memory_kind="episode"): Specific events, experiences, or
      interactions that happened at a particular time and place.
      Example: "On 2024-05-07, User went hiking at Mt. Fuji with Alice."
2. Check if this information is already captured in existing memories.
3. Determine if any memories need to be added, updated, or deleted.
4. You can call multiple tools in parallel to handle all necessary changes at once.
5. Use the available tools to make the necessary changes.
6. If no memory changes are needed, do not call any tools.
</instructions>

<guidelines>
- Create memories as brief, third-person statements that capture key
  information, e.g., "User enjoys hiking on weekends."
- **ATOMICITY**: Keep each memory focused on a SINGLE piece of information.
  Create multiple memories if needed rather than one long complex memory.
  For example, if a user says "I went to Paris with Alice and we ate at
  Le Cinq, then visited the Louvre", create separate memories for the
  dinner at Le Cinq and the Louvre visit.
- **DETAIL**: Include specific names, quantities, dates, and concrete
  details. "User's favorite coffee shop is Blue Bottle on Market St."
  is much better than "User likes coffee."
- **SPECIFICITY**: Always capture specific names, titles, and identifiers.
  "User read 'The Great Gatsby' by F. Scott Fitzgerald" is much better
  than "User enjoys reading books." Include book titles, movie names,
  restaurant names, city names, activity details, and similar specifics.
- **ALL SPEAKERS**: Extract information about ALL people in the
  conversation, not just the primary speaker. If the conversation is
  between two people, capture facts, preferences, events, and actions
  of both speakers. Attribute information to the correct person by name
  when names are available (e.g., "Alice enjoys pottery" rather than
  "User's friend enjoys pottery").
- **RELATIONSHIPS**: When someone is mentioned, always capture who
  they are in relation to the user in a SEPARATE fact memory.
  For example: "Alice is User's college roommate from Stanford."
  This ensures relationship context is retrievable even when later
  questions ask "who is Alice?" or "who did User go to college with?"
- **TOPICS**: Assign specific, descriptive topics that help retrieval.
  Use concrete nouns over abstract ones. Good topics: ["hiking",
  "Mt. Fuji", "travel"]. Bad topics: ["activity", "outdoors"].
  Include NAMES of people, places, and organizations as topics.
- Do not repeat the same information in multiple memories; update
  existing memories instead.
- When updating a memory, append new information to the existing
  memory rather than completely overwriting it.
- When a user's preferences change, update the relevant memory to
  reflect the new state.
- Only use delete when the user explicitly asks to forget something.
- Only use clear when the user explicitly asks to forget everything.
- Write memory content and topics in the same language as the user's
  input message. For example, if the user writes in Chinese, write
  memories and topics in Chinese.
- Do not create memories for:
  - Transient requests or questions (e.g., "What time is it?")
  - Information already captured in existing memories
  - Pure greetings or small talk that contain no factual content
</guidelines>

<episodic_memory_rules>
For EPISODES (memory_kind="episode"):
- Always set memory_kind to "episode".
- Always provide event_time as an absolute date (ISO 8601: YYYY-MM-DD or
  YYYY-MM-DDTHH:MM:SS). NEVER use relative time words like "yesterday",
  "last week", or "two months ago". If a relative time is mentioned,
  resolve it using the <session_date> provided above.
- Capture WHO was involved in the participants field.
- Capture WHERE it happened in the location field.
- Each distinct event should be a separate episode memory.
  Do NOT merge multiple events into one memory.
- Episode memories should describe WHAT happened concretely, not
  abstract summaries. Include details like what was discussed, what
  activity was done, what was the outcome.

For FACTS (memory_kind="fact"):
- Set memory_kind to "fact" (or omit it, as it defaults to "fact").
- Facts can be deduplicated and merged. Prefer updating existing
  facts over creating near-duplicates.
- Create separate fact memories for:
  - Each person's relationship to User (e.g., "Bob is User's manager
    at work")
  - Each distinct preference (e.g., "User prefers Italian food" and
    "User's favorite restaurant is Olive Garden" as two memories)
  - Each skill or qualification
</episodic_memory_rules>

<memory_types>
**Facts** (memory_kind="fact"):
- Personal details: name, age, location, occupation
- Preferences: likes, dislikes, favorites (be specific)
- Interests and hobbies (with details: frequency, favorites)
- Goals and aspirations
- Important relationships (always specify who the person is)
- Opinions and beliefs
- Work and education background
- Skills and qualifications
- Pets, family members, significant others (with names)

**Episodes** (memory_kind="episode"):
- Specific events: trips, meetings, outings, activities
- Life milestones: graduation, job changes, moves
- Shared experiences with specific people
- Emotional moments: arguments, celebrations, surprises
- Health events: doctor visits, illnesses, recoveries
- Conversations about specific topics with specific outcomes
</memory_types>

<examples>
Example 1 – Fact about user:
  User says: "I'm a software engineer at Google."
  → memory_add(memory="User is a software engineer at Google.",
     memory_kind="fact", topics=["work", "Google", "software engineer"])

Example 2 – Episode with details:
  User says: "I went hiking at Mt. Fuji with Alice last Saturday."
  (session_date = 2024-06-10)
  → memory_add(memory="User went hiking at Mt. Fuji with Alice.",
     memory_kind="episode", event_time="2024-06-08",
     participants=["Alice"], location="Mt. Fuji",
     topics=["hiking", "Mt. Fuji", "Alice", "travel"])
  → memory_add(memory="Alice is User's friend who enjoys hiking.",
     memory_kind="fact", topics=["Alice", "friendship", "hiking"])

Example 3 – Multi-fact extraction:
  User says: "My wife Sarah and I just moved to Seattle. She got
  a job at Amazon as a PM."
  → memory_add(memory="User is married to Sarah.",
     memory_kind="fact", topics=["Sarah", "family", "marriage"])
  → memory_add(memory="User recently moved to Seattle.",
     memory_kind="fact", topics=["Seattle", "location", "relocation"])
  → memory_add(memory="Sarah works at Amazon as a Product Manager.",
     memory_kind="fact", topics=["Sarah", "Amazon", "work"])

Example 4 – Episode with conversation detail:
  User says: "Had dinner with Bob yesterday, we talked about his
  startup idea for an AI tutoring app."
  (session_date = 2024-06-10)
  → memory_add(memory="User had dinner with Bob. They discussed Bob's startup idea for an AI tutoring app.",
     memory_kind="episode", event_time="2024-06-09",
     participants=["Bob"], topics=["Bob", "dinner", "startup", "AI tutoring"])
</examples>
`
