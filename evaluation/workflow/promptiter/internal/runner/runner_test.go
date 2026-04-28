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
}

func TestCaptureOutputReturnsRunnerErrors(t *testing.T) {
	events := make(chan *event.Event, 1)
	events <- event.NewErrorEvent("invocation-id", "runner", model.ErrorTypeRunError, "runner failed")
	close(events)

	output, err := CaptureOutput(events)

	assert.Error(t, err)
	assert.Nil(t, output)
}
