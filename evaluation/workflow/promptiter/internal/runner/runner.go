//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package runner captures common runner outputs for PromptIter workflow adapters.
package runner

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Output stores the structured and textual outputs collected from one runner invocation.
type Output struct {
	// StructuredOutput keeps the latest in-memory structured payload emitted by the runner.
	StructuredOutput any
	// FinalContent keeps the latest textual assistant content emitted by the runner.
	FinalContent string
	// Usage contains model-call telemetry observed while consuming the stream.
	Usage promptiter.Usage
}

type callUsage struct {
	done  bool
	usage *model.Usage
}

// CaptureOutput consumes one runner event stream and extracts common output artifacts.
func CaptureOutput(events <-chan *event.Event) (*Output, error) {
	if events == nil {
		return nil, errors.New("runner event stream is nil")
	}
	output := &Output{}
	calls := make(map[string]*callUsage)
	anonymousCall := 0
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.IsError() {
			if evt.Error != nil {
				return nil, fmt.Errorf("runner returned error: %w", evt.Error)
			}
			return nil, errors.New("runner returned error event")
		}
		captureCallUsage(evt, calls, &anonymousCall)
		if evt.StructuredOutput != nil {
			output.StructuredOutput = evt.StructuredOutput
		}
		if len(evt.Choices) > 0 && len(evt.Choices[0].Message.Content) > 0 {
			output.FinalContent = evt.Choices[0].Message.Content
		}
	}
	output.Usage = summarizeCallUsage(calls)
	return output, nil
}

func captureCallUsage(
	evt *event.Event,
	calls map[string]*callUsage,
	anonymousCall *int,
) {
	if evt == nil || evt.Response == nil || evt.IsRunnerCompletion() || evt.IsToolResultResponse() {
		return
	}
	response := evt.Response
	// Some runners emit a separate structured-output carrier with no model
	// response identity, choices, error, or usage. It is not another model call.
	if response.ID == "" && len(response.Choices) == 0 && response.Error == nil && response.Usage == nil {
		return
	}
	key := response.ID
	if key == "" {
		key = anonymousCallKey(calls, anonymousCall)
	}
	state := calls[key]
	if state == nil {
		state = &callUsage{}
		calls[key] = state
	}
	state.done = state.done || response.Done
	if response.Usage != nil {
		usage := *response.Usage
		state.usage = &usage
	}
}

func anonymousCallKey(calls map[string]*callUsage, sequence *int) string {
	if *sequence > 0 {
		current := fmt.Sprintf("anonymous-%d", *sequence)
		if state := calls[current]; state != nil && !state.done {
			return current
		}
	}
	*sequence++
	return fmt.Sprintf("anonymous-%d", *sequence)
}

func summarizeCallUsage(calls map[string]*callUsage) promptiter.Usage {
	if len(calls) == 0 {
		return promptiter.Usage{Complete: true}
	}
	result := promptiter.Usage{Calls: len(calls), Complete: true}
	for _, call := range calls {
		if call == nil || !call.done || call.usage == nil {
			result.Complete = false
			continue
		}
		result.PromptTokens += int64(call.usage.PromptTokens)
		result.CompletionTokens += int64(call.usage.CompletionTokens)
		result.TotalTokens += int64(call.usage.TotalTokens)
	}
	if result.TotalTokens == 0 {
		result.TotalTokens = result.PromptTokens + result.CompletionTokens
	}
	return result
}
