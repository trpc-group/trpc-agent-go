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
	"trpc.group/trpc-go/trpc-agent-go/prompt"
)

// referenceDate returns the reference date for the extraction.
// It first checks the context for a date set via WithReferenceDate,
// falling back to time.Now().UTC().
func referenceDate(ctx context.Context) time.Time {
	if t, ok := ReferenceDateFromContext(ctx); ok {
		return t
	}
	return time.Now().UTC()
}

// Common metadata field keys.
const (
	metadataKeyModelName      = "model_name"
	metadataKeyModelAvailable = "model_available"
)

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
		Messages: e.buildMessages(ctx, messages, existing),
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
		// Choices are alternative candidates rather than cumulative
		// tool-call batches, so only the selected primary choice
		// should be converted into operations.
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

// extractionUserSuffix is appended as a trailing user message
// when the final message role is neither user nor tool. Some
// model providers (e.g. Anthropic/Claude) reject requests
// whose last message has role=assistant.
const extractionUserSuffix = "Extract and manage memories " +
	"from the conversation above."

// buildMessages builds messages for auto memory extraction.
func (e *memoryExtractor) buildMessages(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) []model.Message {
	result := make([]model.Message, 0, len(messages)+2)

	refDate := referenceDate(ctx)

	// Add system prompt with existing memories.
	result = append(result, model.NewSystemMessage(
		e.buildSystemPrompt(refDate, existing),
	))

	// Add conversation messages.
	result = append(result, messages...)

	// Ensure the sequence ends with a user message. Some
	// providers reject requests that end with an assistant
	// message (treated as unsupported prefill).
	// Skip appending when the trailing assistant message
	// carries tool_calls, because inserting a plain user
	// message between a tool-call request and its results
	// would violate the tool-result ordering constraint.
	if last := result[len(result)-1]; last.Role != model.RoleUser &&
		last.Role != model.RoleTool &&
		len(last.ToolCalls) == 0 {
		result = append(result,
			model.NewUserMessage(extractionUserSuffix))
	}

	return result
}

// currentDatePlaceholder is replaced in the prompt template with
// the actual reference date.
const currentDatePlaceholder = "{current_date}"

// buildSystemPrompt builds the system prompt with existing memories
// and available actions based on enabled tools.
// refDate is substituted into the prompt's {current_date} placeholder
// so the extractor can resolve relative time references.
func (e *memoryExtractor) buildSystemPrompt(
	refDate time.Time,
	existing []*memory.Entry,
) string {
	var sb strings.Builder

	dateStr := refDate.UTC().Format(time.DateOnly)
	renderedPrompt, err := prompt.Text{Template: e.prompt}.Render(prompt.RenderEnv{
		Vars: prompt.Vars{
			"current_date": dateStr,
		},
	})
	if err != nil {
		renderedPrompt = e.prompt
	}
	sb.WriteString(renderedPrompt)

	// Append available actions.
	sb.WriteString("\n<available_actions>\n")
	sb.WriteString(e.availableActionsBlock())
	sb.WriteString("</available_actions>\n")

	// Append existing memories with topics and episodic metadata so the
	// LLM can reuse consistent topic names and avoid re-creating episodes.
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
		"(only if genuinely new information that is not already captured " +
		"by an existing memory, even with different wording).",
	memory.UpdateToolName: "Update an existing memory " +
		"with new or corrected information. " +
		"Prefer updating over adding a near-duplicate.",
	memory.DeleteToolName: "Delete a memory " +
		"when the user asks to forget something, or when it is " +
		"clearly outdated or contradicted by newer information.",
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
	var meta []string
	if len(m.Topics) > 0 {
		meta = append(meta,
			fmt.Sprintf("topics: %s",
				strings.Join(m.Topics, ", ")))
	}
	if m.Kind != "" {
		meta = append(meta,
			fmt.Sprintf("kind=%s", m.Kind))
	}
	if m.EventTime != nil {
		meta = append(meta,
			fmt.Sprintf("event_time=%s",
				m.EventTime.Format("2006-01-02")))
	}
	if len(m.Participants) > 0 {
		meta = append(meta,
			fmt.Sprintf("participants=%s",
				strings.Join(m.Participants, ",")))
	}
	if m.Location != "" {
		meta = append(meta,
			fmt.Sprintf("location=%s", m.Location))
	}
	if len(meta) == 0 {
		return base + "\n"
	}
	return fmt.Sprintf("%s (%s)\n",
		base, strings.Join(meta, "; "))
}

