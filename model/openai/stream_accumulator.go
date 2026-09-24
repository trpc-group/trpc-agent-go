//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"strings"

	openaigo "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/respjson"
)

// chatStreamAccumulator keeps the SDK accumulator's structural behavior while
// avoiding its quadratic string concatenation for streamed text fields.
type chatStreamAccumulator struct {
	acc openaigo.ChatCompletionAccumulator

	choices []*chatStreamChoiceAccumulator

	scratchChunk     openaigo.ChatCompletionChunk
	scratchChoices   []openaigo.ChatCompletionChunkChoice
	scratchToolCalls [][]openaigo.ChatCompletionChunkChoiceDeltaToolCall
}

type chatStreamChoiceAccumulator struct {
	content   strings.Builder
	refusal   strings.Builder
	toolCalls []*chatStreamToolCallAccumulator
}

type chatStreamToolCallAccumulator struct {
	name      strings.Builder
	arguments strings.Builder
}

func (a *chatStreamAccumulator) addChunk(chunk openaigo.ChatCompletionChunk) bool {
	sdkChunk := a.sdkChunk(chunk)
	for index, choice := range chunk.Choices {
		if choice.Delta.JSON.Content.Valid() {
			sdkChunk.Choices[index].Delta.JSON.Content = respjson.NewField("true")
		}
		if choice.Delta.JSON.Refusal.Valid() {
			sdkChunk.Choices[index].Delta.JSON.Refusal = respjson.NewField("true")
		}
		if choice.Delta.JSON.ToolCalls.Valid() && len(choice.Delta.ToolCalls) > 0 {
			sdkChunk.Choices[index].Delta.JSON.ToolCalls = respjson.NewField("true")
		}
	}
	if !a.acc.AddChunk(sdkChunk) {
		return false
	}

	for _, choice := range chunk.Choices {
		choiceIndex := int(choice.Index)
		state := a.choiceState(choiceIndex)
		state.content.WriteString(choice.Delta.Content)
		state.refusal.WriteString(choice.Delta.Refusal)

		for _, toolCall := range choice.Delta.ToolCalls {
			toolState := a.toolCallState(state, int(toolCall.Index))
			toolState.name.WriteString(toolCall.Function.Name)
			toolState.arguments.WriteString(toolCall.Function.Arguments)
		}

		a.publishChoice(choiceIndex, state)
	}
	return true
}

func (a *chatStreamAccumulator) choiceState(index int) *chatStreamChoiceAccumulator {
	if index >= len(a.choices) {
		a.choices = append(a.choices, make([]*chatStreamChoiceAccumulator, index-len(a.choices)+1)...)
		for choiceIndex := range a.choices {
			if a.choices[choiceIndex] == nil {
				a.choices[choiceIndex] = &chatStreamChoiceAccumulator{}
			}
		}
	}
	return a.choices[index]
}

func (a *chatStreamAccumulator) toolCallState(
	choice *chatStreamChoiceAccumulator,
	index int,
) *chatStreamToolCallAccumulator {
	if index >= len(choice.toolCalls) {
		choice.toolCalls = append(
			choice.toolCalls,
			make([]*chatStreamToolCallAccumulator, index-len(choice.toolCalls)+1)...,
		)
		for toolIndex := range choice.toolCalls {
			if choice.toolCalls[toolIndex] == nil {
				choice.toolCalls[toolIndex] = &chatStreamToolCallAccumulator{}
			}
		}
	}
	return choice.toolCalls[index]
}

func (a *chatStreamAccumulator) publishChoice(
	choiceIndex int,
	state *chatStreamChoiceAccumulator,
) {
	if choiceIndex >= len(a.acc.Choices) {
		return
	}
	choice := &a.acc.Choices[choiceIndex]
	choice.Message.Content = state.content.String()
	choice.Message.Refusal = state.refusal.String()
	for index, toolState := range state.toolCalls {
		if index >= len(choice.Message.ToolCalls) {
			break
		}
		if toolState == nil {
			continue
		}
		choice.Message.ToolCalls[index].Function.Name = toolState.name.String()
		choice.Message.ToolCalls[index].Function.Arguments = toolState.arguments.String()
	}
}

// sdkChunk returns a reusable copy with growing strings cleared. The original
// chunk remains untouched for callbacks and partial responses.
func (a *chatStreamAccumulator) sdkChunk(chunk openaigo.ChatCompletionChunk) openaigo.ChatCompletionChunk {
	a.scratchChunk = chunk
	if len(chunk.Choices) == 0 {
		clear(a.scratchChoices)
		clear(a.scratchToolCalls)
		return a.scratchChunk
	}

	if cap(a.scratchChoices) < len(chunk.Choices) {
		a.scratchChoices = make([]openaigo.ChatCompletionChunkChoice, len(chunk.Choices))
	} else {
		if len(chunk.Choices) < len(a.scratchChoices) {
			clear(a.scratchChoices[len(chunk.Choices):])
		}
		a.scratchChoices = a.scratchChoices[:len(chunk.Choices)]
	}
	if cap(a.scratchToolCalls) < len(chunk.Choices) {
		a.scratchToolCalls = make(
			[][]openaigo.ChatCompletionChunkChoiceDeltaToolCall,
			len(chunk.Choices),
		)
	} else {
		if len(chunk.Choices) < len(a.scratchToolCalls) {
			clear(a.scratchToolCalls[len(chunk.Choices):])
		}
		a.scratchToolCalls = a.scratchToolCalls[:len(chunk.Choices)]
	}

	for index, choice := range chunk.Choices {
		scratchChoice := choice
		scratchChoice.Delta.Content = ""
		scratchChoice.Delta.Refusal = ""

		toolCalls := choice.Delta.ToolCalls
		if cap(a.scratchToolCalls[index]) < len(toolCalls) {
			a.scratchToolCalls[index] = make(
				[]openaigo.ChatCompletionChunkChoiceDeltaToolCall,
				len(toolCalls),
			)
		} else {
			if len(toolCalls) < len(a.scratchToolCalls[index]) {
				clear(a.scratchToolCalls[index][len(toolCalls):])
			}
			a.scratchToolCalls[index] = a.scratchToolCalls[index][:len(toolCalls)]
		}
		copy(a.scratchToolCalls[index], toolCalls)
		for toolIndex := range a.scratchToolCalls[index] {
			a.scratchToolCalls[index][toolIndex].Function.Name = ""
			a.scratchToolCalls[index][toolIndex].Function.Arguments = ""
			a.scratchToolCalls[index][toolIndex].JSON = openaigo.ChatCompletionChunkChoiceDeltaToolCall{}.JSON
			a.scratchToolCalls[index][toolIndex].Function.JSON = openaigo.ChatCompletionChunkChoiceDeltaToolCallFunction{}.JSON
		}
		scratchChoice.Delta.ToolCalls = a.scratchToolCalls[index]
		scratchChoice.JSON = openaigo.ChatCompletionChunkChoice{}.JSON
		scratchChoice.Delta.JSON = openaigo.ChatCompletionChunkChoiceDelta{}.JSON
		scratchChoice.Delta.FunctionCall.JSON = openaigo.ChatCompletionChunkChoiceDeltaFunctionCall{}.JSON
		scratchChoice.Logprobs.JSON = openaigo.ChatCompletionChunkChoiceLogprobs{}.JSON
		a.scratchChoices[index] = scratchChoice
	}

	a.scratchChunk.Choices = a.scratchChoices
	return a.scratchChunk
}
