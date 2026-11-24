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

// Request-only unknown outputs currently apply to Responses function calls.
// A matching protocol and model name alone do not define how another adapter
// represents a missing result.
const responsesHistoryProtocol = "openai.responses"

type projectNativeCalls func(*model.ProviderData) (model.Message, error)

func responsesHistoryProjector(m model.Model, req *model.Request) projectNativeCalls {
	projector, ok := m.(model.ProviderDataProjector)
	if !ok || m.Info().ProviderDataProtocol != responsesHistoryProtocol {
		return nil
	}
	if projector, ok := m.(model.ProviderDataRequestProjector); ok && req != nil {
		return func(data *model.ProviderData) (model.Message, error) {
			return projector.ProjectProviderDataWithRequest(req, data)
		}
	}
	return projector.ProjectProviderData
}

type nativeToolRoundBinding struct {
	source  *model.ProviderData
	display []model.ToolCall
	native  []model.ToolCall
}

type nativeToolRoundBindings map[string]nativeToolRoundBinding

// Bind the last native assistant reply of each superseded invocation if it
// contains function calls. The detached snapshot uses wire IDs for result
// matching and summary boundaries. Keep the stored display for projection.
func (p *ContentRequestProcessor) bindNativeToolRounds(
	inv *agent.Invocation, filter string, events []event.Event, project projectNativeCalls,
) nativeToolRoundBindings {
	if project == nil {
		return nil
	}
	// Only a Runner user turn can supersede a pending round. In particular,
	// an AgentTool's input is not evidence that its parent was interrupted.
	root := inv
	for root.GetParentInvocation() != nil {
		root = root.GetParentInvocation()
	}
	latestUserIndex, latestUserInvocation, userInvocations := p.visibleUserTurns(inv, filter, events)
	if latestUserInvocation == "" {
		return nil
	}
	rounds := make(nativeToolRoundBindings)
	seenNativeReply := make(map[string]bool)
	for i := len(events) - 1; i >= 0; i-- {
		evt := &events[i]
		if !p.canMatchToolRound(*evt, inv, filter) {
			continue
		}
		if evt.ID == "" || evt.InvocationID == "" || evt.ParentInvocationID != "" || evt.InvocationID == root.InvocationID ||
			evt.InvocationID == latestUserInvocation || seenNativeReply[evt.InvocationID] {
			continue
		}
		// A call can arrive after a newer user message. Its own user turn
		// must be visible to identify it as an earlier invocation in that case.
		if i > latestUserIndex && !userInvocations[evt.InvocationID] {
			continue
		}
		msg := responsesAssistantMessage(evt)
		if msg == nil {
			continue
		}
		seenNativeReply[evt.InvocationID] = true
		native, err := project(msg.ProviderData)
		if err != nil || len(native.ToolCalls) == 0 {
			continue
		}
		rounds[evt.ID] = nativeToolRoundBinding{source: msg.ProviderData, display: msg.ToolCalls, native: native.ToolCalls}
		msg.ToolCalls = native.ToolCalls
	}
	return rounds
}

// A completed Responses record is a single assistant choice. Native tool
// results can also carry provider data and must not hide the preceding call.
func responsesAssistantMessage(evt *event.Event) *model.Message {
	if evt.Response == nil || len(evt.Choices) != 1 {
		return nil
	}
	msg := &evt.Choices[0].Message
	if msg.Role != model.RoleAssistant || msg.ProviderData == nil || msg.ProviderData.Protocol != responsesHistoryProtocol {
		return nil
	}
	return msg
}

func (p *ContentRequestProcessor) visibleUserTurns(
	inv *agent.Invocation, filter string, events []event.Event,
) (int, string, map[string]bool) {
	last := -1
	seen := make(map[string]bool)
	for i := range events {
		evt := &events[i]
		if isUserTurnEvent(evt) && p.canMatchToolRound(*evt, inv, filter) {
			seen[evt.InvocationID] = true
			last = i
		}
	}
	// Runner may provide its new input before it appears in the snapshot.
	if inv.GetParentInvocation() == nil && inv.Message.Role == model.RoleUser && model.HasPayload(inv.Message) && !seen[inv.InvocationID] {
		return len(events), inv.InvocationID, seen
	}
	if last >= 0 {
		return last, events[last].InvocationID, seen
	}
	return -1, "", seen
}

