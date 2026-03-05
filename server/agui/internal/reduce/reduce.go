//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reduce implements the logic to reduce the AG-UI track events into message snapshots.
package reduce

import (
	"encoding/json"
	"fmt"
	"strings"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// reducer reduces the AG-UI track events into message snapshots.
type reducer struct {
	appName              string
	userID               string
	texts                map[string]*textState
	reasonings           map[string]*reasoningState
	lastReasoningChunkID string
	toolCalls            map[string]*toolCallState
	messages             []*aguievents.Message
}

// textPhase is the phase of the text message.
type textPhase int

const (
	textReceiving textPhase = iota
	textEnded
)

// textState is the state of the text message.
type textState struct {
	role    string
	name    string
	content strings.Builder
	phase   textPhase
	index   int
}

type reasoningPhase int

const (
	reasoningReceiving reasoningPhase = iota
	reasoningEnded
)

type reasoningState struct {
	role    string
	name    string
	content strings.Builder
	phase   reasoningPhase
	index   int
}

// toolPhase is the phase of the tool call.
type toolPhase int

const (
	toolAwaitingArgs toolPhase = iota
	toolAwaitingResult
	toolCompleted
)

// toolCallState is the state of the tool call.
type toolCallState struct {
	messageID string
	name      string
	content   strings.Builder
	phase     toolPhase
	index     int
}

// Reduce reduces the AG-UI track events into message snapshots.
// In order to fetch the history messages as much as possible, still return the messages even if there is an error.
func Reduce(appName, userID string, events []session.TrackEvent) ([]aguievents.Message, error) {
	r := new(appName, userID)
	var err error
	for _, trackEvent := range events {
		if err = r.reduce(trackEvent); err != nil {
			err = fmt.Errorf("reduce: %w", err)
			break
		}
	}
	r.finalizePartial()
	messages := make([]aguievents.Message, 0, len(r.messages))
	for _, message := range r.messages {
		messages = append(messages, *message)
	}
	// In order to fetch the history messages as much as possible, still return the messages even if there is an error.
	return messages, err
}

// new creates a new reducer.
func new(appName, userID string) *reducer {
	return &reducer{
		appName:    appName,
		userID:     userID,
		texts:      make(map[string]*textState),
		reasonings: make(map[string]*reasoningState),
		toolCalls:  make(map[string]*toolCallState),
		messages:   make([]*aguievents.Message, 0),
	}
}

// reduce reduces the AG-UI track event into a message snapshot.
func (r *reducer) reduce(trackEvent session.TrackEvent) error {
	if len(trackEvent.Payload) == 0 {
		return nil
	}
	evt, err := aguievents.EventFromJSON(trackEvent.Payload)
	if err != nil {
		return fmt.Errorf("unmarshal track event payload: %w", err)
	}
	return r.reduceEvent(evt)
}

func (r *reducer) reduceEvent(evt aguievents.Event) error {
	switch e := evt.(type) {
	case *aguievents.TextMessageStartEvent:
		return r.handleTextStart(e)
	case *aguievents.TextMessageContentEvent:
		return r.handleTextContent(e)
	case *aguievents.TextMessageEndEvent:
		return r.handleTextEnd(e)
	case *aguievents.TextMessageChunkEvent:
		return r.handleTextChunk(e)
	case *aguievents.ToolCallStartEvent:
		return r.handleToolStart(e)
	case *aguievents.ToolCallArgsEvent:
		return r.handleToolArgs(e)
	case *aguievents.ToolCallEndEvent:
		return r.handleToolEnd(e)
	case *aguievents.ToolCallResultEvent:
		return r.handleToolResult(e)
	case *aguievents.ReasoningStartEvent:
		return nil
	case *aguievents.ReasoningMessageStartEvent:
		return r.handleReasoningMessageStart(e)
	case *aguievents.ReasoningMessageContentEvent:
		return r.handleReasoningContent(e)
	case *aguievents.ReasoningMessageEndEvent:
		return r.handleReasoningEnd(e)
	case *aguievents.ReasoningMessageChunkEvent:
		return r.handleReasoningChunk(e)
	case *aguievents.ReasoningEncryptedValueEvent:
		return r.handleReasoningEncryptedValue(e)
	case *aguievents.ReasoningEndEvent:
		return nil
	case *aguievents.CustomEvent:
		if e.Name == multimodal.CustomEventNameUserMessage {
			return r.handleUserMessageCustomEvent(e)
		}
		return r.handleActivity(e)
	default:
		return r.handleActivity(e)
	}
}

func (r *reducer) handleUserMessageCustomEvent(e *aguievents.CustomEvent) error {
	if e.Value == nil {
		return fmt.Errorf("user message custom event missing value")
	}
	data, err := json.Marshal(e.Value)
	if err != nil {
		return fmt.Errorf("marshal user message custom event value: %w", err)
	}
	var message types.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return fmt.Errorf("unmarshal user message custom event value: %w", err)
	}
	if message.Role != types.RoleUser {
		return fmt.Errorf("user message custom event role must be user: %s", message.Role)
	}
	if message.ID == "" {
		return fmt.Errorf("user message custom event missing message id")
	}
	if message.Name == "" {
		message.Name = r.userID
	}
	if _, ok := message.ContentString(); !ok {
		if _, ok := message.ContentInputContents(); !ok {
			return fmt.Errorf("user message custom event content is invalid")
		}
	}
	r.messages = append(r.messages, &message)
	return nil
}

