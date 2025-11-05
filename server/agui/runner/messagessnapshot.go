//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"fmt"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// MessagesSnapshotProvider provides a MessagesSnapshot Event stream by replaying persisted session events.
type MessagesSnapshotProvider interface {
	// MessagesSnapshot sends a MessagesSnapshot Event stream by replaying persisted session events.
	MessagesSnapshot(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error)
}

// MessagesSnapshot sends a MessagesSnapshot Event stream by replaying persisted session events.
func (r *runner) MessagesSnapshot(ctx context.Context, input *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("agui: runner is nil")
	}
	if input == nil {
		return nil, errors.New("agui: run input cannot be nil")
	}
	if r.appName == "" {
		return nil, errors.New("agui: app name is empty")
	}
	if r.sessionService == nil {
		return nil, errors.New("agui: session service is nil")
	}
	modifiedInput, err := r.applyRunAgentInputHook(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("agui: run input hook: %w", err)
	}
	events := make(chan aguievents.Event)
	go r.messagesSnapshot(ctx, modifiedInput, events)
	return events, nil
}

// messagesSnapshot sends a MessagesSnapshot Event stream by replaying persisted session events.
func (r *runner) messagesSnapshot(ctx context.Context, input *adapter.RunAgentInput, events chan<- aguievents.Event) {
	defer close(events)
	threadID := input.ThreadID
	runID := input.RunID
	// Emit a RUN_STARTED event to anchor the synthetic run.
	if !r.emitEvent(ctx, events, aguievents.NewRunStartedEvent(threadID, runID), runID) {
		return
	}
	userID, err := r.userIDResolver(ctx, input)
	if err != nil {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("resolve user ID: %v", err),
			aguievents.WithRunID(runID)), runID)
		return
	}
	sessionKey := session.Key{
		AppName:   r.appName,
		UserID:    userID,
		SessionID: threadID,
	}
	messagesSnapshotEvent, err := r.getMessagesSnapshotEvent(ctx, sessionKey)
	if err != nil {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("load history: %v", err),
			aguievents.WithRunID(runID)), runID)
		return
	}
	// Emit a MESSAGES_SNAPSHOT event to send the snapshot payload.
	if !r.emitEvent(ctx, events, messagesSnapshotEvent, runID) {
		return
	}
	// Emit a RUN_FINISHED event to signal downstream consumers there is no more data.
	if !r.emitEvent(ctx, events, aguievents.NewRunFinishedEvent(threadID, runID), runID) {
		return
	}
}

// getMessagesSnapshotEvent retrieves all session events and converts them to AG-UI MessagesSnapshotEvent.
func (r *runner) getMessagesSnapshotEvent(ctx context.Context,
	sessionKey session.Key) (*aguievents.MessagesSnapshotEvent, error) {
	events, err := r.getSessionEvents(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("runner get session events: %w", err)
	}
	return r.convertToMessagesSnapshotEvent(ctx, events)
}

// getSessionEvents retrieves all events for a given session key from session service.
func (r *runner) getSessionEvents(ctx context.Context, sessionKey session.Key) ([]event.Event, error) {
	session, err := r.sessionService.GetSession(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("session service get session: %w", err)
	}
	if session == nil {
		return nil, nil
	}
	return session.GetEvents(), nil
}

// convertToMessagesSnapshotEvent converts runner events to AG-UI MessagesSnapshotEvent.
func (r *runner) convertToMessagesSnapshotEvent(ctx context.Context,
	events []event.Event) (*aguievents.MessagesSnapshotEvent, error) {
	messages := make([]aguievents.Message, 0)
	if len(events) == 0 {
		return aguievents.NewMessagesSnapshotEvent(messages), nil
	}
	for _, event := range events {
		event, err := r.handleBeforeTranslate(ctx, &event)
		if err != nil {
			return nil, fmt.Errorf("handle before translate: %w", err)
		}
		if event == nil || event.Response == nil || len(event.Response.Choices) == 0 {
			continue
		}
		for _, choice := range event.Response.Choices {
			switch choice.Message.Role {
			case model.RoleSystem:
				messages = append(messages, *r.convertToSystemMessage(event.ID, choice))
			case model.RoleUser:
				messages = append(messages, *r.convertToUserMessage(event.ID, choice))
			case model.RoleAssistant:
				messages = append(messages, *r.convertToAssistantMessage(event.ID, choice))
			case model.RoleTool:
				messages = append(messages, *r.convertToToolMessage(event.ID, choice))
			default:
				return nil, fmt.Errorf("unknown role: %s", choice.Message.Role)
			}
		}
	}
	return aguievents.NewMessagesSnapshotEvent(messages), nil
}

// convertToSystemMessage converts system events to AG-UI Message.
func (r *runner) convertToSystemMessage(id string, choice model.Choice) *aguievents.Message {
	return &aguievents.Message{
		ID:      id,
		Role:    string(choice.Message.Role),
		Content: &choice.Message.Content,
	}
}

// convertToUserMessage converts user events to AG-UI Message.
func (r *runner) convertToUserMessage(id string, choice model.Choice) *aguievents.Message {
	return &aguievents.Message{
		ID:      id,
		Role:    string(choice.Message.Role),
		Content: &choice.Message.Content,
	}
}

// convertToAssistantMessage converts assistant events, including tool calls, to AG-UI Message.
func (r *runner) convertToAssistantMessage(id string, choice model.Choice) *aguievents.Message {
	toolCalls := make([]aguievents.ToolCall, 0)
	for _, toolCall := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, aguievents.ToolCall{
			ID:   toolCall.ID,
			Type: toolCall.Type,
			Function: aguievents.Function{
				Name:      toolCall.Function.Name,
				Arguments: string(toolCall.Function.Arguments),
			},
		})
	}
	return &aguievents.Message{
		ID:        id,
		Role:      string(choice.Message.Role),
		Content:   &choice.Message.Content,
		ToolCalls: toolCalls,
	}
}

// convertToToolMessage converts tool responses to AG-UI Message.
func (r *runner) convertToToolMessage(id string, choice model.Choice) *aguievents.Message {
	return &aguievents.Message{
		ID:         id,
		Role:       string(choice.Message.Role),
		Content:    &choice.Message.Content,
		Name:       &choice.Message.ToolName,
		ToolCallID: &choice.Message.ToolID,
	}
}