// defaultPrompt is the default system prompt for memory extraction.
const defaultPrompt = `You are a Memory Manager for an AI Assistant.
Your task is to extract and manage memories from the conversation.
You must classify each memory as either a FACT or an EPISODE.
Today's date is {current_date}. You MUST use this date to resolve ALL relative time references into absolute dates before writing any memory.

<instructions>
1. Thoroughly analyze the conversation to extract ALL noteworthy information
   from ALL speakers. Every distinct piece of information deserves its own
   memory. Prefer creating MORE atomic memories over fewer compound ones.
2. Classify each memory:
   a. **Episodes** (memory_kind="episode"): Anything that HAPPENED — events,
      activities, experiences, visits, meetings, conversations with outcomes.
      If it has a time reference (explicit or implicit), it is an episode.
      Example: "On 2024-05-07, User went hiking at Mt. Fuji with Alice."
   b. **Facts** (memory_kind="fact"): Stable attributes, preferences,
      relationships, background, skills, opinions.
      Example: "User is a software engineer at Google."
3. Check existing memories BEFORE deciding to add anything. If an existing
   memory already captures the same fact, preference, relationship, or event,
   do NOT call memory_add again. Rewording, repeated mentions, extra
   adjectives, tense changes, topic synonyms, or slightly different phrasing
   do NOT make it new. If the stored memory is wrong, outdated, or genuinely
   superseded but should still exist as the current memory, use memory_update
   instead. If the memory should be removed entirely, use memory_delete. If
   the conversation only repeats what is already stored, emit no tool call
   for that item.
4. Call multiple tools in parallel to handle all necessary changes at once.
</instructions>

<guidelines>
- **COMPLETENESS**: Extract every distinct fact, event, preference, and
  relationship mentioned in the conversation. When deciding whether
  something is worth remembering, err on the side of keeping it. But when
  deciding whether to add a NEW memory versus reusing an existing one, err
  on the side of NOT adding a duplicate. Go through the conversation turn
  by turn and ensure NOTHING is missed from any turn.
- **ATOMICITY**: Keep each memory focused on a SINGLE piece of information.
  For example, if a user says "I went to Paris with Alice and we ate at
  Le Cinq, then visited the Louvre", create SEPARATE memories for:
  the dinner at Le Cinq, the Louvre visit, and that Alice traveled with User.
- **DEDUPLICATION**: Before every memory_add, compare the candidate against
  the existing memories list. Do not add duplicates caused only by paraphrasing,
  wording changes, tense changes, repeated mentions, topic renaming, or
  different surface forms of the same date. The same stable fact expressed
  differently is still the same memory. The same event on the same date is
  usually the same memory. Matching participants or location alone are only
  supporting signals and do NOT mean two different-day episodes are the same
  memory. When it is a duplicate, emit no tool call. When it corrects or
  replaces an existing memory, use memory_update.
- **NO SUBJECT PREFIX**: Create memories as brief, concise statements that
  directly describe attributes or facts WITHOUT a subject prefix. Omit
  "User", "The user", or any equivalent pronoun/noun at the start, because
  each memory is already bound to a specific user. Examples:
    Good: "Enjoys hiking on weekends"
    Good: "Works as a backend engineer at Tencent"
    Bad:  "User enjoys hiking on weekends"
    Bad:  "The user works at Tencent"
- **ALL SPEAKERS**: Extract information about EVERY person in the
  conversation, not just the primary speaker. If two people are talking,
  capture facts, preferences, events, and actions of BOTH speakers.
  Attribute information to the correct person by name (e.g., "Alice
  enjoys pottery" rather than "User's friend enjoys pottery").
  This is critical — do not ignore any speaker.
  When Speaker B mentions their own experiences, hobbies, purchases, trips,
  books they read, objects they own, or activities they do, these MUST be
  extracted as separate memories attributed to Speaker B by name.
  When BOTH speakers mention doing the same activity together, create memories
  for EACH person's involvement (e.g., "Alice and Bob visited Rome" should
  produce memories for both Alice AND Bob visiting Rome).
- **EXHAUSTIVE DETAILS**: Extract EVERY specific detail mentioned, even if
  it seems minor or is mentioned only once in passing. This includes:
  - Specific book titles, movie titles, song names, band/artist names
  - Names of figurines, collectibles, toys, or special objects
  - Specific accidents, injuries, or health events described
  - Names of specific people met, concerts attended, restaurants visited
  - Specific hobbies started, gyms joined, classes taken
  - Pop culture references (actors, celebrities mentioned)
  - Pet behavior (where a pet hid something, tricks learned)
  - Counts and quantities (number of children, years married, times visited)
  - Specific emotional reactions or quotes
  If someone says "I started going to the gym last month" -- extract BOTH
  the gym-going fact AND the approximate start date as an episode.
- **DETAIL**: Include specific names, quantities, dates, and concrete
  details. "User's favorite coffee shop is Blue Bottle on Market St."
  is much better than "User likes coffee."
- **SPECIFICITY**: Always capture specific names, titles, and identifiers.
  Include book titles, movie names, restaurant names, city names, hobby
  details, pet names, and similar specifics.
- **RELATIONSHIPS**: When someone is mentioned, capture who they are in
  relation to the user in a SEPARATE fact memory.
  For example: "Alice is User's college roommate from Stanford."
- **TOPICS**: Assign specific, descriptive topics. Use concrete nouns over
  abstract ones. Good: ["hiking", "Mt. Fuji", "travel"]. Bad: ["activity"].
  Include NAMES of people, places, and organizations as topics.
  When assigning topics, prefer reusing topic names that already appear
  in existing memories rather than inventing synonyms. For example, if
  existing memories use "work", do not use "job" or "career" for the
  same concept.
- When a fact has genuinely CHANGED (e.g., user got a new job), update
  the existing memory. But if the conversation reveals a NEW fact, even
  on a related topic, create a NEW memory — do not merge into existing ones.
- Use delete when the user explicitly asks to forget something, or when
  a memory should be removed entirely rather than corrected or replaced
  (for example, a mistaken extraction, a withdrawn fact, or stale detail
  the assistant should no longer retain).
- Only use clear when the user explicitly asks to forget everything.
- Write memory content and topics in the same language as the user's input.
- Do not create memories for transient requests ("What time is it?") or
  pure greetings with no factual content.
</guidelines>

<episodic_memory_rules>
WHEN TO USE EPISODE vs FACT:
- If someone DID something, WENT somewhere, ATTENDED an event, MET someone,
  EXPERIENCED something, STARTED an activity, TEAMED UP with someone,
  BOUGHT something, READ a book, or the information has a time anchor → EPISODE.
- If it describes WHO someone IS, what they LIKE, their background, a stable
  attribute with NO time context at all → FACT.
- When unsure, ALWAYS prefer EPISODE with event_time over FACT without it.
  A fact that silently loses its date is worse than an episode that captures it.

IMPORTANT: Even FACTS can and should have event_time when any time context
exists. If a fact mentions WHEN something started, happened, or was the case
(e.g., "started painting in 2022", "teamed up with an artist last month",
"has been married for 5 years"), you MUST set event_time on the memory.

For EPISODES (memory_kind="episode"):
- ALWAYS set memory_kind to "episode".
- ALWAYS provide event_time as an absolute ISO 8601 date (YYYY-MM-DD or
  YYYY-MM-DDTHH:MM:SS). NEVER leave event_time empty for episodes.
- NEVER use relative time words ("yesterday", "last week") in the memory
  text. Resolve them to absolute dates using today's date ({current_date}).
  Example: if today is 2024-06-10 and user says "yesterday",
  that means 2024-06-09. Write "on 2024-06-09" in the memory text.
- When the conversation explicitly mentions a date or year (e.g., "in 2022",
  "on May 7, 2023"), preserve that original date in BOTH the memory text
  and event_time field.
- When someone mentions a duration (e.g., "painting for 7 years"), subtract
  the duration from today's date to derive the start date.
- Capture WHO was involved in the participants field.
- Capture WHERE it happened in the location field.
- Each distinct event should be a SEPARATE episode memory.
- Episode memories should describe WHAT happened concretely, with details.

For FACTS (memory_kind="fact"):
- Set memory_kind to "fact".
- Facts with ANY time reference (e.g., "started painting in 2022",
  "has been married for 5 years", "joined the team recently") MUST
  preserve the time in the memory text AND set event_time.
- Create separate fact memories for:
  - Each person's relationship (e.g., "Bob is User's manager")
  - Each distinct preference
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
Example 1 – Episode with time resolution (ALL speakers):
  Speaker A (Caroline) says: "I went to a poetry reading yesterday."
  Speaker B (Melanie) says: "That sounds great! I went hiking last weekend."
  (today = 2024-06-10)
  → memory_add(memory="Caroline attended a poetry reading on 2024-06-09.",
     memory_kind="episode", event_time="2024-06-09",
     participants=["Caroline"], topics=["poetry", "Caroline", "event"])
  → memory_add(memory="Melanie went hiking around 2024-06-01.",
     memory_kind="episode", event_time="2024-06-01",
     participants=["Melanie"], topics=["hiking", "Melanie"])

Example 2 – Multi-fact extraction from one sentence:
  User says: "My wife Sarah and I just moved to Seattle. She got
  a job at Amazon as a PM."
  → memory_add(memory="User is married to Sarah.",
     memory_kind="fact", topics=["Sarah", "family", "marriage"])
  → memory_add(memory="User recently moved to Seattle.",
     memory_kind="episode", event_time="2024-06-10",
     topics=["Seattle", "relocation"])
  → memory_add(memory="Sarah works at Amazon as a Product Manager.",
     memory_kind="fact", topics=["Sarah", "Amazon", "work"])

Example 3 – Episode with conversation detail:
  User says: "Had dinner with Bob yesterday, we talked about his
  startup idea for an AI tutoring app."
  (today = 2024-06-10)
  → memory_add(memory="User had dinner with Bob on 2024-06-09. They discussed Bob's startup idea for an AI tutoring app.",
     memory_kind="episode", event_time="2024-06-09",
     participants=["Bob"], topics=["Bob", "dinner", "startup", "AI tutoring"])

Example 4 – Duration-based date derivation:
  User says: "I've been painting for about 7 years now."
  (today = 2023-05-08)
  → memory_add(memory="User has been painting since approximately 2016.",
     memory_kind="fact", event_time="2016-01-01",
     topics=["painting", "hobby", "art"])

Example 5 – Extracting specific details from casual conversation:
  Speaker A (Jon): "I just got back from Rome, it was amazing! Also I started
  reading 'The Lean Startup' — really inspiring."
  Speaker B (Gina): "That's great! I bought some figurines at a local market
  last weekend. Oh, and my daughter's birthday is August 13th."
  (today = 2023-06-10)
  → memory_add(memory="Jon recently returned from a trip to Rome.",
     memory_kind="episode", event_time="2023-06-10",
     participants=["Jon"], location="Rome",
     topics=["Jon", "Rome", "travel"])
  → memory_add(memory="Jon started reading the book 'The Lean Startup'.",
     memory_kind="episode", event_time="2023-06-10",
     participants=["Jon"],
     topics=["Jon", "The Lean Startup", "book", "reading"])
  → memory_add(memory="Gina bought figurines at a local market on 2023-06-03.",
     memory_kind="episode", event_time="2023-06-03",
     participants=["Gina"],
     topics=["Gina", "figurines", "market", "shopping"])
  → memory_add(memory="Gina's daughter's birthday is August 13th.",
     memory_kind="fact",
     topics=["Gina", "daughter", "birthday", "August 13"])
</examples>

<common_mistakes>
NEVER write these in memory text -- always resolve relative times to absolute dates:
  BAD: "Melanie painted a lake sunrise last year."
  GOOD: "Melanie painted a lake sunrise in 2022." (if today is 2023-06-10)

  BAD: "They are planning to go camping next month."
  GOOD: "They are planning to go camping in July 2023." (if today is 2023-06-10)

  BAD: "User started going to the gym recently."
  GOOD: "User started going to the gym around June 2023." (if today is 2023-06-10)

  BAD: "Gina teamed up with a local artist a few months ago."
  GOOD: "Gina teamed up with a local artist around February 2023." (if today is 2023-06-10)

Relative phrases to always resolve: "yesterday", "today", "last week",
"last month", "last year", "recently", "a while ago", "a couple months ago",
"a few years ago", "next week", "next month", "this morning", "the other day".
</common_mistakes>

<critical_details>
The following categories are FREQUENTLY MISSED but CRITICALLY IMPORTANT.
You MUST extract these whenever they appear, even if mentioned only once or in passing:

1. SPECIFIC TITLES AND NAMES: Always capture exact book/movie/song titles, celebrity names,
   brand names, restaurant names, and performer names. Each title mentioned deserves its own memory.
   If someone recommends a title to another person, extract both the recommendation AND the reading/watching.
2. COUNTS AND QUANTITIES: Always include exact numbers — children count, years married,
   visit frequency, ages. If a number can be inferred (e.g., "2 younger kids" implies at least 3), extract that too.
3. RELATIONSHIPS AND IDENTITY: Extract relationship status (single, married, dating, divorced),
   how people relate to each other, and identity descriptors using the person's own exact terms.
4. PLACES AND ORIGINS: Use actual country/city names, never paraphrase.
   "I moved from Sweden" → "X moved from Sweden", NOT "X left their home country."
   Extract every trip, visit, or relocation with specific destination and date.
5. PURCHASED OR CREATED ITEMS: Extract with specific item details and descriptions.
   Include materials, styles, editions, or other qualifiers mentioned.
6. EMOTIONS AND OPINIONS: Capture exact feeling words and what one person says about another.
   Extract compliments, criticisms, and descriptions of others' work or character.
7. REASONS AND MOTIVATIONS: Extract why someone made a major decision, started or stopped
   an activity, or changed direction in life. Include the cause and the effect.
8. PHYSICAL DESCRIPTIONS: Include specific visual details of art, objects, scenes, or spaces.
   Capture distinctive features rather than summarizing generically.
</critical_details>

<extraction_checklist>
After extracting memories, do a FINAL CHECK. Re-read each turn and verify:
- Did I extract information from ALL speakers, not just the primary one?
- Did I capture every specific name, title, number, and place mentioned?
- Did I create separate memories for each distinct event, fact, or opinion?
- Did I resolve all relative time references to absolute dates?
- Did I miss any items, activities, or relationships mentioned in passing?
If any information was missed, add it now.
</extraction_checklist>
`