func (rounds nativeToolRoundBindings) restoreDisplayCalls(events []event.Event) {
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

// The window and result matcher use the wire IDs. The items retain the
// projected display, which is restored before returning the request.
func (rounds nativeToolRoundBindings) useNativeCalls(history projectedHistory, project projectNativeCalls) {
	if len(rounds) == 0 || len(history.messages) != len(history.items) {
		return
	}
	for i, item := range history.items {
		id := item.EffectiveEvent.ID
		round, ok := rounds[id]
		if !ok {
			continue
		}
		data := item.Message.ProviderData
		if data == nil || data.Protocol != round.source.Protocol {
			delete(rounds, id)
			continue
		}
		calls := round.native
		if data != round.source && (data.Model != round.source.Model || !bytes.Equal(data.Payload, round.source.Payload)) {
			msg, err := project(data)
			if err != nil {
				delete(rounds, id)
				continue
			}
			calls = msg.ToolCalls
		}
		history.messages[i].ToolCalls = calls
	}
}

func isUserTurnEvent(evt *event.Event) bool {
	return evt.ParentInvocationID == "" && evt.InvocationID != "" && evt.Author == "user" && evt.IsUserMessage()
}

// A result persisted after the next user turn would otherwise be downgraded
// to an orphan by the tool-history sanitizer. Move just that result in the
// request. Normal native rounds retain their original event order.
func (history projectedHistory) withLateToolResults(rounds nativeToolRoundBindings) projectedHistory {
	if len(rounds) == 0 || len(history.items) != len(history.messages) {
		return history
	}
	callSites := uniqueToolCallPositions(history.messages)
	insertAfter := make(map[int][]int)
	moved := make(map[int]bool)
	lastUser := -1
	for i := range history.items {
		item := &history.items[i]
		if isUserTurnEvent(&item.EffectiveEvent) {
			lastUser = i
		}
		msg := item.Message
		if msg.Role != model.RoleTool || msg.ProviderData != nil {
			continue
		}
		call, ok := callSites[msg.ToolID]
		if !ok || call < 0 {
			continue
		}
		delete(callSites, msg.ToolID)
		owner := history.items[call].EffectiveEvent
		if _, superseded := rounds[owner.ID]; !superseded || lastUser <= call || history.items[lastUser].EffectiveEvent.InvocationID == owner.InvocationID {
			continue
		}
		end := call
		for end+1 < len(history.items) && history.messages[end+1].Role == model.RoleTool {
			end++
		}
		insertAfter[end] = append(insertAfter[end], i)
		moved[i] = true
	}
	if len(moved) == 0 {
		return history
	}
	var ordered projectedHistory
	for i, item := range history.items {
		if !moved[i] {
			ordered.messages = append(ordered.messages, history.messages[i])
			ordered.items = append(ordered.items, item)
		}
		for _, index := range insertAfter[i] {
			result := history.items[index]
			// A moved result cannot advance the summary past intervening turns.
			result.Boundary = summaryview.Boundary{}
			ordered.messages = append(ordered.messages, result.Message)
			ordered.items = append(ordered.items, result)
		}
	}
	return ordered
}

func uniqueToolCallPositions(messages []model.Message) map[string]int {
	// The wire call ID also identifies results persisted by a later resume
	// invocation. Ambiguous IDs stay subject to native history validation.
	callSites := make(map[string]int)
	for i, msg := range messages {
		for _, call := range msg.ToolCalls {
			if _, exists := callSites[call.ID]; exists {
				callSites[call.ID] = -1
			} else {
				callSites[call.ID] = i
			}
		}
	}
	return callSites
}

// Add missing outputs only to the selected request history, after windowing and
// compaction. They must neither evict a real message nor be reported as
// compacted successful results.
func (history projectedHistory) withInterruptedToolResults(
	rounds nativeToolRoundBindings,
) projectedHistory {
	if len(rounds) == 0 || len(history.items) != len(history.messages) {
		return history
	}
	// Native histories can contain nested messages between a call and its
	// output. An existing output anywhere after the call answers it, even
	// when the content is empty.
	results := make(map[string]int)
	for i, msg := range history.messages {
		if msg.Role == model.RoleTool && msg.ProviderData == nil {
			results[msg.ToolID] = i
		}
	}
	var out projectedHistory
	start := 0
	for i, item := range history.items {
		if _, ok := rounds[item.EffectiveEvent.ID]; !ok {
			continue
		}
		next := i + 1
		for next < len(history.items) && history.items[next].Message.Role == model.RoleTool {
			next++
		}
		missing := missingResponsesToolResults(history.messages[i].ToolCalls, i, results)
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

func missingResponsesToolResults(calls []model.ToolCall, callIndex int, results map[string]int) []model.Message {
	const missingResult = "No result is available for this tool call. " +
		"The previous run may have been interrupted before the result was recorded. " +
		"The tool may already have run, and its outcome is unknown."
	var missing []model.Message
	for _, call := range calls {
		if index, ok := results[call.ID]; !ok || index <= callIndex {
			missing = append(missing, model.NewToolMessage(call.ID, call.Function.Name, missingResult))
		}
	}
	return missing
}