func (r *reducer) finalizePartial() {
	for _, state := range r.texts {
		if state.phase != textReceiving || state.content.Len() == 0 {
			continue
		}
		text := strings.Clone(state.content.String())
		r.messages[state.index].Content = &text
	}
	for _, state := range r.reasonings {
		if state.phase != reasoningReceiving || state.content.Len() == 0 {
			continue
		}
		text := strings.Clone(state.content.String())
		r.messages[state.index].Content = &text
	}
	for _, state := range r.toolCalls {
		if state.phase != toolAwaitingArgs || state.content.Len() == 0 {
			continue
		}
		parentState, ok := r.texts[state.messageID]
		if !ok {
			continue
		}
		if parentState.index < 0 || parentState.index >= len(r.messages) {
			continue
		}
		parent := r.messages[parentState.index]
		if state.index < 0 || state.index >= len(parent.ToolCalls) {
			continue
		}
		parent.ToolCalls[state.index].Function.Arguments = strings.Clone(state.content.String())
	}
}

// handleTextStart handles the text message start event.
func (r *reducer) handleTextStart(e *aguievents.TextMessageStartEvent) error {
	if e.MessageID == "" {
		return fmt.Errorf("text message start missing id")
	}
	if _, exists := r.texts[e.MessageID]; exists {
		return fmt.Errorf("duplicate text message start: %s", e.MessageID)
	}
	role := string(model.RoleAssistant)
	if e.Role != nil && *e.Role != "" {
		role = string(*e.Role)
	}
	name := ""
	switch role {
	case string(model.RoleUser):
		name = r.userID
	case string(model.RoleAssistant):
		name = r.appName
	default:
		return fmt.Errorf("unsupported role: %s", role)
	}
	r.messages = append(r.messages, &aguievents.Message{
		ID:   e.MessageID,
		Role: types.Role(role),
		Name: name,
	})
	r.texts[e.MessageID] = &textState{
		role:  role,
		name:  name,
		phase: textReceiving,
		index: len(r.messages) - 1,
	}
	return nil
}

// handleTextContent handles the text message content event.
func (r *reducer) handleTextContent(e *aguievents.TextMessageContentEvent) error {
	state, ok := r.texts[e.MessageID]
	if !ok {
		return fmt.Errorf("text message content without start: %s", e.MessageID)
	}
	if state.phase != textReceiving {
		return fmt.Errorf("text message content after end: %s", e.MessageID)
	}
	state.content.WriteString(e.Delta)
	return nil
}

