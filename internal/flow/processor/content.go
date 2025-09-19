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
	// MaxHistoryRuns limits the number of recent messages appended after
	// branch-incremental selection (0 means unlimited).
	MaxHistoryRuns int
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

// WithMaxHistoryRuns limits the number of recent messages appended after
// branch-incremental selection (0 means unlimited).
func WithMaxHistoryRuns(n int) ContentOption {
	return func(p *ContentRequestProcessor) {
		p.MaxHistoryRuns = n
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

	// 0) If caller supplied explicit messages via RunOptions, prefer them
	// and skip deriving from session or the single invocation message to
	// avoid duplication. This supports use cases where the upstream
	// system maintains the conversation history and passes it in each run.
	if len(invocation.RunOptions.Messages) > 0 {
		req.Messages = append(req.Messages, invocation.RunOptions.Messages...)

		// Send a preprocessing event and return early.
		evt := event.New(invocation.InvocationID, invocation.AgentName, event.WithObject(model.ObjectTypePreprocessingContent))
		log.Debugf("Content request processor: used explicit messages (%d)", len(invocation.RunOptions.Messages))
		agent.EmitEvent(ctx, invocation, ch, evt)
		return
	}

	// 1) Prepend session summary as a system message if enabled and available.
	if p.AddSessionSummary && invocation.Session != nil {
		if msg := p.getSessionSummaryMessage(invocation); msg != nil {
			// Prepend to the front of messages.
			req.Messages = append([]model.Message{*msg}, req.Messages...)
		}
	}

	// 2) Append branch-incremental messages from session events when allowed.
	if p.IncludeContents != IncludeContentsNone && invocation.Session != nil {
		messages := p.getBranchIncrementalMessages(invocation)
		req.Messages = append(req.Messages, messages...)
	}

	// 3) Include the current invocation message if:
	// 1. It has content, AND
	// 2. There's no session OR the session has no events
	// This prevents duplication when using Runner (which adds user message to session)
	// while ensuring standalone usage works (where invocation.Message is the source)
	if invocation.Message.Content != "" &&
		(invocation.Session == nil || len(invocation.Session.Events) == 0) {
		req.Messages = append(req.Messages, invocation.Message)
		log.Debugf("Content request processor: added invocation message with role %s (no session or empty session)",
			invocation.Message.Role)
	}

	// 4) Safety fallback: if messages are still empty, include the current
	// invocation message when non-empty to avoid empty model input.
	if len(req.Messages) == 0 && invocation.Message.Content != "" {
		req.Messages = append(req.Messages, invocation.Message)
		log.Debugf("Content request processor: fallback added invocation message to avoid empty input.")
	}

	// Send a preprocessing event.
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingPlanning),
	))
}

// getSessionSummaryMessage returns the current-branch session summary as a
// system message if available and non-empty.
func (p *ContentRequestProcessor) getSessionSummaryMessage(inv *agent.Invocation) *model.Message {
	if inv == nil || inv.Session == nil || inv.Session.Summaries == nil {
		return nil
	}
	branch := inv.GetEventFilterKey()
	if branch == "" {
		branch = inv.AgentName
	}
	sum := inv.Session.Summaries[branch]
	if sum == nil || sum.Summary == "" {
		return nil
	}
	return &model.Message{Role: model.RoleSystem, Content: sum.Summary}
}

// getBranchIncrementalMessages converts branch-incremental events into messages
// and applies MaxHistoryRuns truncation.
func (p *ContentRequestProcessor) getBranchIncrementalMessages(inv *agent.Invocation) []model.Message {
	branch := inv.GetEventFilterKey()
	if branch == "" {
		branch = inv.AgentName
	}
	var evs []event.Event
	if inv.Session != nil && inv.Session.Summaries != nil {
		if sum := inv.Session.Summaries[branch]; sum != nil {
			evs = p.eventsSince(inv.Session.Events, sum.UpdatedAt, branch)
		} else {
			evs = p.eventsInBranch(inv.Session.Events, branch)
		}
	}
	msgs := p.convertEventsToMessages(evs, inv.AgentName)
	if p.MaxHistoryRuns > 0 && len(msgs) > p.MaxHistoryRuns {
		msgs = msgs[len(msgs)-p.MaxHistoryRuns:]
	}
	return msgs
}

// eventsSince returns events after the given time and matching the branch filter.
func (p *ContentRequestProcessor) eventsSince(
	events []event.Event,
	since time.Time,
	branch string,
) []event.Event {
	var result []event.Event
	for _, evt := range events {
		if evt.Timestamp.After(since) && evt.Filter(branch) && evt.IsValidContent() {
			result = append(result, evt)
		}
	}
	return result
}

// eventsInBranch returns all events matching the branch filter.
func (p *ContentRequestProcessor) eventsInBranch(
	events []event.Event,
	branch string,
) []event.Event {
	var result []event.Event
	for _, evt := range events {
		if evt.Filter(branch) && evt.IsValidContent() {
			result = append(result, evt)
		}
	}
	return result
}

// convertEventsToMessages converts a list of events (already filtered for
// validity/branch) into model messages, reusing the same conversion rules
// as getContents.
func (p *ContentRequestProcessor) convertEventsToMessages(
	events []event.Event,
	agentName string,
) []model.Message {
	// Rearrange events for function call/response consistency.
	resultEvents := p.rearrangeLatestFuncResp(events)
	resultEvents = p.rearrangeAsyncFuncRespHist(resultEvents)

	// Convert events to messages.
	var messages []model.Message
	for _, evt := range resultEvents {
		// Convert foreign events or keep as-is.
		ev := evt
		if p.isOtherAgentReply(agentName, &ev) {
			ev = p.convertForeignEvent(&ev)
		}
		if len(ev.Choices) > 0 {
			for _, choice := range ev.Choices {
				if choice.Message.Content != "" || choice.Message.ToolID != "" || len(choice.Message.ToolCalls) > 0 {
					messages = append(messages, choice.Message)
				}
			}
		}
	}
	return messages
}

// isOtherAgentReply checks whether the event is a reply from another agent.
func (p *ContentRequestProcessor) isOtherAgentReply(
	currentAgentName string,
	evt *event.Event,
) bool {
	return currentAgentName != "" &&
		evt.Author != currentAgentName &&
		evt.Author != "user" &&
		evt.Author != ""
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
		convertedEvent.Choices = []model.Choice{
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
		mergedEvent.Choices = allChoices
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
