//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package processor provides content processing logic for agent requests.
// It includes utilities for including, filtering, and rearranging session
// events for LLM requests, as well as helpers for function call/response
// event handling.
package processor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Content inclusion options.
const (
	// BranchFilterModePrefix Prefix matching pattern
	BranchFilterModePrefix = "prefix"
	// BranchFilterModeAll include all
	BranchFilterModeAll = "all"
	// BranchFilterModeExact exact match
	BranchFilterModeExact = "exact"

	// TimelineFilterAll includes all historical message records
	// Suitable for scenarios requiring full conversation context
	TimelineFilterAll = "all"
	// TimelineFilterCurrentRequest only includes messages within the current request cycle
	// Filters out previous historical records, keeping only messages related to this request
	TimelineFilterCurrentRequest = "request"
	// TimelineFilterCurrentInvocation only includes messages within the current invocation session
	// Suitable for scenarios requiring isolation between different invocation cycles in long-running sessions
	TimelineFilterCurrentInvocation = "invocation"
)

// Reasoning content mode constants control how reasoning_content is handled in
// multi-turn conversations. This is particularly important for models like
// DeepSeek that output reasoning_content (thinking chain) alongside the final
// content.
const (
	// ReasoningContentModeKeepAll keeps all reasoning_content in history.
	// Use this for debugging or when you need to retain thinking chains.
	ReasoningContentModeKeepAll = "keep_all"

	// ReasoningContentModeDiscardPreviousTurns discards reasoning_content from
	// messages that belong to previous request turns. Messages within the current
	// request retain their reasoning_content (for tool call scenarios where the
	// model needs to reference its previous reasoning). This is the default mode
	// and recommended for DeepSeek models according to their API documentation.
	// Reference: https://api-docs.deepseek.com/guides/thinking_mode#tool-calls
	ReasoningContentModeDiscardPreviousTurns = "discard_previous_turns"

	// ReasoningContentModeDiscardAll discards all reasoning_content from history.
	// Use this for maximum bandwidth savings when reasoning history is not needed.
	ReasoningContentModeDiscardAll = "discard_all"
)

// ContentRequestProcessor implements content processing logic for agent requests.
type ContentRequestProcessor struct {
	// BranchFilterMode determines how to include content from session events.
	// Options: "prefix", "all", "exact" (default: "prefix").
	BranchFilterMode string
	// AddContextPrefix controls whether to add "For context:" prefix when converting foreign events.
	// When false, foreign agent events are passed directly without the prefix.
	AddContextPrefix bool
	// AddSessionSummary controls whether to prepend the current branch summary
	// as a system message to the request if available.
	AddSessionSummary bool
	// MaxHistoryRuns sets the maximum number of history messages when AddSessionSummary is false.
	// When 0 (default), no limit is applied.
	MaxHistoryRuns int
	// PreserveSameBranch keeps events authored within the same invocation branch in
	// their original roles instead of re-labeling them as user context. This
	// allows graph executions to retain authentic assistant/tool transcripts
	// while still enabling cross-agent contextualization when branches differ.
	PreserveSameBranch bool
	// TimelineFilterMode controls whether to append history messages to the request.
	TimelineFilterMode string
	// ReasoningContentMode controls how reasoning_content is handled in multi-turn
	// conversations. Default is ReasoningContentModeDiscardPreviousTurns, which is
	// recommended for DeepSeek thinking mode.
	ReasoningContentMode string
	// PreloadMemory sets the number of memories to preload into system prompt.
	// When > 0, the specified number of most recent memories are loaded.
	// When 0, no memories are preloaded (use tools instead).
	// When < 0 (default), all memories are loaded.
	PreloadMemory int
	// SummaryFormatter allows custom formatting of session summary content.
	// When nil (default), uses the default formatSummaryContent function.
	SummaryFormatter func(summary string) string
}

// ContentOption is a functional option for configuring the ContentRequestProcessor.
type ContentOption func(*ContentRequestProcessor)

// WithBranchFilterMode sets how to include content from session events.
func WithBranchFilterMode(mode string) ContentOption {
	return func(p *ContentRequestProcessor) {
		if mode != BranchFilterModeAll && mode != BranchFilterModeExact {
			mode = BranchFilterModePrefix
		}
		p.BranchFilterMode = mode
	}
}

// WithTimelineFilterMode sets whether to append history messages to the request.
func WithTimelineFilterMode(mode string) ContentOption {
	return func(p *ContentRequestProcessor) {
		if mode != TimelineFilterCurrentRequest && mode != TimelineFilterCurrentInvocation {
			mode = TimelineFilterAll
		}
		p.TimelineFilterMode = mode
	}
}

// WithAddContextPrefix controls whether to add "For context:" prefix when converting foreign events.
func WithAddContextPrefix(addPrefix bool) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.AddContextPrefix = addPrefix
	}
}