// handleTextEnd handles the text message end event.
func (r *reducer) handleTextEnd(e *aguievents.TextMessageEndEvent) error {
	state, ok := r.texts[e.MessageID]
	if !ok {
		return fmt.Errorf("text message end without start: %s", e.MessageID)
	}
	if state.phase != textReceiving {
		return fmt.Errorf("duplicate text message end: %s", e.MessageID)
	}
	state.phase = textEnded
	text := strings.Clone(state.content.String())
	r.messages[state.index].Content = &text
	return nil
}

// handleTextChunk handles the text message chunk event.
func (r *reducer) handleTextChunk(e *aguievents.TextMessageChunkEvent) error {
	if e.MessageID == nil || *e.MessageID == "" {
		return fmt.Errorf("text message chunk missing id")
	}
	if _, exists := r.texts[*e.MessageID]; exists {
		return fmt.Errorf("duplicate text message chunk: %s", *e.MessageID)
	}
	role := string(model.RoleAssistant)
	if e.Role != nil && *e.Role != "" {
		role = string(*e.Role)
	}
	name := ""
	switch role {
	case string(model.RoleUser):
		name = r.userID
	case string(model.RoleAssistant):
		name = r.appName
	default:
		return fmt.Errorf("unsupported role: %s", role)
	}
	content := ""
	if e.Delta != nil {
		content = strings.Clone(*e.Delta)
	}
	r.messages = append(r.messages, &aguievents.Message{
		ID:      *e.MessageID,
		Role:    types.Role(role),
		Name:    name,
		Content: &content,
	})
	builder := strings.Builder{}
	builder.WriteString(content)
	r.texts[*e.MessageID] = &textState{
		role:    role,
		name:    name,
		content: builder,
		phase:   textEnded,
		index:   len(r.messages) - 1,
	}
	return nil
}

func (r *reducer) handleReasoningMessageStart(e *aguievents.ReasoningMessageStartEvent) error {
	if e.MessageID == "" {
		return fmt.Errorf("reasoning message start missing id")
	}
	if _, exists := r.reasonings[e.MessageID]; exists {
		return fmt.Errorf("duplicate reasoning message start: %s", e.MessageID)
	}
	role := e.Role
	if role == "" {
		role = string(model.RoleAssistant)
	}
	if role != string(model.RoleAssistant) {
		return fmt.Errorf("unsupported role: %s", role)
	}
	name := r.appName
	msg := &aguievents.Message{
		ID:   e.MessageID,
		Role: types.RoleReasoning,
		Name: name,
	}
	r.messages = append(r.messages, msg)
	r.reasonings[e.MessageID] = &reasoningState{
		role:  role,
		name:  name,
		phase: reasoningReceiving,
		index: len(r.messages) - 1,
	}
	return nil
}

func (r *reducer) handleReasoningContent(e *aguievents.ReasoningMessageContentEvent) error {
	state, ok := r.reasonings[e.MessageID]
	if !ok {
		return fmt.Errorf("reasoning message content without start: %s", e.MessageID)
	}
	if state.phase != reasoningReceiving {
		return fmt.Errorf("reasoning message content after end: %s", e.MessageID)
	}
	state.content.WriteString(e.Delta)
	return nil
}

func (r *reducer) handleReasoningEnd(e *aguievents.ReasoningMessageEndEvent) error {
	state, ok := r.reasonings[e.MessageID]
	if !ok {
		return fmt.Errorf("reasoning message end without start: %s", e.MessageID)
	}
	if state.phase != reasoningReceiving {
		return fmt.Errorf("duplicate reasoning message end: %s", e.MessageID)
	}
	state.phase = reasoningEnded
	text := strings.Clone(state.content.String())
	r.messages[state.index].Content = &text
	if r.lastReasoningChunkID == e.MessageID {
		r.lastReasoningChunkID = ""
	}
	return nil
}

