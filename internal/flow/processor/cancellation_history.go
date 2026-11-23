//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const executionProjectionErrorKey = "__execution_projection_error__"

// completeTrackedToolRounds works only on the already filtered projection.
// Original events, summary cutoffs, branches and history windows remain intact.
func completeTrackedToolRounds(events []event.Event, inv *agent.Invocation) ([]event.Event, error) {
	records, err := tooljournal.Index(inv.Session)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return events, nil
	}
	matches := toolResponseMatchesByCallEvent(events)
	var out []event.Event
	for i, e := range events {
		out = append(out, e)
		record, ok := records[e.ID]
		if !ok || !e.IsToolCallResponse() {
			continue
		}
		present := make(map[string]bool)
		for _, match := range matches[i] {
			for _, choiceIndex := range match.choiceIndices {
				present[toolResponseIDFromChoice(events[match.eventIndex].Choices[choiceIndex])] = true
			}
		}
		facts := make(map[string]tooljournal.Call, len(record.Calls))
		for _, c := range record.Calls {
			facts[c.Tool.ID] = c
		}
		var choices []model.Choice
		for _, id := range e.GetToolCallIDs() {
			if present[id] {
				continue
			}
			if eventHasProviderData(e) {
				return nil, fmt.Errorf("incomplete native tool history: source %s requires provider-specific recovery", e.ID)
			}
			fact, ok := facts[id]
			if !ok {
				return nil, fmt.Errorf("tool execution evidence missing for call %q", id)
			}
			result, err := tooljournal.Outcome(fact)
			if err != nil {
				return nil, err
			}
			choices = append(choices, result...)
		}
		if len(choices) == 0 {
			continue
		}
		projected := event.NewResponseEvent(e.InvocationID, e.Author, &model.Response{Object: model.ObjectTypeToolResponse, Choices: choices})
		projected.ID = "execution-projection:" + record.ID
		projected.RequestID = e.RequestID
		projected.Timestamp = e.Timestamp
		projected.Branch = e.Branch
		projected.FilterKey = e.FilterKey
		out = append(out, *projected)
	}
	return out, nil
}

func executionProjectionFailed(inv *agent.Invocation, err error) {
	inv.EndInvocation = true
	inv.SetState(executionProjectionErrorKey, err.Error())
}