// WithAddSessionSummary controls whether to prepend the current branch summary
// as a system message when available.
func WithAddSessionSummary(add bool) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.AddSessionSummary = add
	}
}

// WithMaxHistoryRuns sets the maximum number of history messages when AddSessionSummary is false.
// When 0 (default), no limit is applied.
func WithMaxHistoryRuns(maxRuns int) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.MaxHistoryRuns = maxRuns
	}
}

// WithPreserveSameBranch toggles preserving original roles for events emitted
// from the same invocation branch. When enabled, messages that originate from
// nodes in the current agent/graph execution keep their assistant/tool roles
// instead of being rewritten as user context.
func WithPreserveSameBranch(preserve bool) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.PreserveSameBranch = preserve
	}
}

// WithReasoningContentMode sets how reasoning_content is handled in multi-turn
// conversations. This is particularly important for DeepSeek models where
// reasoning_content should be discarded from previous request turns.
//
// Available modes:
//   - ReasoningContentModeDiscardPreviousTurns: Discard reasoning_content from
//     previous requests, keep for current request (default, recommended).
//   - ReasoningContentModeKeepAll: Keep all reasoning_content.
//   - ReasoningContentModeDiscardAll: Discard all reasoning_content from history.
func WithReasoningContentMode(mode string) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.ReasoningContentMode = mode
	}
}

// WithPreloadMemory sets the number of memories to preload into system prompt.
//   - Set to 0 (default) to disable preloading (use tools instead).
//   - Set to N (N > 0) to load the most recent N memories.
//   - Set to -1 to load all memories.
//     WARNING: Loading all memories may significantly increase token usage
//     and API costs, especially for users with many stored memories.
//     Consider using a positive limit (e.g., 10-50) for production use.
func WithPreloadMemory(limit int) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.PreloadMemory = limit
	}
}

// WithSummaryFormatter sets a custom formatter for session summary content.
func WithSummaryFormatter(formatter func(summary string) string) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.SummaryFormatter = formatter
	}
}

const (
	mergedUserSeparator = "\n\n"
	contextPrefix       = "For context:"

	contentHasSessionSummaryStateKey = "processor:content:has_session_summary"
)

// NewContentRequestProcessor creates a new content request processor.
func NewContentRequestProcessor(opts ...ContentOption) *ContentRequestProcessor {
	processor := &ContentRequestProcessor{
		BranchFilterMode: BranchFilterModePrefix, // Default only to include
		// filtered contents.
		AddContextPrefix: true, // Default to add context prefix.
		// Default to rewriting same-branch lineage events to user context so
		// that downstream subagents see a single consolidated user message
		// stream unless explicitly opted back into preserving roles.
		PreserveSameBranch: false,
		// Default to append history message.
		TimelineFilterMode: TimelineFilterAll,
		// Default to disable memory preloading (use tools instead).
		PreloadMemory: 0,
	}

	// Apply options.
	for _, opt := range opts {
		opt(processor)
	}

	return processor
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It handles adding messages from the session events to the request.
func (p *ContentRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.ErrorfContext(
			ctx,
			"Content request processor: request is nil",
		)
		return
	}

	if invocation == nil {
		return
	}

	// Honor per-invocation include_contents flag from runtime state when
	// present. This allows callers (including GraphAgent subgraphs) to
	// disable seeding session history for specific runs without changing
	// the processor configuration.
	includeMode := ""
	if invocation.RunOptions.RuntimeState != nil {
		if v, ok := invocation.RunOptions.RuntimeState[graph.CfgKeyIncludeContents]; ok {
			if s, ok2 := v.(string); ok2 {
				includeMode = strings.ToLower(s)
			}
		}
	}
	skipHistory := includeMode == "none"

	p.injectInjectedContextMessages(invocation, req)

	// Append per-filter messages from session events when allowed.
	needToAddInvocationMessage := true
	if invocation.Session != nil {
		var messages []model.Message
		var summaryUpdatedAt time.Time
		var summaryMsg *model.Message
		// Skip session summary when include_contents=none, but still get current
		// invocation's events (tool calls/results) to maintain ReAct loop context.
		if !skipHistory && p.AddSessionSummary && p.TimelineFilterMode == TimelineFilterAll {
			// Fetch session summary early so we can insert it after other
			// semi-stable system blocks (for example, preloaded memories).
			summaryMsg, summaryUpdatedAt =
				p.getSessionSummaryMessage(invocation)
		}

		// Preload memories into system prompt if configured.
		// PreloadMemory: 0 = disabled, -1 = all, N > 0 = most recent N.
		if p.PreloadMemory != 0 && invocation.MemoryService != nil {
			if memMsg := p.getPreloadMemoryMessage(ctx, invocation); memMsg != nil {
				// Insert memory as a system message after the last system
				// message to keep stable instructions cacheable.
				systemMsgIndex := findLastSystemMessageIndex(
					req.Messages,
				)
				if systemMsgIndex >= 0 {
					req.Messages = append(req.Messages[:systemMsgIndex+1],
						append([]model.Message{*memMsg}, req.Messages[systemMsgIndex+1:]...)...)
				} else {
					req.Messages = append([]model.Message{*memMsg}, req.Messages...)
				}
			}
		}

		if summaryMsg != nil {
			invocation.SetState(
				contentHasSessionSummaryStateKey,
				true,
			)
			// Insert summary as a separate system message after the last
			// system message to keep stable instructions cacheable.
			systemMsgIndex := findLastSystemMessageIndex(req.Messages)
			if systemMsgIndex >= 0 {
				req.Messages = append(req.Messages[:systemMsgIndex+1],
					append([]model.Message{*summaryMsg}, req.Messages[systemMsgIndex+1:]...)...)
			} else {
				req.Messages = append([]model.Message{*summaryMsg}, req.Messages...)
			}
		}

		if skipHistory {
			// When include_contents=none, only get events from current invocation
			// to preserve tool call history within the current ReAct loop.
			// This fixes the infinite loop issue where the agent doesn't see its
			// own tool calls when running as an isolated subgraph.
			messages = p.getCurrentInvocationMessages(invocation)
		} else {
			messages = p.getIncrementMessages(invocation, summaryUpdatedAt)
		}
		req.Messages = append(req.Messages, messages...)
		needToAddInvocationMessage = len(messages) == 0
	}

	if model.HasPayload(invocation.Message) && needToAddInvocationMessage {
		req.Messages = append(req.Messages, invocation.Message)
		log.DebugfContext(
			ctx,
			"Content request processor: added invocation message with "+
				"role %s (no session or empty session)",
			invocation.Message.Role,
		)
	}

	// Send a preprocessing event.
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingContent),
	))
}