func (r *reducer) handleReasoningChunk(e *aguievents.ReasoningMessageChunkEvent) error {
	messageID := ""
	if e.MessageID != nil && *e.MessageID != "" {
		messageID = *e.MessageID
		r.lastReasoningChunkID = messageID
	} else if r.lastReasoningChunkID != "" {
		messageID = r.lastReasoningChunkID
	} else {
		return fmt.Errorf("reasoning message chunk missing id")
	}

	state, ok := r.reasonings[messageID]
	if ok {
		if state.phase != reasoningReceiving {
			return fmt.Errorf("reasoning message chunk after end: %s", messageID)
		}
		if e.Delta == nil {
			return nil
		}
		if *e.Delta == "" {
			state.phase = reasoningEnded
			if r.lastReasoningChunkID == messageID {
				r.lastReasoningChunkID = ""
			}
			if state.content.Len() > 0 {
				text := strings.Clone(state.content.String())
				r.messages[state.index].Content = &text
			}
			return nil
		}
		state.content.WriteString(*e.Delta)
		return nil
	}

	msg := &aguievents.Message{
		ID:   messageID,
		Role: types.RoleReasoning,
		Name: r.appName,
	}
	r.messages = append(r.messages, msg)
	r.reasonings[messageID] = &reasoningState{
		role:  string(model.RoleAssistant),
		name:  r.appName,
		phase: reasoningReceiving,
		index: len(r.messages) - 1,
	}
	if e.Delta != nil {
		if *e.Delta == "" {
			r.reasonings[messageID].phase = reasoningEnded
			if r.lastReasoningChunkID == messageID {
				r.lastReasoningChunkID = ""
			}
		} else {
			r.reasonings[messageID].content.WriteString(*e.Delta)
		}
	}
	return nil
}

func (r *reducer) handleReasoningEncryptedValue(e *aguievents.ReasoningEncryptedValueEvent) error {
	if e.EntityID == "" {
		return fmt.Errorf("reasoning encrypted value missing entity id")
	}
	if e.EncryptedValue == "" {
		return fmt.Errorf("reasoning encrypted value missing encrypted value")
	}
	if e.Subtype != aguievents.ReasoningEncryptedValueSubtypeMessage {
		return nil
	}
	state, ok := r.reasonings[e.EntityID]
	if !ok {
		msg := &aguievents.Message{
			ID:             e.EntityID,
			Role:           types.RoleReasoning,
			Name:           r.appName,
			EncryptedValue: e.EncryptedValue,
		}
		r.messages = append(r.messages, msg)
		r.reasonings[e.EntityID] = &reasoningState{
			role:  string(model.RoleAssistant),
			name:  r.appName,
			phase: reasoningEnded,
			index: len(r.messages) - 1,
		}
		return nil
	}
	if state.index < 0 || state.index >= len(r.messages) {
		return fmt.Errorf("reasoning encrypted value missing target message: %s", e.EntityID)
	}
	r.messages[state.index].EncryptedValue = e.EncryptedValue
	return nil
}

// handleToolStart handles the tool call start event.
func (r *reducer) handleToolStart(e *aguievents.ToolCallStartEvent) error {
	if e.ToolCallID == "" {
		return fmt.Errorf("tool call start missing id")
	}
	if _, exists := r.toolCalls[e.ToolCallID]; exists {
		return fmt.Errorf("duplicate tool call start: %s", e.ToolCallID)
	}
	if e.ParentMessageID == nil {
		return fmt.Errorf("tool call start missing parent message id")
	}
	parentState, ok := r.texts[*e.ParentMessageID]
	if !ok {
		name := r.appName
		r.messages = append(r.messages, &aguievents.Message{
			ID:   *e.ParentMessageID,
			Role: types.Role(string(model.RoleAssistant)),
			Name: name,
		})
		parentState = &textState{
			role:  string(model.RoleAssistant),
			name:  r.appName,
			phase: textEnded,
			index: len(r.messages) - 1,
		}
		r.texts[*e.ParentMessageID] = parentState
	}
	r.messages[parentState.index].ToolCalls = append(r.messages[parentState.index].ToolCalls, aguievents.ToolCall{
		ID:   e.ToolCallID,
		Type: "function",
		Function: aguievents.Function{
			Name: e.ToolCallName,
		},
	})
	r.toolCalls[e.ToolCallID] = &toolCallState{
		messageID: *e.ParentMessageID,
		name:      e.ToolCallName,
		phase:     toolAwaitingArgs,
		index:     len(r.messages[parentState.index].ToolCalls) - 1,
	}
	return nil
}

