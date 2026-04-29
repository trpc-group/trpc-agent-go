//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

import (
	"slices"
	"strings"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Tool-call delta streaming follows four rules.
// 1. Delta chunks build state and may emit START or ARGS.
// 2. Arguments received before START are buffered.
// 3. Final messages close a started delta stream and may fill a missing suffix.
// 4. Unindexed chunks are skipped when attaching them would require guessing.
// toolCallDeltaKey identifies one streamed tool call inside one assistant message.
type toolCallDeltaKey struct {
	parentMessageID string
	choiceIndex     int
	toolIndex       int
}

// toolCallDeltaState keeps state because provider chunks may omit fields.
type toolCallDeltaState struct {
	key               toolCallDeltaKey
	id                string
	name              string
	parentMessageID   string
	choiceIndex       int
	arguments         string
	bufferedArguments []string
	started           bool
	ended             bool
}

func (t *translator) messageToolCallEvents(parentMessageID string, choice model.Choice) []aguievents.Event {
	events := make([]aguievents.Event, 0, len(choice.Message.ToolCalls))
	for position, toolCall := range choice.Message.ToolCalls {
		if t.toolCallDeltaStreamingEnabled {
			if state := t.deltaToolCallForFinalMessage(parentMessageID, choice.Index, position, toolCall); state != nil {
				if state.started {
					events = append(events, t.finishDeltaToolCallWithFinalMessage(state, toolCall)...)
					continue
				}
				t.discardDeltaToolCall(state)
			}
		}
		t.recordToolCallID(toolCall.ID)
		// Tool Call Start Event.
		startOpt := []aguievents.ToolCallStartOption{aguievents.WithParentMessageID(parentMessageID)}
		events = append(events, aguievents.NewToolCallStartEvent(toolCall.ID, toolCall.Function.Name, startOpt...))
		// Tool Call Arguments Event.
		toolCallArguments := formatToolCallArguments(toolCall.Function.Arguments)
		if toolCallArguments != "" {
			events = append(events, aguievents.NewToolCallArgsEvent(toolCall.ID, toolCallArguments))
		}
		// Tool call end should precede result to align with AG-UI protocol.
		events = append(events, aguievents.NewToolCallEndEvent(toolCall.ID))
	}
	return events
}

func (t *translator) deltaToolCallEvents(parentMessageID string, choice model.Choice) []aguievents.Event {
	events := make([]aguievents.Event, 0, len(choice.Delta.ToolCalls))
	for position, toolCall := range choice.Delta.ToolCalls {
		events = append(events, t.handleToolCallDelta(parentMessageID, choice.Index, position,
			len(choice.Delta.ToolCalls), toolCall)...)
	}
	return events
}

func (t *translator) handleToolCallDelta(
	parentMessageID string,
	choiceIndex int,
	position int,
	deltaToolCallCount int,
	toolCall model.ToolCall,
) []aguievents.Event {
	state := t.lookupOrCreateDeltaToolCall(parentMessageID, choiceIndex, position, deltaToolCallCount, toolCall)
	if state == nil {
		return nil
	}
	t.updateToolCallDeltaState(state, toolCall)
	arguments := formatToolCallArguments(toolCall.Function.Arguments)
	if !state.started {
		if arguments != "" {
			state.bufferedArguments = append(state.bufferedArguments, arguments)
		}
		if state.id == "" || state.name == "" {
			return nil
		}
		return t.startDeltaToolCall(state)
	}
	if arguments == "" {
		return nil
	}
	return []aguievents.Event{t.newToolCallDeltaArgsEvent(state, arguments)}
}

// lookupOrCreateDeltaToolCall maps one provider delta to state without unsafe guessing.
func (t *translator) lookupOrCreateDeltaToolCall(
	parentMessageID string,
	choiceIndex int,
	position int,
	deltaToolCallCount int,
	toolCall model.ToolCall,
) *toolCallDeltaState {
	if toolCall.Index == nil && toolCall.ID != "" {
		// An explicit tool-call ID is safer than falling back to array position.
		if state := t.openDeltaToolCallByID(toolCall.ID); state != nil {
			return state
		}
	}
	usesPositionFallback := toolCall.Index == nil
	singleToolCallDelta := deltaToolCallCount == 1
	// Position fallback is only safe before any tool call is open for the choice.
	if usesPositionFallback && singleToolCallDelta && t.hasOpenDeltaToolCall(parentMessageID, choiceIndex) {
		return nil
	}
	key := toolCallDeltaKeyFor(parentMessageID, choiceIndex, position, toolCall)
	if t.toolCallDeltas == nil {
		t.toolCallDeltas = make(map[toolCallDeltaKey]*toolCallDeltaState)
	}
	state := t.toolCallDeltas[key]
	if state == nil {
		state = &toolCallDeltaState{
			key:             key,
			parentMessageID: parentMessageID,
			choiceIndex:     choiceIndex,
		}
		t.toolCallDeltas[key] = state
	}
	return state
}

func (t *translator) updateToolCallDeltaState(state *toolCallDeltaState, toolCall model.ToolCall) {
	if !state.started && toolCall.ID != "" {
		if state.id != "" && state.id != toolCall.ID {
			delete(t.toolCallDeltasByID, state.id)
		}
		state.id = toolCall.ID
		if t.toolCallDeltasByID == nil {
			t.toolCallDeltasByID = make(map[string]*toolCallDeltaState)
		}
		t.toolCallDeltasByID[state.id] = state
	}
	if toolCall.Function.Name != "" {
		state.name = toolCall.Function.Name
	}
}

func (t *translator) openDeltaToolCallByID(toolCallID string) *toolCallDeltaState {
	state := t.toolCallDeltasByID[toolCallID]
	if state == nil || state.ended {
		return nil
	}
	return state
}

func (t *translator) hasOpenDeltaToolCall(
	parentMessageID string,
	choiceIndex int,
) bool {
	for _, state := range t.toolCallDeltas {
		if state.parentMessageID == parentMessageID && state.choiceIndex == choiceIndex && !state.ended {
			return true
		}
	}
	return false
}

func (t *translator) startDeltaToolCall(state *toolCallDeltaState) []aguievents.Event {
	startOpt := []aguievents.ToolCallStartOption{aguievents.WithParentMessageID(state.parentMessageID)}
	events := []aguievents.Event{aguievents.NewToolCallStartEvent(state.id, state.name, startOpt...)}
	state.started = true
	t.recordToolCallID(state.id)
	for _, bufferedArgument := range state.bufferedArguments {
		events = append(events, t.newToolCallDeltaArgsEvent(state, bufferedArgument))
	}
	state.bufferedArguments = nil
	return events
}

func (t *translator) newToolCallDeltaArgsEvent(state *toolCallDeltaState, delta string) aguievents.Event {
	state.arguments += delta
	return aguievents.NewToolCallArgsEvent(state.id, delta)
}

func (t *translator) deltaToolCallForFinalMessage(
	parentMessageID string,
	choiceIndex int,
	position int,
	toolCall model.ToolCall,
) *toolCallDeltaState {
	key := toolCallDeltaKeyFor(parentMessageID, choiceIndex, position, toolCall)
	return t.toolCallDeltas[key]
}

func (t *translator) finishDeltaToolCallWithFinalMessage(
	state *toolCallDeltaState,
	toolCall model.ToolCall,
) []aguievents.Event {
	var events []aguievents.Event
	finalArguments := formatToolCallArguments(toolCall.Function.Arguments)
	if !state.ended && finalArguments != "" && strings.HasPrefix(finalArguments, state.arguments) {
		// The final message may contain the full arguments, so only the missing suffix is emitted.
		if suffix := finalArguments[len(state.arguments):]; suffix != "" {
			events = append(events, t.newToolCallDeltaArgsEvent(state, suffix))
		}
	}
	events = append(events, t.closeDeltaToolCall(state)...)
	return events
}

func (t *translator) closeOpenToolCallDeltas() []aguievents.Event {
	keys := make([]toolCallDeltaKey, 0, len(t.toolCallDeltas))
	for key := range t.toolCallDeltas {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, compareToolCallDeltaKeys)
	events := make([]aguievents.Event, 0, len(keys))
	for _, key := range keys {
		events = append(events, t.closeDeltaToolCall(t.toolCallDeltas[key])...)
	}
	return events
}

func (t *translator) closeDeltaToolCallForResult(toolCallID string) []aguievents.Event {
	return t.closeDeltaToolCall(t.toolCallDeltasByID[toolCallID])
}

func (t *translator) closeDeltaToolCall(state *toolCallDeltaState) []aguievents.Event {
	if state == nil || state.ended {
		return nil
	}
	if !state.started || state.id == "" {
		t.discardDeltaToolCall(state)
		return nil
	}
	t.discardDeltaToolCall(state)
	return []aguievents.Event{aguievents.NewToolCallEndEvent(state.id)}
}

func (t *translator) discardDeltaToolCall(state *toolCallDeltaState) {
	if state == nil {
		return
	}
	state.ended = true
	if state.id != "" {
		delete(t.toolCallDeltasByID, state.id)
	}
	delete(t.toolCallDeltas, state.key)
}

func toolCallDeltaIndex(toolCall model.ToolCall, fallback int) int {
	if toolCall.Index != nil {
		return *toolCall.Index
	}
	return fallback
}

func toolCallDeltaKeyFor(parentMessageID string, choiceIndex, position int, toolCall model.ToolCall) toolCallDeltaKey {
	return toolCallDeltaKey{
		parentMessageID: parentMessageID,
		choiceIndex:     choiceIndex,
		toolIndex:       toolCallDeltaIndex(toolCall, position),
	}
}

func compareToolCallDeltaKeys(a, b toolCallDeltaKey) int {
	if a.parentMessageID != b.parentMessageID {
		if a.parentMessageID < b.parentMessageID {
			return -1
		}
		return 1
	}
	if a.choiceIndex != b.choiceIndex {
		return a.choiceIndex - b.choiceIndex
	}
	return a.toolIndex - b.toolIndex
}