// injectInjectedContextMessages inserts per-run context messages into the request
// before session-derived history is appended.
func (p *ContentRequestProcessor) injectInjectedContextMessages(invocation *agent.Invocation, req *model.Request) {
	if invocation == nil || req == nil {
		return
	}
	messages := invocation.RunOptions.InjectedContextMessages
	if len(messages) == 0 {
		return
	}
	req.Messages = append(req.Messages, messages...)
}

// getSessionSummaryMessage returns the current-branch session summary as a
// system message if available and non-empty, along with its UpdatedAt timestamp.
func (p *ContentRequestProcessor) getSessionSummaryMessage(inv *agent.Invocation) (*model.Message, time.Time) {
	if inv.Session == nil {
		return nil, time.Time{}
	}

	// Acquire read lock to protect Summaries access.
	inv.Session.SummariesMu.RLock()
	defer inv.Session.SummariesMu.RUnlock()

	if inv.Session.Summaries == nil {
		return nil, time.Time{}
	}
	filter := inv.GetEventFilterKey()
	// For BranchFilterModeAll, prefer the full-session summary under empty filter key.
	if p.BranchFilterMode == BranchFilterModeAll {
		filter = ""
	}

	// Try exact match first.
	sum := inv.Session.Summaries[filter]
	if sum != nil && sum.Summary != "" {
		content := p.formatSummary(sum.Summary)
		return &model.Message{Role: model.RoleSystem, Content: content}, sum.UpdatedAt
	}

	// For BranchFilterModePrefix, aggregate summaries with matching prefix.
	// This handles the case where events have custom filterKeys (e.g., "app/user-messages")
	// but the invocation's eventFilterKey is the app prefix (e.g., "app").
	if p.BranchFilterMode == BranchFilterModePrefix && filter != "" {
		summaryText, updatedAt := p.aggregatePrefixSummaries(inv.Session.Summaries, filter)
		if summaryText != "" {
			content := p.formatSummary(summaryText)
			return &model.Message{Role: model.RoleSystem, Content: content}, updatedAt
		}
	}
	return nil, time.Time{}
}

// aggregatePrefixSummaries aggregates all summaries whose keys have the given prefix.
func (p *ContentRequestProcessor) aggregatePrefixSummaries(
	summaries map[string]*session.Summary,
	prefix string,
) (string, time.Time) {
	var parts []string
	var latestTime time.Time
	filterPrefix := prefix + agent.EventFilterKeyDelimiter

	keys := make([]string, 0, len(summaries))
	for key := range summaries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		sum := summaries[key]
		if sum == nil || sum.Summary == "" {
			continue
		}
		// Check if key matches prefix (key starts with "prefix/" or key equals prefix).
		keyWithDelim := key + agent.EventFilterKeyDelimiter
		if key == prefix || strings.HasPrefix(keyWithDelim, filterPrefix) {
			parts = append(parts, sum.Summary)
			if sum.UpdatedAt.After(latestTime) {
				latestTime = sum.UpdatedAt
			}
		}
	}
	if len(parts) == 0 {
		return "", time.Time{}
	}
	return strings.Join(parts, "\n\n"), latestTime
}

