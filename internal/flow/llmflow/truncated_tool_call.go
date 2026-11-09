//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type truncatedOutputSnapshot struct {
	calls []truncatedCall
	texts []truncatedText
}

type truncatedCall struct {
	choiceIndex         int
	id, name, arguments string
}

type truncatedText struct {
	choiceIndex int
	value       string
}

// Snapshot only executable material from completed, output-limited choices.
// Callback code can mutate the response and its argument bytes in place.
func snapshotTruncatedOutput(response *model.Response) truncatedOutputSnapshot {
	var snapshot truncatedOutputSnapshot
	if response == nil || response.IsPartial {
		return snapshot
	}
	for i := range response.Choices {
		choice := &response.Choices[i]
		if !choice.HasOutputLimitFinishReason() {
			continue
		}
		for _, message := range [...]model.Message{choice.Message, choice.Delta} {
			for _, call := range message.ToolCalls {
				snapshot.calls = append(snapshot.calls, truncatedCall{
					choiceIndex: choice.Index,
					id:          call.ID,
					name:        call.Function.Name,
					arguments:   string(call.Function.Arguments),
				})
			}
		}
		if choiceHasToolCalls(*choice) {
			continue
		}
		if text, ok := repairableMessageText(&choice.Message); ok && strings.Contains(text, textToolCallOpenTag) {
			snapshot.texts = append(snapshot.texts, truncatedText{choiceIndex: choice.Index, value: text})
		}
	}
	return snapshot
}

func (s truncatedOutputSnapshot) retainedIn(choice model.Choice) bool {
	if len(s.calls) == 0 && len(s.texts) == 0 {
		return false
	}
	for _, message := range [...]model.Message{choice.Message, choice.Delta} {
		for _, call := range message.ToolCalls {
			for _, before := range s.calls {
				if call.ID != "" && before.id != "" {
					if call.ID == before.id {
						return true
					}
					continue
				}
				if before.choiceIndex == choice.Index && before.name == call.Function.Name &&
					before.arguments == string(call.Function.Arguments) {
					return true
				}
			}
		}
	}
	for _, before := range s.texts {
		if before.choiceIndex == choice.Index {
			if text, ok := repairableMessageText(&choice.Message); ok && text == before.value {
				return true
			}
		}
	}
	return false
}

func (s truncatedOutputSnapshot) truncatedChoices(response *model.Response) []bool {
	if response == nil || response.IsPartial {
		return nil
	}
	var truncated []bool
	for i, choice := range response.Choices {
		if !choice.HasOutputLimitFinishReason() && !s.retainedIn(choice) {
			continue
		}
		if truncated == nil {
			truncated = make([]bool, len(response.Choices))
		}
		truncated[i] = true
	}
	return truncated
}

func choiceHasToolCalls(choice model.Choice) bool {
	return len(choice.Message.ToolCalls) != 0 || len(choice.Delta.ToolCalls) != 0
}

func (p *streamingResponseProcessor) prepareToolCalls(
	eventInvocation *agent.Invocation,
	response *model.Response,
	snapshot truncatedOutputSnapshot,
) bool {
	invocation := p.rejectingInvocation()
	var truncated []bool
	if invocation != nil {
		// Capture reasons before text repair changes them to "tool_calls".
		truncated = snapshot.truncatedChoices(response)
	}
	rejectSelected := len(truncated) > 0 && truncated[0]
	if rejectSelected && response.IsToolCallResponse() {
		p.rejectTruncatedToolCall(eventInvocation, invocation, response)
		return false
	}
	p.repairToolCallArguments(response)
	p.repairToolCallTextAndStats(response)
	if rejectSelected && response.IsToolCallResponse() {
		p.rejectTruncatedToolCall(eventInvocation, invocation, response)
		return false
	}
	if len(truncated) > 1 {
		// Only the first choice is executed. Do not persist incomplete calls
		// from alternatives that can never receive a corresponding tool result.
		choices := response.Choices
		kept := choices[:1]
		for i := 1; i < len(choices); i++ {
			if !truncated[i] || !choiceHasToolCalls(choices[i]) {
				kept = append(kept, choices[i])
			}
		}
		clear(choices[len(kept):])
		response.Choices = kept
	}
	return true
}
