//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runneroutput

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// Capture contains the structured output payload and optional final response content.
type Capture struct {
	StructuredOutput any
	FinalContent     string
}

// CaptureRunnerOutputs consumes runner events and collects the latest structured output and final response content.
func CaptureRunnerOutputs(events <-chan *event.Event) (*Capture, error) {
	var out Capture
	for e := range events {
		if e == nil {
			continue
		}
		if e.Error != nil {
			return nil, fmt.Errorf("runner event error: %v", e.Error)
		}
		if e.StructuredOutput != nil {
			out.StructuredOutput = e.StructuredOutput
		}
		if e.IsFinalResponse() {
			if e.Response == nil || len(e.Response.Choices) == 0 {
				return nil, errors.New("runner final response has no choices")
			}
			out.FinalContent = e.Response.Choices[0].Message.Content
		}
	}
	if out.StructuredOutput == nil && out.FinalContent == "" {
		return nil, errors.New("runner did not return any output")
	}
	return &out, nil
}