// formatSummary applies custom formatter if available, otherwise uses default.
func (p *ContentRequestProcessor) formatSummary(summary string) string {
	if p.SummaryFormatter != nil {
		return p.SummaryFormatter(summary)
	}
	// Default format.
	return fmt.Sprintf("Here is a brief summary of your previous interactions:\n\n"+
		"<summary_of_previous_interactions>\n%s\n</summary_of_previous_interactions>\n\n"+
		"Note: this information is from previous interactions and may be outdated. "+
		"You should ALWAYS prefer information from this conversation over the past summary.\n", summary)
}

// getHistoryMessages gets history messages for the current filter, potentially truncated by MaxHistoryRuns.
// This method is used when AddSessionSummary is false to get recent history messages.
func (p *ContentRequestProcessor) getIncrementMessages(inv *agent.Invocation, since time.Time) []model.Message {
	if inv.Session == nil {
		return nil
	}
	isZeroTime := since.IsZero()
	filter := inv.GetEventFilterKey()
	var includedInvocationMessage bool

	var events []event.Event
	inv.Session.EventMu.RLock()
	for _, evt := range inv.Session.Events {
		shouldInclude, isInvocationMessage := p.shouldIncludeEvent(evt, inv, filter, isZeroTime, since)
		if !shouldInclude {
			continue
		}
		if isInvocationMessage {
			includedInvocationMessage = true
		}
		// use error fill message content if message content is empty
		if len(evt.Response.Choices) > 0 && evt.Response.Choices[0].Message.Content == "" && evt.Response.Error != nil {
			rsp := evt.Response.Clone()
			rsp.Choices[0].Message.Content = fmt.Sprintf("type: %s, message: %s", rsp.Error.Type, rsp.Error.Message)
			evt.Response = rsp
		}
		events = append(events, evt)
	}
	inv.Session.EventMu.RUnlock()

	// insert invocation message
	if !includedInvocationMessage && model.HasPayload(inv.Message) {
		events = p.insertInvocationMessage(events, inv)
	}

	resultEvents := p.rearrangeLatestFuncResp(events)
	resultEvents = p.rearrangeAsyncFuncRespHist(resultEvents)

	// Get current request ID for reasoning content filtering.
	currentRequestID := inv.RunOptions.RequestID

	// Convert events to messages with reasoning content handling.
	var messages []model.Message
	for _, evt := range resultEvents {
		// Convert foreign events or keep as-is.
		ev := evt
		if p.isOtherAgentReply(inv.AgentName, inv.Branch, &ev) {
			ev = p.convertForeignEvent(&ev)
		}
		if len(ev.Choices) > 0 {
			for _, choice := range ev.Choices {
				msg := choice.Message
				// Apply reasoning content stripping based on mode.
				msg = p.processReasoningContent(msg, evt.RequestID, currentRequestID)
				if isEmptyAssistantMessage(msg) {
					continue
				}
				messages = append(messages, msg)
			}
		}
	}

	messages = p.mergeUserMessages(messages)

	// Apply MaxHistoryRuns limit when AddSessionSummary is false.
	if !p.AddSessionSummary && p.MaxHistoryRuns > 0 {
		messages = applyMaxHistoryRuns(messages, p.MaxHistoryRuns)
	}
	return messages
}

// applyMaxHistoryRuns trims messages to at most maxRuns entries from the tail.
// If the trim boundary falls on a tool-result message whose corresponding
// tool_use was truncated, the boundary is advanced past any such orphaned
// results to prevent API 400 "unexpected tool_use_id" errors.
func applyMaxHistoryRuns(messages []model.Message, maxRuns int) []model.Message {
	if len(messages) <= maxRuns {
		return messages
	}
	startIdx := len(messages) - maxRuns

	// Only scan the truncated prefix when the boundary actually falls on a
	// tool-result message; otherwise there's nothing to skip.
	if messages[startIdx].Role != model.RoleTool || messages[startIdx].ToolID == "" {
		return messages[startIdx:]
	}

	// Collect tool-call IDs that will be truncated (before startIdx).
	truncatedToolIDs := make(map[string]struct{})
	for i := 0; i < startIdx; i++ {
		for _, tc := range messages[i].ToolCalls {
			if tc.ID != "" {
				truncatedToolIDs[tc.ID] = struct{}{}
			}
		}
	}

	// Skip orphaned tool results whose corresponding call was truncated.
	for startIdx < len(messages) &&
		messages[startIdx].Role == model.RoleTool &&
		messages[startIdx].ToolID != "" {
		if _, orphaned := truncatedToolIDs[messages[startIdx].ToolID]; orphaned {
			startIdx++
			continue
		}
		break
	}

	return messages[startIdx:]
}

