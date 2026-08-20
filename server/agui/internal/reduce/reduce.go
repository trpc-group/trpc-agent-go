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
	appName                   string
	userID                    string
	includeRunLifecycleEvents bool
	texts                     map[string]*textState
	reasonings                map[string]*reasoningState
	lastReasoningChunkID      string
	toolCalls                 map[string]*toolCallState
	messages                  []*aguievents.Message
}

// textPhase is the phase of the text message.
type textPhase int

const (
	textNotStarted textPhase = iota
	textReceiving
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
	started bool
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
	messageID    string
	name         string
	content      strings.Builder
	phase        toolPhase
	index        int
	messageIndex int
}

// Reduce reduces the AG-UI track events into message snapshots.
// In order to fetch the history messages as much as possible, still return the messages even if there is an error.
func Reduce(appName, userID string, events []session.TrackEvent, opt ...Option) ([]aguievents.Message, error) {
	opts := options{}
	for _, o := range opt {
		o(&opts)
	}
	r := new(appName, userID, opts)
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
		sanitized := r.sanitizeSnapshotMessage(message)
		if sanitized == nil {
			continue
		}
		messages = append(messages, *sanitized)
	}
	// In order to fetch the history messages as much as possible, still return the messages even if there is an error.
	return messages, err
}

// new creates a new reducer.
func new(appName, userID string, opts options) *reducer {
	return &reducer{
		appName:                   appName,
		userID:                    userID,
		includeRunLifecycleEvents: opts.includeRunLifecycleEvents,
		texts:                     make(map[string]*textState),
		reasonings:                make(map[string]*reasoningState),
		toolCalls:                 make(map[string]*toolCallState),
		messages:                  make([]*aguievents.Message, 0),
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
	case *aguievents.RunStartedEvent:
		err := r.handleRunStarted(e)
		r.finishRun()
		return err
	case *aguievents.RunFinishedEvent:
		err := r.handleRunFinished(e)
		r.finishRun()
		return err
	case *aguievents.RunErrorEvent:
		err := r.handleRunError(e)
		r.finishRun()
		return err
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

func (r *reducer) finishRun() {
	r.finalizePartial()
	r.texts = make(map[string]*textState)
	r.reasonings = make(map[string]*reasoningState)
	r.lastReasoningChunkID = ""
	pendingToolCalls := make(map[string]*toolCallState)
	for id, state := range r.toolCalls {
		if state.phase == toolAwaitingResult {
			pendingToolCalls[id] = state
		}
	}
	r.toolCalls = pendingToolCalls
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
		parent, ok := r.toolCallParent(state)
		if !ok {
			continue
		}
		if state.index < 0 || state.index >= len(parent.ToolCalls) {
			continue
		}
		parent.ToolCalls[state.index].Function.Arguments = strings.Clone(state.content.String())
	}
}

func (r *reducer) sanitizeSnapshotMessage(message *aguievents.Message) *aguievents.Message {
	if message == nil {
		return nil
	}
	if message.Role != types.RoleReasoning {
		return message
	}
	if _, ok := message.ContentString(); ok {
		return message
	}
	state, ok := r.reasonings[message.ID]
	if message.EncryptedValue == "" && (!ok || state.phase != reasoningReceiving || !state.started) {
		return nil
	}
	cloned := *message
	empty := ""
	cloned.Content = &empty
	return &cloned
}

func (r *reducer) handleRunStarted(e *aguievents.RunStartedEvent) error {
	if !r.includeRunLifecycleEvents {
		return nil
	}
	content := map[string]any{
		"threadId": e.ThreadID(),
		"runId":    e.RunID(),
	}
	r.appendRunActivity(e, content)
	return nil
}

func (r *reducer) handleRunFinished(e *aguievents.RunFinishedEvent) error {
	if !r.includeRunLifecycleEvents {
		return nil
	}
	content := map[string]any{
		"threadId": e.ThreadID(),
		"runId":    e.RunID(),
	}
	if e.Result != nil {
		content["result"] = e.Result
	}
	r.appendRunActivity(e, content)
	return nil
}

func (r *reducer) handleRunError(e *aguievents.RunErrorEvent) error {
	if !r.includeRunLifecycleEvents {
		return nil
	}
	content := map[string]any{
		"message": e.Message,
	}
	if e.RunID() != "" {
		content["runId"] = e.RunID()
	}
	if e.Code != nil {
		content["code"] = *e.Code
	}
	r.appendRunActivity(e, content)
	return nil
}

func (r *reducer) appendRunActivity(e aguievents.Event, content map[string]any) {
	r.messages = append(r.messages, &aguievents.Message{
		ID:           e.GetBaseEvent().ID(),
		Role:         types.RoleActivity,
		ActivityType: string(e.Type()),
		Content:      content,
	})
}

// handleTextStart handles the text message start event.
func (r *reducer) handleTextStart(e *aguievents.TextMessageStartEvent) error {
	if e.MessageID == "" {
		return fmt.Errorf("text message start missing id")
	}
	role := string(model.RoleAssistant)
	if e.Role != nil && *e.Role != "" {
		role = string(*e.Role)
	}
	role, name, err := r.textIdentity(role)
	if err != nil {
		return err
	}
	if state, exists := r.texts[e.MessageID]; exists {
		if state.phase == textReceiving {
			return fmt.Errorf("duplicate text message start: %s", e.MessageID)
		}
		if err := validateTextIdentity(state, e.MessageID, role, name); err != nil {
			return err
		}
		state.phase = textReceiving
		return nil
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
	if !ok || state.phase == textNotStarted {
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
	if !ok || state.phase == textNotStarted {
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
	messageID := *e.MessageID
	if state, exists := r.texts[messageID]; exists && state.phase == textReceiving {
		return fmt.Errorf("duplicate text message chunk: %s", *e.MessageID)
	}
	role := string(model.RoleAssistant)
	if e.Role != nil && *e.Role != "" {
		role = string(*e.Role)
	}
	role, name, err := r.textIdentity(role)
	if err != nil {
		return err
	}
	content := ""
	if e.Delta != nil {
		content = strings.Clone(*e.Delta)
	}
	if state, exists := r.texts[messageID]; exists {
		if err := validateTextIdentity(state, messageID, role, name); err != nil {
			return err
		}
		state.content.WriteString(content)
		state.phase = textEnded
		text := strings.Clone(state.content.String())
		r.messages[state.index].Content = &text
		return nil
	}
	r.messages = append(r.messages, &aguievents.Message{
		ID:      messageID,
		Role:    types.Role(role),
		Name:    name,
		Content: &content,
	})
	state := &textState{
		role:  role,
		name:  name,
		phase: textEnded,
		index: len(r.messages) - 1,
	}
	state.content.WriteString(content)
	r.texts[messageID] = state
	return nil
}

func (r *reducer) textIdentity(role string) (string, string, error) {
	switch role {
	case string(model.RoleUser):
		return role, r.userID, nil
	case string(model.RoleAssistant):
		return role, r.appName, nil
	default:
		return "", "", fmt.Errorf("unsupported role: %s", role)
	}
}

func validateTextIdentity(state *textState, messageID, role, name string) error {
	if state.role == role && state.name == name {
		return nil
	}
	return fmt.Errorf("text message identity mismatch: %s", messageID)
}

func (r *reducer) handleReasoningMessageStart(e *aguievents.ReasoningMessageStartEvent) error {
	if e.MessageID == "" {
		return fmt.Errorf("reasoning message start missing id")
	}
	role, name, err := r.reasoningIdentity(e.Role)
	if err != nil {
		return err
	}
	if state, exists := r.reasonings[e.MessageID]; exists {
		if state.phase == reasoningReceiving {
			return fmt.Errorf("duplicate reasoning message start: %s", e.MessageID)
		}
		if err := validateReasoningIdentity(state, e.MessageID, role, name); err != nil {
			return err
		}
		state.phase = reasoningReceiving
		state.started = true
		return nil
	}
	msg := &aguievents.Message{
		ID:   e.MessageID,
		Role: types.RoleReasoning,
		Name: name,
	}
	r.messages = append(r.messages, msg)
	r.reasonings[e.MessageID] = &reasoningState{
		role:    role,
		name:    name,
		phase:   reasoningReceiving,
		index:   len(r.messages) - 1,
		started: true,
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
		if e.Delta == nil {
			return nil
		}
		if state.phase == reasoningEnded {
			if *e.Delta == "" {
				return fmt.Errorf("duplicate reasoning message end: %s", messageID)
			}
			state.phase = reasoningReceiving
		}
		if state.phase != reasoningReceiving {
			return fmt.Errorf("reasoning message chunk after end: %s", messageID)
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
		role:  string(types.RoleReasoning),
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

func (r *reducer) reasoningIdentity(role string) (string, string, error) {
	switch role {
	case "", string(types.RoleReasoning), string(model.RoleAssistant):
		return string(types.RoleReasoning), r.appName, nil
	default:
		return "", "", fmt.Errorf("unsupported role: %s", role)
	}
}

func validateReasoningIdentity(state *reasoningState, messageID, role, name string) error {
	if state.role == role && state.name == name {
		return nil
	}
	return fmt.Errorf("reasoning message identity mismatch: %s", messageID)
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
			role:  string(types.RoleReasoning),
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
			phase: textNotStarted,
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
		messageID:    *e.ParentMessageID,
		name:         e.ToolCallName,
		phase:        toolAwaitingArgs,
		index:        len(r.messages[parentState.index].ToolCalls) - 1,
		messageIndex: parentState.index,
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
	parent, ok := r.toolCallParent(state)
	if !ok {
		return fmt.Errorf("tool call end missing parent message: %s", state.messageID)
	}
	if state.index < 0 || state.index >= len(parent.ToolCalls) {
		return fmt.Errorf("tool call end missing parent tool call: %s", e.ToolCallID)
	}
	parent.ToolCalls[state.index].Function.Arguments = strings.Clone(state.content.String())
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
	parent, ok := r.toolCallParent(state)
	if !ok {
		return fmt.Errorf("tool call result missing parent message: %s", state.messageID)
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
	r.insertMessage(r.toolResultInsertIndex(state.messageIndex, parent, e.ToolCallID), msg)
	state.phase = toolCompleted
	return nil
}

func (r *reducer) toolCallParent(state *toolCallState) (*aguievents.Message, bool) {
	if state.messageIndex < 0 || state.messageIndex >= len(r.messages) {
		return nil, false
	}
	parent := r.messages[state.messageIndex]
	if parent == nil || parent.ID != state.messageID {
		return nil, false
	}
	return parent, true
}

func (r *reducer) toolResultInsertIndex(parentIndex int, parent *aguievents.Message, toolCallID string) int {
	toolCallOrder := make(map[string]int, len(parent.ToolCalls))
	targetOrder := -1
	for i, call := range parent.ToolCalls {
		toolCallOrder[call.ID] = i
		if call.ID == toolCallID {
			targetOrder = i
		}
	}
	if targetOrder < 0 {
		return parentIndex + 1
	}
	index := parentIndex + 1
	for index < len(r.messages) {
		message := r.messages[index]
		if message == nil || message.Role != types.RoleTool || message.ToolCallID == "" {
			break
		}
		order, ok := toolCallOrder[message.ToolCallID]
		if !ok || order >= targetOrder {
			break
		}
		index++
	}
	return index
}

func (r *reducer) insertMessage(index int, message *aguievents.Message) {
	if index < 0 || index >= len(r.messages) {
		r.messages = append(r.messages, message)
		return
	}
	r.messages = append(r.messages, nil)
	copy(r.messages[index+1:], r.messages[index:])
	r.messages[index] = message
	r.shiftMessageIndexes(index)
}

func (r *reducer) shiftMessageIndexes(insertIndex int) {
	for _, state := range r.texts {
		if state.index >= insertIndex {
			state.index++
		}
	}
	for _, state := range r.reasonings {
		if state.index >= insertIndex {
			state.index++
		}
	}
	for _, state := range r.toolCalls {
		if state.messageIndex >= insertIndex {
			state.messageIndex++
		}
	}
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
