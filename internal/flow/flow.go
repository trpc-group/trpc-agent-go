//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package flow provides the core flow functionality interfaces and types.
package flow

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Flow is the interface that all flows must implement.
type Flow interface {
	// Run executes the flow and yields events as they occur.
	// Returns the event channel and any setup error.
	Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error)
}

// RequestProcessor processes LLM requests before they are sent to the model.
type RequestProcessor interface {
	// ProcessRequest processes the request and sends events directly to the provided channel.
	ProcessRequest(ctx context.Context, invocation *agent.Invocation, req *model.Request, ch chan<- *event.Event)
}

// ResponseProcessor processes LLM responses after they are received from the model.
type ResponseProcessor interface {
	// ProcessResponse processes the response and sends events directly to the provided channel.
	ProcessResponse(ctx context.Context, invocation *agent.Invocation, rsp *model.Response, ch chan<- *event.Event)
}