// processReasoningContent applies reasoning content stripping based on the
// configured mode and request boundaries.
func (p *ContentRequestProcessor) processReasoningContent(
	msg model.Message,
	messageRequestID string,
	currentRequestID string,
) model.Message {
	// Only process assistant messages with reasoning content.
	if msg.Role != model.RoleAssistant || msg.ReasoningContent == "" {
		return msg
	}

	switch p.ReasoningContentMode {
	case ReasoningContentModeDiscardAll:
		// Discard all reasoning_content.
		msg.ReasoningContent = ""
	case ReasoningContentModeKeepAll:
		// Keep all reasoning_content: do nothing.
	default:
		// ReasoningContentModeDiscardPreviousTurns or empty (default):
		// Discard reasoning_content from previous requests only.
		// Current request messages retain their reasoning_content.
		if messageRequestID != currentRequestID {
			msg.ReasoningContent = ""
		}
	}
	return msg
}

func isEmptyAssistantMessage(msg model.Message) bool {
	if msg.Role != model.RoleAssistant {
		return false
	}
	return msg.Content == "" &&
		len(msg.ContentParts) == 0 &&
		len(msg.ToolCalls) == 0 &&
		msg.ReasoningContent == ""
}

// getCurrentInvocationMessages gets messages only from the current invocation.
// This is used when include_contents=none to preserve tool call history within
// the current ReAct loop while isolating from parent/other branch history.
func (p *ContentRequestProcessor) getCurrentInvocationMessages(inv *agent.Invocation) []model.Message {
	if inv.Session == nil {
		return nil
	}

	var events []event.Event
	inv.Session.EventMu.RLock()
	for _, evt := range inv.Session.Events {
		// Only include events from current invocation
		if evt.InvocationID != inv.InvocationID {
			continue
		}
		// Skip invalid events
		if evt.Response == nil || evt.IsPartial || !evt.IsValidContent() {
			continue
		}
		// use error fill message content if message content is empty
		if len(evt.Response.Choices) > 0 && evt.Response.Choices[0].Message.Content == "" && evt.Response.Error != nil {
			rsp := evt.Response.Clone()
			rsp.Choices[0].Message.Content = fmt.Sprintf("type: %s, message: %s", rsp.Error.Type, rsp.Error.Message)
			evt.Response = rsp
		}
		events = append(events, evt)
	}
	inv.Session.EventMu.RUnlock()

	// insert invocation message if not already included
	var hasInvocationMessage bool
	for _, evt := range events {
		if invocationMessageEqual(inv.Message, evt.Choices[0].Message) {
			hasInvocationMessage = true
			break
		}
	}
	if !hasInvocationMessage && model.HasPayload(inv.Message) {
		events = p.insertInvocationMessage(events, inv)
	}

	resultEvents := p.rearrangeLatestFuncResp(events)
	resultEvents = p.rearrangeAsyncFuncRespHist(resultEvents)

	// Get current request ID for reasoning content filtering.
	currentRequestID := inv.RunOptions.RequestID

	// Convert events to messages with reasoning content handling.
	var messages []model.Message
	for _, evt := range resultEvents {
		// Convert foreign events or keep as-is (consistent with getIncrementMessages).
		ev := evt
		if p.isOtherAgentReply(inv.AgentName, inv.Branch, &ev) {
			ev = p.convertForeignEvent(&ev)
		}
		if len(ev.Choices) > 0 {
			for _, choice := range ev.Choices {
				msg := choice.Message
				msg = p.processReasoningContent(msg, evt.RequestID, currentRequestID)
				if isEmptyAssistantMessage(msg) {
					continue
				}
				messages = append(messages, msg)
			}
		}
	}

	messages = p.mergeUserMessages(messages)
	return messages
}

func (p *ContentRequestProcessor) insertInvocationMessage(
	events []event.Event, inv *agent.Invocation) []event.Event {
	if !model.HasPayload(inv.Message) {
		return events
	}
	userMsgEvent := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
		Choices: []model.Choice{
			{Message: inv.Message},
		},
	})
	userMsgEvent.RequestID = inv.RunOptions.RequestID
	if len(events) == 0 {
		return []event.Event{*userMsgEvent}
	}
	insertIndex := -1
	for index, evt := range events {
		if evt.RequestID == inv.RunOptions.RequestID && evt.InvocationID == inv.InvocationID {
			insertIndex = index
			break
		}
	}
	if insertIndex == -1 {
		return append(events, *userMsgEvent)
	}
	events = append(events, event.Event{})
	copy(events[insertIndex+1:], events[insertIndex:])
	events[insertIndex] = *userMsgEvent
	return events
}