// handleToolArgs handles the tool call arguments event.
func (r *reducer) handleToolArgs(e *aguievents.ToolCallArgsEvent) error {
	state, ok := r.toolCalls[e.ToolCallID]
	if !ok {
		return fmt.Errorf("tool call args without start: %s", e.ToolCallID)
	}
	if state.phase != toolAwaitingArgs {
		return fmt.Errorf("tool call args invalid phase: %s", e.ToolCallID)
	}
	state.content.WriteString(e.Delta)
	return nil
}

// handleToolEnd handles the tool call end event.
func (r *reducer) handleToolEnd(e *aguievents.ToolCallEndEvent) error {
	state, ok := r.toolCalls[e.ToolCallID]
	if !ok {
		return fmt.Errorf("tool call end without start: %s", e.ToolCallID)
	}
	if state.phase != toolAwaitingArgs {
		return fmt.Errorf("duplicate tool call end: %s", e.ToolCallID)
	}
	parentState, ok := r.texts[state.messageID]
	if !ok {
		return fmt.Errorf("tool call end missing parent message: %s", state.messageID)
	}
	r.messages[parentState.index].ToolCalls[state.index].Function.Arguments = strings.Clone(state.content.String())
	state.phase = toolAwaitingResult
	return nil
}

// handleToolResult handles the tool call result event.
func (r *reducer) handleToolResult(e *aguievents.ToolCallResultEvent) error {
	if e.MessageID == "" || e.ToolCallID == "" {
		return fmt.Errorf("tool call result missing identifiers")
	}
	state, ok := r.toolCalls[e.ToolCallID]
	if !ok || state.phase != toolAwaitingResult {
		return fmt.Errorf("tool call result without completed call: %s", e.ToolCallID)
	}
	role := string(model.RoleTool)
	if e.Role != nil && *e.Role != "" {
		role = *e.Role
	}
	content := strings.Clone(e.Content)
	toolCallID := strings.Clone(e.ToolCallID)
	msg := &aguievents.Message{
		ID:         e.MessageID,
		Role:       types.Role(role),
		Content:    &content,
		ToolCallID: toolCallID,
	}
	r.messages = append(r.messages, msg)
	state.phase = toolCompleted
	return nil
}

// handleActivity handles the activity event.
func (r *reducer) handleActivity(e aguievents.Event) error {
	activity := &aguievents.Message{Role: "activity"}
	switch e := e.(type) {
	case *aguievents.StepStartedEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"stepName": e.StepName,
		}
	case *aguievents.StepFinishedEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"stepName": e.StepName,
		}
	case *aguievents.StateSnapshotEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"snapshot": e.Snapshot,
		}
	case *aguievents.StateDeltaEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"delta": e.Delta,
		}
	case *aguievents.MessagesSnapshotEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"messages": e.Messages,
		}
	case *aguievents.ActivitySnapshotEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"messageId":    e.MessageID,
			"activityType": e.ActivityType,
			"content":      e.Content,
			"replace":      e.Replace,
		}
	case *aguievents.ActivityDeltaEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"messageId":    e.MessageID,
			"activityType": e.ActivityType,
			"patch":        e.Patch,
		}
	case *aguievents.CustomEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"name":  e.Name,
			"value": e.Value,
		}
	case *aguievents.RawEvent:
		activity.ID = e.ID()
		activity.ActivityType = string(e.Type())
		activity.Content = map[string]any{
			"source": e.Source,
			"event":  e.Event,
		}
	default:
		return nil
	}
	r.messages = append(r.messages, activity)
	return nil
}
