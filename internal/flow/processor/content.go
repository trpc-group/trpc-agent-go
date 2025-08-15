//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package processor provides content processing logic for agent requests.
// It includes utilities for including, filtering, and rearranging session
// events for LLM requests, as well as helpers for function call/response
// event handling.
//

package processor

import (
	"context"
	"fmt"
	"strings"

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
	// Options: "none", "all", "filtered" (default: "all").
	IncludeContents string
	// AddContextPrefix controls whether to add "For context:" prefix when converting foreign events.
	// When false, foreign agent events are passed directly without the prefix.
	AddContextPrefix bool
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

// NewContentRequestProcessor creates a new content request processor.
func NewContentRequestProcessor(opts ...ContentOption) *ContentRequestProcessor {
	processor := &ContentRequestProcessor{
		IncludeContents:  IncludeContentsAll, // Default to include all contents.
		AddContextPrefix: true,               // Default to add context prefix.
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

	// Process session events if available and includeContents is not "none".
	if p.IncludeContents != IncludeContentsNone && invocation.Session != nil {
		sessionMessages := p.getContents(
			invocation.Branch, // Current branch for filtering
			invocation.Session.Events,
			invocation.AgentName, // Current agent name for filtering
		)
		req.Messages = append(req.Messages, sessionMessages...)
	}

	// Include the current invocation message if:
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

	// Send a preprocessing event.
	if invocation != nil {
		evt := event.New(invocation.InvocationID, invocation.AgentName)
		evt.Object = model.ObjectTypePreprocessingContent
		evt.Branch = invocation.Branch // Set branch for hierarchical event filtering

		select {
		case ch <- evt:
			log.Debugf("Content request processor: sent preprocessing event")
		case <-ctx.Done():
			log.Debugf("Content request processor: context cancelled")
		}
	}
}

// getContents gets the contents for the LLM request from session events.
func (p *ContentRequestProcessor) getContents(
	currentBranch string,
	events []event.Event,
	agentName string,
) []model.Message {
	var filteredEvents []event.Event

	// Parse the events, leaving the contents and the function calls and
	// responses from the current agent.
	for _, evt := range events {
		// Skip events without content, or generated neither by user nor by model
		// or has empty text. E.g. events purely for mutating session states.
		if !p.hasValidContent(&evt) {
			continue
		}

		// Skip events not belong to current branch.
		if !p.isEventBelongsToBranch(currentBranch, &evt) {
			continue
		}

		// Convert foreign events or keep as-is.
		if p.isOtherAgentReply(agentName, &evt) {
			filteredEvents = append(filteredEvents, p.convertForeignEvent(&evt))
		} else {
			filteredEvents = append(filteredEvents, evt)
		}
	}

	// Rearrange events for function call/response consistency.
	resultEvents := p.rearrangeLatestFuncResp(filteredEvents)
	resultEvents = p.rearrangeAsyncFuncRespHist(resultEvents)

	// Convert events to messages.
	var messages []model.Message
	for _, evt := range resultEvents {
		if len(evt.Choices) > 0 {
			for _, choice := range evt.Choices {
				if choice.Message.Content != "" || choice.Message.ToolID != "" || len(choice.Message.ToolCalls) > 0 {
					// Remove client function call IDs if needed (simplified).
					messages = append(messages, choice.Message)
				}
			}
		}
	}

	return messages
}

// hasValidContent checks if an event has valid content for message generation.
func (p *ContentRequestProcessor) hasValidContent(evt *event.Event) bool {
	// Check if event has choices with content.
	if len(evt.Choices) > 0 {
		for _, choice := range evt.Choices {
			if choice.Message.Content != "" {
				return true
			}
			if choice.Delta.Content != "" {
				return true
			}
		}
	}
	if len(evt.Choices) > 0 && evt.Choices[0].Message.ToolID != "" {
		return true
	}
	if len(evt.Choices) > 0 && len(evt.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	return false
}

// isEventBelongsToBranch checks if an event belongs to a specific branch.
// Event belongs to a branch when event.branch is prefix of the invocation branch.
func (p *ContentRequestProcessor) isEventBelongsToBranch(
	invocationBranch string,
	evt *event.Event,
) bool {
	if invocationBranch == "" || evt.Branch == "" {
		return true
	}
	return strings.HasPrefix(invocationBranch, evt.Branch)
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
	if !p.isFunctionResponseEvent(&lastEvent) {
		return events
	}

	functionResponseIDs := p.getFunctionResponseIDs(&lastEvent)
	if len(functionResponseIDs) == 0 {
		return events
	}

	// Look for corresponding function call event.
	functionCallEventIdx := -1
	for i := len(events) - 2; i >= 0; i-- {
		evt := &events[i]
		if p.isFunctionCallEvent(evt) {
			functionCallIDs := p.getFunctionCallIDs(evt)
			for responseID := range functionResponseIDs {
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
		if p.isFunctionResponseEvent(evt) {
			responseIDs := p.getFunctionResponseIDs(evt)
			for responseID := range functionResponseIDs {
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
		if p.isFunctionResponseEvent(&evt) {
			responseIDs := p.getFunctionResponseIDs(&evt)
			for responseID := range responseIDs {
				functionCallIDToResponseEventIndex[responseID] = i
			}
		}
	}

	var resultEvents []event.Event
	for _, evt := range events {
		if p.isFunctionResponseEvent(&evt) {
			// Function response should be handled with function call below.
			continue
		} else if p.isFunctionCallEvent(&evt) {
			functionCallIDs := p.getFunctionCallIDs(&evt)
			var responseEventIndices []int
			for callID := range functionCallIDs {
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

// Helper functions for function call/response detection and processing.

func (p *ContentRequestProcessor) isFunctionCallEvent(evt *event.Event) bool {
	if len(evt.Choices) == 0 {
		return false
	}
	return len(evt.Choices[0].Message.ToolCalls) > 0
}

func (p *ContentRequestProcessor) isFunctionResponseEvent(evt *event.Event) bool {
	if len(evt.Choices) == 0 {
		return false
	}
	return evt.Choices[0].Message.ToolID != ""
}

func (p *ContentRequestProcessor) getFunctionCallIDs(evt *event.Event) map[string]bool {
	ids := make(map[string]bool)
	if len(evt.Choices) > 0 {
		for _, toolCall := range evt.Choices[0].Message.ToolCalls {
			ids[toolCall.ID] = true
		}
	}
	return ids
}

func (p *ContentRequestProcessor) getFunctionResponseIDs(evt *event.Event) map[string]bool {
	ids := make(map[string]bool)
	if len(evt.Choices) > 0 && evt.Choices[0].Message.ToolID != "" {
		ids[evt.Choices[0].Message.ToolID] = true
	}
	return ids
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