func (p *ContentRequestProcessor) mergeUserMessages(
	messages []model.Message,
) []model.Message {
	if len(messages) <= 1 {
		return messages
	}
	if !p.AddContextPrefix {
		return messages
	}
	var merged []model.Message
	var current *model.Message
	appendCurrent := func() {
		if current == nil {
			return
		}
		merged = append(merged, *current)
		current = nil
	}
	for i := range messages {
		msg := messages[i]
		if msg.Role != model.RoleUser ||
			!strings.HasPrefix(msg.Content, contextPrefix) {
			appendCurrent()
			merged = append(merged, msg)
			continue
		}
		if current == nil {
			cloned := msg
			current = &cloned
			continue
		}
		if msg.Content != "" {
			if current.Content == "" {
				current.Content = msg.Content
			} else {
				current.Content = current.Content + mergedUserSeparator +
					msg.Content
			}
		}
		if len(msg.ContentParts) > 0 {
			current.ContentParts = append(
				current.ContentParts,
				msg.ContentParts...,
			)
		}
	}
	appendCurrent()
	if len(merged) == 0 {
		return messages
	}
	return merged
}

func (p *ContentRequestProcessor) shouldIncludeEvent(evt event.Event, inv *agent.Invocation, filter string,
	isZeroTime bool, since time.Time) (bool, bool) {
	if evt.Response == nil || evt.IsPartial || !evt.IsValidContent() {
		return false, false
	}

	// check is invocation message
	if inv.RunOptions.RequestID == evt.RequestID &&
		len(evt.Choices) > 0 &&
		invocationMessageEqual(inv.Message, evt.Choices[0].Message) {
		return true, true
	}

	// Use strict After so events stamped exactly at summary UpdatedAt are
	// treated as already summarized and not re-sent.
	if !isZeroTime && !evt.Timestamp.After(since) {
		return false, false
	}

	// Check timeline filter
	switch p.TimelineFilterMode {
	case TimelineFilterCurrentRequest:
		if inv.RunOptions.RequestID != evt.RequestID {
			return false, false
		}
	case TimelineFilterCurrentInvocation:
		if evt.InvocationID != inv.InvocationID {
			return false, false
		}
	default:
	}

	// Check branch filter
	switch p.BranchFilterMode {
	case BranchFilterModeExact:
		if evt.FilterKey != filter {
			return false, false
		}
	case BranchFilterModePrefix:
		if !evt.Filter(filter) {
			return false, false
		}
	default:
	}

	return true, false
}

func invocationMessageEqual(invMsg model.Message, evtMsg model.Message) bool {
	if invMsg.Role == "" {
		if evtMsg.Role != model.RoleUser {
			return false
		}
		return invMsg.Content == evtMsg.Content
	}
	return model.MessagesEqual(invMsg, evtMsg)
}

// isOtherAgentReply checks whether the event is a reply from another agent.
func (p *ContentRequestProcessor) isOtherAgentReply(
	currentAgentName string,
	currentBranch string,
	evt *event.Event,
) bool {
	if evt == nil || currentAgentName == "" {
		return false
	}
	if evt.Author == "" || evt.Author == "user" || evt.Author == currentAgentName {
		return false
	}
	if p.PreserveSameBranch && currentBranch != "" && evt.Branch != "" {
		// Treat events within the same branch lineage as non-foreign to
		// preserve original roles. This includes both descendants and
		// ancestors of the current branch.
		if evt.Branch == currentBranch ||
			strings.HasPrefix(evt.Branch, currentBranch+agent.BranchDelimiter) ||
			strings.HasPrefix(currentBranch, evt.Branch+agent.BranchDelimiter) {
			return false
		}
	}
	return true
}

