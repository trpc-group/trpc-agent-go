//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package processor provides request and response processing functionality.
package processor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// BasicRequestProcessor implements the basic request processing logic.
type BasicRequestProcessor struct {
	// GenerationConfig contains the default generation configuration.
	GenerationConfig model.GenerationConfig
}

// NewBasicRequestProcessor creates a new basic request processor with default settings.
func NewBasicRequestProcessor(opts ...BasicOption) *BasicRequestProcessor {
	processor := &BasicRequestProcessor{
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	// Apply options.
	for _, opt := range opts {
		opt(processor)
	}

	return processor
}

// BasicOption is a functional option for configuring the BasicRequestProcessor.
type BasicOption func(*BasicRequestProcessor)

// WithGenerationConfig sets the default generation configuration.
func WithGenerationConfig(config model.GenerationConfig) BasicOption {
	return func(p *BasicRequestProcessor) {
		p.GenerationConfig = config
	}
}

// ProcessRequest implements the flow.RequestProcessor interface.
// It handles setting generation parameters.
func (p *BasicRequestProcessor) ProcessRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	ch chan<- *event.Event,
) {
	if req == nil {
		log.ErrorfContext(
			ctx,
			"Basic request processor: request is nil",
		)
		return
	}

	if invocation == nil {
		return
	}

	log.DebugfContext(
		ctx,
		"Basic request processor: processing request for agent %s",
		invocation.AgentName,
	)

	// Set generation configuration.
	req.GenerationConfig = p.GenerationConfig

	// Propagate structured output from invocation to request if present.
	if invocation.StructuredOutput != nil {
		req.StructuredOutput = invocation.StructuredOutput
	}

	log.DebugContext(ctx, "Basic request processor: sent preprocessing event")
	// Send a preprocessing event.
	if err := agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithObject(model.ObjectTypePreprocessingBasic),
	)); err != nil {
		log.DebugContext(ctx, "Basic request processor: context cancelled")
	}

}
