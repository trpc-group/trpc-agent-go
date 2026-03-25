//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversation

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const pluginName = "openclaw_conversation"

// Plugin persists request-scoped conversation metadata onto user events.
type Plugin struct{}

// Name implements plugin.Plugin.
func (Plugin) Name() string { return pluginName }

// Register implements plugin.Plugin.
func (Plugin) Register(r *plugin.Registry) {
	if r == nil {
		return
	}
	r.OnEvent(func(
		_ context.Context,
		invocation *agent.Invocation,
		evt *event.Event,
	) (*event.Event, error) {
		if evt == nil || evt.Author != authorUser || invocation == nil {
			return evt, nil
		}
		annotation, ok := AnnotationFromRuntimeState(
			invocation.RunOptions.RuntimeState,
		)
		if !ok {
			return evt, nil
		}
		if err := SetEventAnnotation(evt, annotation); err != nil {
			return nil, err
		}
		return evt, nil
	})
}

// PreSummaryHook rewrites summary input using persisted speaker metadata
// when available.
func PreSummaryHook(
	in *summary.PreSummaryHookContext,
) error {
	if in == nil || len(in.Events) == 0 {
		return nil
	}
	text := BuildSummaryText(in.Events)
	if text == "" {
		return nil
	}
	in.Text = text
	return nil
}
