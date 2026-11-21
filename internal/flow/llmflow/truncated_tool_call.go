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
	calls      []truncatedCall
	texts      []truncatedText
	traceUsage *model.Usage
}

type truncatedCall struct {
	choiceIndex                       int
	id, name, arguments, finishReason string
}

type truncatedText struct {
	choiceIndex         int
	value, finishReason string
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
		reason := *choice.FinishReason
		for _, message := range [...]model.Message{choice.Message, choice.Delta} {
			for _, call := range message.ToolCalls {
				snapshot.calls = append(snapshot.calls, truncatedCall{
					choiceIndex:  choice.Index,
					id:           call.ID,
					name:         call.Function.Name,
					arguments:    string(call.Function.Arguments),
					finishReason: reason,
				})
			}
		}
		if choiceHasToolCalls(*choice) {
			continue
		}
		if text, ok := repairableMessageText(&choice.Message); ok && strings.Contains(text, textToolCallOpenTag) {
			snapshot.texts = append(snapshot.texts, truncatedText{choiceIndex: choice.Index, value: text, finishReason: reason})
		}
	}
	return snapshot
}

func (s truncatedOutputSnapshot) retainedFinishReason(choice model.Choice) string {
	if len(s.calls) == 0 && len(s.texts) == 0 {
		return ""
	}
	for _, message := range [...]model.Message{choice.Message, choice.Delta} {
		for _, call := range message.ToolCalls {
			for _, before := range s.calls {
				if call.ID != "" && before.id != "" {
					if call.ID == before.id {
						return before.finishReason
					}
					continue
				}
				if before.choiceIndex == choice.Index && before.name == call.Function.Name &&
					before.arguments == string(call.Function.Arguments) {
					return before.finishReason
				}
			}
		}
	}
	for _, before := range s.texts {
		if before.choiceIndex == choice.Index {
			if text, ok := repairableMessageText(&choice.Message); ok && text == before.value {
				return before.finishReason
			}
		}
	}
	return ""
}

func (s truncatedOutputSnapshot) truncatedChoiceReasons(response *model.Response) []string {
	if response == nil || response.IsPartial {
		return nil
	}
	var truncated []string
	for i, choice := range response.Choices {
		var reason string
		if choice.HasOutputLimitFinishReason() {
			reason = *choice.FinishReason
		} else {
			reason = s.retainedFinishReason(choice)
		}
		if reason == "" {
			continue
		}
		if truncated == nil {
			truncated = make([]string, len(response.Choices))
		}
		truncated[i] = reason
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
	var truncated []string
	if invocation != nil {
		// Capture reasons before text repair changes them to "tool_calls".
		truncated = snapshot.truncatedChoiceReasons(response)
	}
	rejectSelected := len(truncated) > 0 && truncated[0] != ""
	if rejectSelected && response.IsToolCallResponse() {
		p.rejectTruncatedToolCall(eventInvocation, invocation, response, truncated[0], snapshot.traceUsage)
		return false
	}
	p.repairToolCallArguments(response)
	p.repairToolCallTextAndStats(response)
	if rejectSelected && response.IsToolCallResponse() {
		p.rejectTruncatedToolCall(eventInvocation, invocation, response, truncated[0], snapshot.traceUsage)
		return false
	}
	if len(truncated) > 1 {
		// Only the first choice is executed. Do not persist incomplete calls
		// from alternatives that can never receive a corresponding tool result.
		choices := response.Choices
		kept := choices[:1]
		for i := 1; i < len(choices); i++ {
			if truncated[i] == "" || !choiceHasToolCalls(choices[i]) {
				kept = append(kept, choices[i])
			}
		}
		clear(choices[len(kept):])
		response.Choices = kept
	}
	return true
}