// convertForeignEvent converts an event authored by another agent as a user-content event.
func (p *ContentRequestProcessor) convertForeignEvent(evt *event.Event) event.Event {
	if len(evt.Choices) == 0 {
		return *evt
	}
	// Create a new event with user context.
	convertedEvent := evt.Clone()
	convertedEvent.Author = "user"

	// Build content parts for context.
	var contents []string
	var contentParts []model.ContentPart
	if p.AddContextPrefix {
		prefix := contextPrefix
		contents = append(contents, contextPrefix)
		contentParts = append(contentParts, model.ContentPart{
			Type: model.ContentTypeText,
			Text: &prefix,
		})
	}

	for _, choice := range evt.Choices {
		if len(choice.Message.ContentParts) > 0 {
			if p.AddContextPrefix {
				prefix := fmt.Sprintf("[%s] said:", evt.Author)
				contentParts = append(contentParts, model.ContentPart{
					Type: model.ContentTypeText,
					Text: &prefix,
				})
			}
			contentParts = append(contentParts, choice.Message.ContentParts...)
		}

		if choice.Message.Content != "" {
			if p.AddContextPrefix {
				contents = append(contents, fmt.Sprintf("[%s] said: %s", evt.Author, choice.Message.Content))
			} else {
				// When prefix is disabled, pass the content directly.
				contents = append(contents, choice.Message.Content)
			}
		} else if len(choice.Message.ToolCalls) > 0 {
			for _, toolCall := range choice.Message.ToolCalls {
				if p.AddContextPrefix {
					contents = append(contents,
						fmt.Sprintf("[%s] called tool `%s` with parameters: %s",
							evt.Author, toolCall.Function.Name, string(toolCall.Function.Arguments)))
				} else {
					// When prefix is disabled, pass tool call info directly.
					contents = append(contents,
						fmt.Sprintf("Tool `%s` called with parameters: %s",
							toolCall.Function.Name, string(toolCall.Function.Arguments)))
				}
			}
		} else if choice.Message.ToolID != "" {
			if p.AddContextPrefix {
				contents = append(contents,
					fmt.Sprintf("[%s] `%s` tool returned result: %s",
						evt.Author, choice.Message.ToolID, choice.Message.Content))
			} else {
				// When prefix is disabled, pass tool result directly.
				contents = append(contents, choice.Message.Content)
			}
		}
	}

	// Set the converted message.
	if len(contents) > 0 || len(contentParts) > 0 {
		msg := model.Message{
			Role: model.RoleUser,
		}
		if len(contents) > 0 {
			msg.Content = strings.Join(contents, " ")
		}
		if len(contentParts) > 0 {
			msg.ContentParts = contentParts
		}
		convertedEvent.Response.Choices = []model.Choice{
			{
				Index:   0,
				Message: msg,
			},
		}
	}
	return *convertedEvent
}

// rearrangeEventsForLatestFunctionResponse rearranges the events for the latest function_response.
func (p *ContentRequestProcessor) rearrangeLatestFuncResp(
	events []event.Event,
) []event.Event {
	if len(events) == 0 {
		return events
	}

	// Check if latest event is a function response.
	lastEvent := events[len(events)-1]
	if !lastEvent.IsToolResultResponse() {
		return events
	}

	functionResponseIDs := lastEvent.GetToolResultIDs()
	if len(functionResponseIDs) == 0 {
		return events
	}

	// Look for corresponding function call event.
	functionCallEventIdx := -1
	for i := len(events) - 2; i >= 0; i-- {
		evt := &events[i]
		if evt.IsToolCallResponse() {
			functionCallIDs := toMap(evt.GetToolCallIDs())
			for _, responseID := range functionResponseIDs {
				if functionCallIDs[responseID] {
					functionCallEventIdx = i
					break
				}
			}
			if functionCallEventIdx != -1 {
				break
			}
		}
	}

	if functionCallEventIdx == -1 {
		return events
	}

	// Collect function response events between call and latest response.
	var functionResponseEvents []event.Event
	for i := functionCallEventIdx + 1; i < len(events); i++ {
		evt := &events[i]
		if evt.IsToolResultResponse() {
			responseIDs := toMap(evt.GetToolResultIDs())
			for _, responseID := range functionResponseIDs {
				if responseIDs[responseID] {
					functionResponseEvents = append(functionResponseEvents, *evt)
					break
				}
			}
		}
	}

	// Build result with rearranged events.
	resultEvents := make([]event.Event, functionCallEventIdx+1)
	copy(resultEvents, events[:functionCallEventIdx+1])

	if len(functionResponseEvents) > 0 {
		mergedEvent := p.mergeFunctionResponseEvents(functionResponseEvents)
		resultEvents = append(resultEvents, mergedEvent)
	}

	return resultEvents
}

