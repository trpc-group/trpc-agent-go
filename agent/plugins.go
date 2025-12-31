//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PluginManager provides runner-scoped, invocation-aware hooks.
//
// A PluginManager is typically created once on a Runner, then attached to
// every Invocation constructed by that Runner. This makes cross-cutting
// behaviors (logging, policy enforcement, request shaping) available without
// repeating per-agent configuration.
type PluginManager interface {
	// AgentCallbacks returns global agent callbacks.
	AgentCallbacks() *Callbacks

	// ModelCallbacks returns global model callbacks.
	ModelCallbacks() *model.Callbacks

	// ToolCallbacks returns global tool callbacks.
	ToolCallbacks() *tool.Callbacks

	// OnEvent is called for each event passing through the Runner.
	// Implementations may mutate the event in place or return a replacement.
	OnEvent(
		ctx context.Context,
		invocation *Invocation,
		e *event.Event,
	) (*event.Event, error)

	// Close releases any resources held by plugins.
	Close(ctx context.Context) error
}
