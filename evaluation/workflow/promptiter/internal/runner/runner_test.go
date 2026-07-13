//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCaptureOutputRejectsNilStream(t *testing.T) {
	output, err := CaptureOutput(nil)

	assert.Error(t, err)
	assert.Nil(t, output)
}

func TestCaptureOutputCollectsStructuredOutputAndFinalContent(t *testing.T) {
	events := make(chan *event.Event, 3)
	events <- nil
	events <- event.NewResponseEvent(
		"invocation-id",
		"runner",
		&model.Response{Done: true},
		event.WithStructuredOutputPayload(map[string]any{"k": "v"}),
	)
	events <- event.NewResponseEvent(
		"invocation-id",
		"runner",
		&model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("final content")},
			},
		},
	)
	close(events)

	output, err := CaptureOutput(events)

	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"k": "v"}, output.StructuredOutput)
	assert.Equal(t, "final content", output.FinalContent)
	assert.Equal(t, 1, output.Usage.Calls)
	assert.False(t, output.Usage.Complete)
}

func TestCaptureOutputReturnsRunnerErrors(t *testing.T) {
	events := make(chan *event.Event, 1)
	events <- event.NewErrorEvent("invocation-id", "runner", model.ErrorTypeRunError, "runner failed")
	close(events)

	output, err := CaptureOutput(events)

	assert.Error(t, err)
	assert.Nil(t, output)
}

func TestCaptureOutputSummarizesCompleteAndMissingUsage(t *testing.T) {
	events := make(chan *event.Event, 2)
	events <- event.NewResponseEvent("invocation-id", "runner", &model.Response{
		ID: "call-1", Done: true,
		Usage: &model.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	})
	events <- event.NewResponseEvent("invocation-id", "runner", &model.Response{
		ID: "call-2", Done: true,
	})
	close(events)

	output, err := CaptureOutput(events)
	assert.NoError(t, err)
	assert.Equal(t, 2, output.Usage.Calls)
	assert.Equal(t, int64(5), output.Usage.TotalTokens)
	assert.False(t, output.Usage.Complete)
}
