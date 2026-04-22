//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package errormessage provides a runner-scoped plugin that rewrites the
// user-facing content of error events before they are persisted or forwarded.
//
// By default, Runner populates the assistant-visible content of an error event
// with a generic fallback message when the upstream event carries
// Response.Error but no Choices content. This plugin runs in OnEvent before
// that fallback and fills the Choices content itself, so callers can surface a
// customised, tenant-specific, or localised message to end users while keeping
// the structured Response.Error intact for debugging and downstream consumers.
//
// Scope:
//
//   - The plugin is triggered via the runner OnEvent hook, so it covers
//     errors produced by agents during a run (for example, events emitted by
//     llmflow for agent.StopError, or any raw event.NewErrorEvent).
//   - Errors returned synchronously from agent.Run (before any event channel
//     is produced) are handled by the runner itself via
//     ensureErrorEventContent, and the runner applies its built-in fallback
//     content to those events before invoking plugin hooks. In that specific
//     path the plugin cannot rewrite the persisted content. Callers that need
//     a custom message there should make their agent implementation surface
//     a first error event instead of returning an error from Run.
package errormessage

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

// errorMessagePlugin rewrites the visible content of error events so that a
// customised message replaces the framework's default fallback.
type errorMessagePlugin struct {
	name         string
	resolver     Resolver
	finishReason string
}

// New creates a new error message plugin.
func New(options ...Option) plugin.Plugin {
	opts := newOptions(options...)
	return &errorMessagePlugin{
		name:         opts.name,
		resolver:     opts.resolver,
		finishReason: opts.finishReason,
	}
}

// Name implements plugin.Plugin.
func (p *errorMessagePlugin) Name() string {
	return p.name
}

// Register implements plugin.Plugin.
func (p *errorMessagePlugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.OnEvent(p.onEvent)
}

func (p *errorMessagePlugin) onEvent(
	ctx context.Context,
	inv *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	if p == nil || p.resolver == nil {
		return nil, nil
	}
	if !isRewritableErrorEvent(e) {
		return nil, nil
	}
	content, ok := p.resolver(ctx, inv, e)
	if !ok || content == "" {
		return nil, nil
	}

	// Shallow-copy the event and deep-copy Response so upstream/sibling
	// consumers are not affected by our mutation.
	updated := *e
	updated.Response = e.Response.Clone()
	ensureFirstAssistantChoice(updated.Response)
	updated.Response.Choices[0].Message.Content = content
	if updated.Response.Choices[0].FinishReason == nil {
		reason := p.finishReason
		updated.Response.Choices[0].FinishReason = &reason
	}
	return &updated, nil
}

// isRewritableErrorEvent reports whether the event is an error event that has
// no assistant-visible content yet. Events that already carry valid content
// (e.g. a partial assistant response produced before the failure) are left
// untouched so this plugin never overwrites real assistant text. Partial
// events are also skipped so that a later final event can still decide the
// outcome without this plugin leaking a premature failure message to callers.
func isRewritableErrorEvent(e *event.Event) bool {
	if e == nil || e.Response == nil || e.Response.Error == nil {
		return false
	}
	if e.IsPartial {
		return false
	}
	if e.IsValidContent() {
		return false
	}
	return true
}

func ensureFirstAssistantChoice(rsp *model.Response) {
	if rsp == nil {
		return
	}
	if len(rsp.Choices) == 0 {
		rsp.Choices = []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
			},
		}}
		return
	}
	// Always force the first choice into an assistant role so resolver
	// output is never written into a non-assistant choice (for example when
	// upstream emits an error event whose first choice carries a
	// role=user or role=system placeholder).
	rsp.Choices[0].Message.Role = model.RoleAssistant
}
