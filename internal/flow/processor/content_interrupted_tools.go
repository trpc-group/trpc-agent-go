//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"bytes"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type interruptedToolRound struct {
	source  *model.ProviderData
	display []model.ToolCall
	native  []model.ToolCall
}

type interruptedToolRounds map[string]interruptedToolRound

// Bind native call IDs on the detached session snapshot before matching results
// or selecting summary boundaries. Preserve the stored display for projection.
func (p *ContentRequestProcessor) bindInterruptedToolRounds(
	inv *agent.Invocation, filter string, events []event.Event, projector model.ProviderDataProjector,
) interruptedToolRounds {
	if projector == nil {
		return nil
	}
	latestUserIndex, userInvocations := p.visibleUserTurns(inv, filter, events)
	var latestUserInvocation string
	switch {
	case inv.Message.Role == model.RoleUser && model.HasPayload(inv.Message) && !userInvocations[inv.InvocationID]:
		// The processor will insert this invocation's user input into history.
		latestUserInvocation = inv.InvocationID
		latestUserIndex = len(events)
	case latestUserIndex >= 0:
		latestUserInvocation = events[latestUserIndex].InvocationID
	default:
		return nil
	}
	rounds := make(interruptedToolRounds)
	seenNativeReply := make(map[string]bool)
	for i := len(events) - 1; i >= 0; i-- {
		evt := &events[i]
		if !p.canMatchToolRound(*evt, inv, filter) {
			continue
		}
		if evt.ID == "" || evt.InvocationID == "" || evt.InvocationID == inv.InvocationID ||
			evt.InvocationID == latestUserInvocation || seenNativeReply[evt.InvocationID] || len(evt.Choices) != 1 {
			continue
		}
		// A call can arrive after a newer user message. Its own user turn
		// must be visible to identify it as an earlier invocation in that case.
		if i > latestUserIndex && !userInvocations[evt.InvocationID] {
			continue
		}
		// A Responses completion has one assistant choice holding its entire output.
		msg := &evt.Choices[0].Message
		if msg.ProviderData == nil || msg.ProviderData.Protocol != "openai.responses" {
			continue
		}
		seenNativeReply[evt.InvocationID] = true
		native, err := projector.ProjectProviderData(msg.ProviderData)
		if err != nil || len(native.ToolCalls) == 0 {
			continue
		}
		rounds[evt.ID] = interruptedToolRound{source: msg.ProviderData, display: msg.ToolCalls, native: native.ToolCalls}
		msg.ToolCalls = native.ToolCalls
	}
	return rounds
}

func (p *ContentRequestProcessor) visibleUserTurns(
	inv *agent.Invocation, filter string, events []event.Event,
) (int, map[string]bool) {
	last := -1
	seen := make(map[string]bool)
	for i := range events {
		evt := &events[i]
		if isUserTurnEvent(evt) && p.canMatchToolRound(*evt, inv, filter) {
			seen[evt.InvocationID] = true
			last = i
		}
	}
	return last, seen
}

func (rounds interruptedToolRounds) restoreDisplayCalls(events []event.Event) {
	if len(rounds) == 0 {
		return
	}
	for i := range events {
		evt := &events[i]
		if round, ok := rounds[evt.ID]; ok && eventHasProviderData(*evt) {
			evt.Choices[0].Message.ToolCalls = round.display
		}
	}
}

// A window cutting through an old tool round must drop results whose native
// call was dropped, even if its display IDs were rewritten.
func (rounds interruptedToolRounds) windowStart(history projectedHistory, limit int) int {
	messages := history.messages
	if len(rounds) > 0 && len(messages) > limit && messages[len(messages)-limit].Role == model.RoleTool {
		messages = append([]model.Message(nil), messages...)
		for i, item := range history.items {
			if round, ok := rounds[item.EffectiveEvent.ID]; ok && item.Message.ProviderData != nil {
				messages[i].ToolCalls = round.native
			}
		}
	}
	return maxHistoryRunsStartIndex(messages, limit)
}

func isUserTurnEvent(evt *event.Event) bool {
	return evt.InvocationID != "" && evt.Author == "user" && evt.IsUserMessage()
}

// Add missing outputs only to the selected request history, after windowing and
// compaction. They must neither evict a real message nor be reported as
// compacted successful results.
func (history projectedHistory) withInterruptedToolResults(
	rounds interruptedToolRounds, projector model.ProviderDataProjector,
) projectedHistory {
	if len(rounds) == 0 {
		return history
	}
	var out projectedHistory
	start := 0
	for i, item := range history.items {
		data := item.Message.ProviderData
		round, ok := rounds[item.EffectiveEvent.ID]
		if !ok || data == nil || data.Protocol != "openai.responses" {
			continue
		}
		next := i + 1
		for next < len(history.items) && history.items[next].Message.Role == model.RoleTool {
			next++
		}
		calls := round.native
		if data != round.source && !bytes.Equal(data.Payload, round.source.Payload) {
			// The application's event projector may replace the native payload.
			projected, err := projector.ProjectProviderData(data)
			if err != nil {
				continue
			}
			calls = projected.ToolCalls
		}
		missing := missingResponsesToolResults(calls, history.messages[i+1:next])
		if len(missing) == 0 {
			continue
		}
		out.messages = append(out.messages, history.messages[start:next]...)
		out.items = append(out.items, history.items[start:next]...)
		// No stored event or summary cutoff corresponds to these outputs.
		appendProjectedHistory(&out, event.Event{}, summaryview.Boundary{}, missing, false)
		start = next
	}
	if start == 0 {
		return history
	}
	out.messages = append(out.messages, history.messages[start:]...)
	out.items = append(out.items, history.items[start:]...)
	return out
}

func missingResponsesToolResults(calls []model.ToolCall, results []model.Message) []model.Message {
	reported := make(map[string]bool, len(results))
	for _, result := range results {
		reported[result.ToolID] = true
	}
	var missing []model.Message
	for _, call := range calls {
		if !reported[call.ID] {
			missing = append(missing, model.NewToolMessage(call.ID, call.Function.Name,
				"No result is available for this tool call. Its execution status is unknown."))
		}
	}
	return missing
}
