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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Content inclusion options.
const (
	IncludeContentsNone     = "none"
	IncludeContentsAll      = "all"
	IncludeContentsFiltered = "filtered"
)

// ContentRequestProcessor implements content processing logic for agent requests.
type ContentRequestProcessor struct {
	// IncludeContents determines how to include content from session events.
	// Options: "none", "all", "filtered" (default: "filtered").
	IncludeContents string
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
}

// ContentOption is a functional option for configuring the ContentRequestProcessor.
type ContentOption func(*ContentRequestProcessor)

// WithIncludeContents sets how to include content from session events.
func WithIncludeContents(includeContents string) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.IncludeContents = includeContents
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

// NewContentRequestProcessor creates a new content request processor.
func NewContentRequestProcessor(opts ...ContentOption) *ContentRequestProcessor {
	processor := &ContentRequestProcessor{
		IncludeContents:  IncludeContentsFiltered, // Default only to include filtered contents.
		AddContextPrefix: true,                    // Default to add context prefix.
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
		log.Errorf("Content request processor: request is nil")
		return
	}

	if invocation == nil {
		return
	}

	// 2) Append per-filter messages from session events when allowed.
	needToAddInvocationMessage := true
	if p.IncludeContents != IncludeContentsNone && invocation.Session != nil {
		var messages []model.Message
		var summaryUpdatedAt time.Time
		if p.AddSessionSummary {
			// Prepend session summary as a system message if enabled and available.
			// Also get the summary's UpdatedAt to ensure consistency with incremental messages.
			if msg, updatedAt := p.getSessionSummaryMessage(invocation); msg != nil {
				// Prepend to the front of messages.
				req.Messages = append([]model.Message{*msg}, req.Messages...)
				summaryUpdatedAt = updatedAt
			}
		}
		messages = p.getHistoryMessages(invocation, summaryUpdatedAt)
		req.Messages = append(req.Messages, messages...)
		needToAddInvocationMessage = summaryUpdatedAt.IsZero() && len(messages) == 0
	}

	if invocation.Message.Content != "" && needToAddInvocationMessage {
		req.Messages = append(req.Messages, invocation.Message)
		log.Debugf("Content request processor: added invocation message with role %s (no session or empty session)",
			invocation.Message.Role)
	}

	// Send a preprocessing event.
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingPlanning),
	))
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
	// For IncludeContentsAll, prefer the full-session summary under empty filter key.
	if p.IncludeContents == IncludeContentsAll {
		filter = ""
	}
	sum := inv.Session.Summaries[filter]
	if sum == nil || sum.Summary == "" {
		return nil, time.Time{}
	}
	return &model.Message{Role: model.RoleSystem, Content: sum.Summary}, sum.UpdatedAt
}

// getHistoryMessages gets history messages for the current filter, potentially truncated by MaxHistoryRuns.
// This method is used when AddSessionSummary is false to get recent history messages.
func (p *ContentRequestProcessor) getHistoryMessages(inv *agent.Invocation, since time.Time) []model.Message {
	if inv.Session == nil {
		return nil
	}
	isZeroTime := since.IsZero()
	filter := inv.GetEventFilterKey()
	var events []event.Event
	inv.Session.EventMu.RLock()
	for _, evt := range inv.Session.Events {
		if evt.Response != nil && !evt.IsPartial && evt.IsValidContent() &&
			(p.IncludeContents != IncludeContentsFiltered || evt.Filter(filter)) &&
			(isZeroTime || evt.Timestamp.After(since)) {
			events = append(events, evt)
		}
	}
	inv.Session.EventMu.RUnlock()

	resultEvents := p.rearrangeLatestFuncResp(events)
	resultEvents = p.rearrangeAsyncFuncRespHist(resultEvents)
	// Convert events to messages.
	var messages []model.Message
	for _, evt := range resultEvents {
		// Convert foreign events or keep as-is.
		ev := evt
		if p.isOtherAgentReply(inv.AgentName, inv.Branch, &ev) {
			ev = p.convertForeignEvent(&ev)
		}
		if len(ev.Choices) > 0 {
			for _, choice := range ev.Choices {
				messages = append(messages, choice.Message)
			}
		}
	}

	// Apply MaxHistoryRuns limit if set MaxHistoryRuns and AddSessionSummary is false.
	if !p.AddSessionSummary && p.MaxHistoryRuns > 0 && len(messages) > p.MaxHistoryRuns {
		startIdx := len(messages) - p.MaxHistoryRuns
		messages = messages[startIdx:]
	}
	return messages
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
		if evt.Branch == currentBranch || strings.HasPrefix(evt.Branch, currentBranch+agent.BranchDelimiter) {
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
	var contentParts []string
	if p.AddContextPrefix {
		contentParts = append(contentParts, "For context:")
	}

	for _, choice := range evt.Choices {
		if choice.Message.Content != "" {
			if p.AddContextPrefix {
				contentParts = append(contentParts,
					fmt.Sprintf("[%s] said: %s", evt.Author, choice.Message.Content))
			} else {
				// When prefix is disabled, pass the content directly.
				contentParts = append(contentParts, choice.Message.Content)
			}
		} else if len(choice.Message.ToolCalls) > 0 {
			for _, toolCall := range choice.Message.ToolCalls {
				if p.AddContextPrefix {
					contentParts = append(contentParts,
						fmt.Sprintf("[%s] called tool `%s` with parameters: %s",
							evt.Author, toolCall.Function.Name, string(toolCall.Function.Arguments)))
				} else {
					// When prefix is disabled, pass tool call info directly.
					contentParts = append(contentParts,
						fmt.Sprintf("Tool `%s` called with parameters: %s",
							toolCall.Function.Name, string(toolCall.Function.Arguments)))
				}
			}
		} else if choice.Message.ToolID != "" {
			if p.AddContextPrefix {
				contentParts = append(contentParts,
					fmt.Sprintf("[%s] `%s` tool returned result: %s",
						evt.Author, choice.Message.ToolID, choice.Message.Content))
			} else {
				// When prefix is disabled, pass tool result directly.
				contentParts = append(contentParts, choice.Message.Content)
			}
		}
	}

	// Set the converted message.
	if len(contentParts) > 0 {
		convertedEvent.Response.Choices = []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleUser,
					Content: strings.Join(contentParts, " "),
				},
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