// rearrangeEventsForAsyncFunctionResponsesInHistory rearranges the async function_response events in the history.
func (p *ContentRequestProcessor) rearrangeAsyncFuncRespHist(
	events []event.Event,
) []event.Event {
	functionCallIDToResponseEventIndex := make(map[string]int)

	// Map function response IDs to event indices.
	for i, evt := range events {
		// Create a local copy to avoid implicit memory aliasing.
		// This bug is fixed in go 1.22.
		// See: https://tip.golang.org/doc/go1.22#language
		evt := evt

		if evt.IsToolResultResponse() {
			responseIDs := evt.GetToolResultIDs()
			for _, responseID := range responseIDs {
				functionCallIDToResponseEventIndex[responseID] = i
			}
		}
	}

	var resultEvents []event.Event
	for _, evt := range events {
		// Create a local copy to avoid implicit memory aliasing.
		// This bug is fixed in go 1.22.
		// See: https://tip.golang.org/doc/go1.22#language
		evt := evt

		if evt.IsToolResultResponse() {
			// Function response should be handled with function call below.
			continue
		} else if evt.IsToolCallResponse() {
			functionCallIDs := evt.GetToolCallIDs()
			var responseEventIndices []int
			for _, callID := range functionCallIDs {
				if idx, exists := functionCallIDToResponseEventIndex[callID]; exists {
					responseEventIndices = append(responseEventIndices, idx)
				}
			}
			// When tools run in parallel they commonly return all results inside one response event.
			// If we pushed the same event once per tool ID, the LLM would see duplicated tool
			// messages and reject the request. Keep only the first occurrence of each event index
			// while preserving their original order.
			seenIdx := make(map[int]struct{}, len(functionCallIDs))
			uniqueIndices := responseEventIndices[:0]
			// Reuse the existing slice to deduplicate in place and maintain the original order.
			for _, idx := range responseEventIndices {
				if _, seen := seenIdx[idx]; seen {
					continue
				}
				seenIdx[idx] = struct{}{}
				uniqueIndices = append(uniqueIndices, idx)
			}
			responseEventIndices = uniqueIndices

			resultEvents = append(resultEvents, evt)

			if len(responseEventIndices) == 0 {
				continue
			} else if len(responseEventIndices) == 1 {
				resultEvents = append(resultEvents, events[responseEventIndices[0]])
			} else {
				// Merge multiple async function responses.
				var responseEvents []event.Event
				for _, idx := range responseEventIndices {
					responseEvents = append(responseEvents, events[idx])
				}
				mergedEvent := p.mergeFunctionResponseEvents(responseEvents)
				resultEvents = append(resultEvents, mergedEvent)
			}
		} else {
			resultEvents = append(resultEvents, evt)
		}
	}

	return resultEvents
}

// mergeFunctionResponseEvents merges a list of function_response events into one event.
func (p *ContentRequestProcessor) mergeFunctionResponseEvents(
	functionResponseEvents []event.Event,
) event.Event {
	if len(functionResponseEvents) == 0 {
		return event.Event{}
	}

	// Start with the first event as base.
	mergedEvent := functionResponseEvents[0]

	// Collect all tool response messages, preserving each individual ToolID.
	var allChoices []model.Choice
	for _, evt := range functionResponseEvents {
		for _, choice := range evt.Choices {
			if choice.Message.Content != "" && choice.Message.ToolID != "" {
				allChoices = append(allChoices, choice)
			}
		}
	}

	if len(allChoices) > 0 {
		mergedEvent.Response.Choices = allChoices
	}

	return mergedEvent
}

func toMap(ids []string) map[string]bool {
	m := make(map[string]bool)
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// getPreloadMemoryMessage returns preloaded memories as a system message if available.
func (p *ContentRequestProcessor) getPreloadMemoryMessage(
	ctx context.Context,
	inv *agent.Invocation,
) *model.Message {
	if inv.MemoryService == nil || inv.Session == nil {
		return nil
	}
	userKey := memory.UserKey{
		AppName: inv.Session.AppName,
		UserID:  inv.Session.UserID,
	}
	// Validate user key.
	if userKey.AppName == "" || userKey.UserID == "" {
		return nil
	}
	// Handle PreloadMemory: 0 = disabled, -1 = all, N > 0 = most recent N.
	if p.PreloadMemory == 0 {
		// PreloadMemory = 0 means disabled, return nil.
		return nil
	}
	// PreloadMemory = -1 means all memories, use 0 for ReadMemories (no limit).
	// PreloadMemory = N > 0 means most recent N memories.
	// Here we use max to handle the case when PreloadMemory is negative.
	limit := max(p.PreloadMemory, 0)
	memories, err := inv.MemoryService.ReadMemories(ctx, userKey, limit)
	if err != nil {
		log.WarnfContext(ctx, "Failed to preload memories: %v", err)
		return nil
	}
	if len(memories) == 0 {
		return nil
	}
	return &model.Message{
		Role:    model.RoleSystem,
		Content: formatMemoryContent(memories),
	}
}

// formatMemoryContent formats memories for system prompt injection.
func formatMemoryContent(memories []*memory.Entry) string {
	var sb strings.Builder
	sb.WriteString("## User Memories\n\n")
	sb.WriteString("The following are memories about the user:\n\n")
	for _, mem := range memories {
		if mem == nil || mem.Memory == nil {
			continue
		}
		fmt.Fprintf(&sb, "ID: %s\n", mem.ID)
		fmt.Fprintf(&sb, "Memory: %s\n\n", mem.Memory.Memory)
	}
	return sb.String()
}
