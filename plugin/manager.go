//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package plugin provides runner-scoped extensions.
package plugin

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	errNilPlugin = errors.New("plugin is nil")
	errEmptyName = errors.New("plugin name is empty")
)

// Plugin registers hooks into a Runner.
//
// Plugins are registered once on a Runner and applied automatically to all
// invocations created by that Runner.
type Plugin interface {
	// Name returns a stable, unique name for this plugin instance.
	Name() string

	// Register wires plugin callbacks into the provided Registry.
	Register(r *Registry)
}

// Closer is implemented by plugins that need to release resources.
type Closer interface {
	Close(ctx context.Context) error
}

// EventHook is invoked for each event passing through the Runner.
type EventHook func(
	ctx context.Context,
	invocation *agent.Invocation,
	e *event.Event,
) (*event.Event, error)

// AfterRunArgs contains context available after one Runner.Run finishes.
type AfterRunArgs struct {
	// Invocation is the root invocation associated with the run.
	Invocation *agent.Invocation
	// CompletionEvent is a snapshot of the finalized Runner completion event.
	CompletionEvent *event.Event
}

// AfterRunHook is invoked after Runner builds the finalized completion event.
type AfterRunHook func(ctx context.Context, args *AfterRunArgs) error

// Registry exposes hook registration points for a single plugin.
type Registry struct {
	name string
	mgr  *Manager
}

// BeforeAgent registers a before-agent callback.
func (r *Registry) BeforeAgent(cb agent.BeforeAgentCallbackStructured) {
	if r == nil || r.mgr == nil || cb == nil {
		return
	}
	r.mgr.agentCallbacks.RegisterBeforeAgent(
		func(ctx context.Context, args *agent.BeforeAgentArgs) (
			*agent.BeforeAgentResult, error,
		) {
			res, err := cb(ctx, args)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.name, err)
			}
			return res, nil
		},
	)
}

// AfterAgent registers an after-agent callback.
func (r *Registry) AfterAgent(cb agent.AfterAgentCallbackStructured) {
	if r == nil || r.mgr == nil || cb == nil {
		return
	}
	r.mgr.agentCallbacks.RegisterAfterAgent(
		func(ctx context.Context, args *agent.AfterAgentArgs) (
			*agent.AfterAgentResult, error,
		) {
			res, err := cb(ctx, args)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.name, err)
			}
			return res, nil
		},
	)
}

// BeforeModel registers a before-model callback.
func (r *Registry) BeforeModel(cb model.BeforeModelCallbackStructured) {
	if r == nil || r.mgr == nil || cb == nil {
		return
	}
	r.mgr.modelCallbacks.RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (
			*model.BeforeModelResult, error,
		) {
			res, err := cb(ctx, args)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.name, err)
			}
			return res, nil
		},
	)
}

// AfterModel registers an after-model callback.
func (r *Registry) AfterModel(cb model.AfterModelCallbackStructured) {
	if r == nil || r.mgr == nil || cb == nil {
		return
	}
	r.mgr.modelCallbacks.RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (
			*model.AfterModelResult, error,
		) {
			res, err := cb(ctx, args)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.name, err)
			}
			return res, nil
		},
	)
}

// BeforeTool registers a before-tool callback.
func (r *Registry) BeforeTool(cb tool.BeforeToolCallbackStructured) {
	if r == nil || r.mgr == nil || cb == nil {
		return
	}
	r.mgr.toolCallbacks.RegisterBeforeTool(
		func(ctx context.Context, args *tool.BeforeToolArgs) (
			*tool.BeforeToolResult, error,
		) {
			res, err := cb(ctx, args)
			if err != nil {
				return res, fmt.Errorf("%s: %w", r.name, err)
			}
			return res, nil
		},
	)
}

// AfterTool registers an after-tool callback.
func (r *Registry) AfterTool(cb tool.AfterToolCallbackStructured) {
	if r == nil || r.mgr == nil || cb == nil {
		return
	}
	r.mgr.toolCallbacks.RegisterAfterTool(
		func(ctx context.Context, args *tool.AfterToolArgs) (
			*tool.AfterToolResult, error,
		) {
			res, err := cb(ctx, args)
			if err != nil {
				return res, fmt.Errorf("%s: %w", r.name, err)
			}
			return res, nil
		},
	)
}

// AfterToolMessages registers a callback that can replace model-facing tool
// result messages after tool execution and before the event is emitted.
func (r *Registry) AfterToolMessages(cb AfterToolMessagesCallback) {
	if r == nil || r.mgr == nil || cb == nil {
		return
	}
	r.mgr.afterToolMessagesHooks = append(
		r.mgr.afterToolMessagesHooks,
		namedAfterToolMessagesHook{name: r.name, hook: cb},
	)
}

// OnEvent registers an event hook.
func (r *Registry) OnEvent(hook EventHook) {
	if r == nil || r.mgr == nil || hook == nil {
		return
	}
	r.mgr.eventHooks = append(r.mgr.eventHooks, namedEventHook{
		name: r.name,
		hook: hook,
	})
}

// AfterRun registers a hook that observes a completed Runner.Run.
func (r *Registry) AfterRun(hook AfterRunHook) {
	if r == nil || r.mgr == nil || hook == nil {
		return
	}
	r.mgr.afterRunHooks = append(r.mgr.afterRunHooks, namedAfterRunHook{
		name: r.name,
		hook: hook,
	})
}

// Manager composes multiple plugins into callback sets.
//
// Manager implements agent.PluginManager.
type Manager struct {
	plugins                []Plugin
	agentCallbacks         *agent.Callbacks
	modelCallbacks         *model.Callbacks
	toolCallbacks          *tool.Callbacks
	eventHooks             []namedEventHook
	afterRunHooks          []namedAfterRunHook
	afterToolMessagesHooks []namedAfterToolMessagesHook
}

type namedEventHook struct {
	name string
	hook EventHook
}

type namedAfterRunHook struct {
	name string
	hook AfterRunHook
}

type namedAfterToolMessagesHook struct {
	name string
	hook AfterToolMessagesCallback
}

// NewManager builds a Manager and registers all plugin hooks.
func NewManager(plugins ...Plugin) (*Manager, error) {
	m := &Manager{
		agentCallbacks: agent.NewCallbacks(),
		modelCallbacks: model.NewCallbacks(),
		toolCallbacks:  tool.NewCallbacks(),
	}
	seen := make(map[string]struct{})
	for _, p := range plugins {
		if p == nil {
			return nil, errNilPlugin
		}
		name := p.Name()
		if name == "" {
			return nil, errEmptyName
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate plugin %q", name)
		}
		seen[name] = struct{}{}
		m.plugins = append(m.plugins, p)
		p.Register(&Registry{name: name, mgr: m})
	}
	return m, nil
}

// MustNewManager panics if plugin registration fails.
func MustNewManager(plugins ...Plugin) *Manager {
	m, err := NewManager(plugins...)
	if err != nil {
		panic(err)
	}
	return m
}

// AgentCallbacks implements agent.PluginManager.
func (m *Manager) AgentCallbacks() *agent.Callbacks {
	if m == nil {
		return nil
	}
	if m.agentCallbacks == nil {
		return nil
	}
	if len(m.agentCallbacks.BeforeAgent) == 0 &&
		len(m.agentCallbacks.AfterAgent) == 0 {
		return nil
	}
	return m.agentCallbacks
}

// ModelCallbacks implements agent.PluginManager.
func (m *Manager) ModelCallbacks() *model.Callbacks {
	if m == nil {
		return nil
	}
	if m.modelCallbacks == nil {
		return nil
	}
	if len(m.modelCallbacks.BeforeModel) == 0 &&
		len(m.modelCallbacks.AfterModel) == 0 {
		return nil
	}
	return m.modelCallbacks
}

// ToolCallbacks implements agent.PluginManager.
func (m *Manager) ToolCallbacks() *tool.Callbacks {
	if m == nil {
		return nil
	}
	if m.toolCallbacks == nil {
		return nil
	}
	if len(m.toolCallbacks.BeforeTool) == 0 &&
		len(m.toolCallbacks.AfterTool) == 0 &&
		m.toolCallbacks.ToolResultMessages == nil {
		return nil
	}
	return m.toolCallbacks
}

// OnEvent implements agent.PluginManager.
func (m *Manager) OnEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	if m == nil || e == nil {
		return e, nil
	}
	curr := e
	for _, h := range m.eventHooks {
		next, err := h.hook(ctx, invocation, curr)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %w", h.name, err)
		}
		if next != nil {
			curr = next
		}
	}
	return curr, nil
}

// AfterRun runs registered after-run hooks in plugin order.
func (m *Manager) AfterRun(ctx context.Context, args *AfterRunArgs) error {
	if m == nil || args == nil {
		return nil
	}
	var errs []error
	for _, h := range m.afterRunHooks {
		if err := h.hook(ctx, args); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q: %w", h.name, err))
		}
	}
	return errors.Join(errs...)
}

// AfterToolMessages runs registered after-tool-messages hooks in plugin order.
func (m *Manager) AfterToolMessages(
	ctx context.Context,
	args *AfterToolMessagesArgs,
) (*AfterToolMessagesResult, error) {
	if m == nil || args == nil {
		return nil, nil
	}
	var last *AfterToolMessagesResult
	for _, h := range m.afterToolMessagesHooks {
		res, err := h.hook(ctx, args)
		if err != nil {
			return res, fmt.Errorf("plugin %q: %w", h.name, err)
		}
		if res == nil || len(res.ToolResultMessages) == 0 {
			continue
		}
		current := cloneMessages(args.ToolResultMessages)
		if len(current) == 0 {
			current = toolResultMessagesFromEvent(args.ToolResultEvent)
		}
		replacements, err := normalizeToolResultReplacements(
			current,
			res.ToolResultMessages,
		)
		if err != nil {
			return res, fmt.Errorf("plugin %q: %w", h.name, err)
		}
		if err := replaceToolResultEventChoices(
			args.ToolResultEvent,
			replacements,
		); err != nil {
			return res, fmt.Errorf("plugin %q: %w", h.name, err)
		}
		tailLen := len(args.ToolResultMessages)
		if tailLen == 0 {
			tailLen = len(current)
		}
		args.Messages = replaceTailMessages(
			args.Messages,
			tailLen,
			replacements,
		)
		args.ToolResultMessages = cloneMessages(replacements)
		last = &AfterToolMessagesResult{
			ToolResultMessages: cloneMessages(replacements),
		}
	}
	return last, nil
}

func normalizeToolResultReplacements(
	original []model.Message,
	replacements []model.Message,
) ([]model.Message, error) {
	if len(original) == 0 {
		return nil, errors.New("after tool messages: original tool result messages are empty")
	}
	if len(replacements) != len(original) {
		return nil, fmt.Errorf(
			"after tool messages: replacement count %d does not match original tool result count %d",
			len(replacements),
			len(original),
		)
	}
	byID := make(map[string]model.Message, len(replacements))
	for _, msg := range replacements {
		if msg.ToolID == "" {
			return nil, errors.New("after tool messages: replacement tool message missing tool id")
		}
		if msg.Role != model.RoleTool {
			return nil, fmt.Errorf(
				"after tool messages: replacement for tool id %q must use role %q",
				msg.ToolID,
				model.RoleTool,
			)
		}
		if _, ok := byID[msg.ToolID]; ok {
			return nil, fmt.Errorf(
				"after tool messages: replacement contains duplicate tool id %q",
				msg.ToolID,
			)
		}
		byID[msg.ToolID] = msg
	}
	out := make([]model.Message, 0, len(original))
	for _, msg := range original {
		if msg.ToolID == "" {
			return nil, errors.New("after tool messages: original tool message missing tool id")
		}
		if msg.Role != model.RoleTool {
			return nil, fmt.Errorf(
				"after tool messages: original for tool id %q must use role %q",
				msg.ToolID,
				model.RoleTool,
			)
		}
		replacement, ok := byID[msg.ToolID]
		if !ok {
			return nil, fmt.Errorf(
				"after tool messages: replacement missing tool id %q",
				msg.ToolID,
			)
		}
		out = append(out, replacement)
		delete(byID, msg.ToolID)
	}
	for toolID := range byID {
		return nil, fmt.Errorf(
			"after tool messages: replacement contains unknown tool id %q",
			toolID,
		)
	}
	return cloneMessages(out), nil
}

func replaceToolResultEventChoices(
	ev *event.Event,
	replacements []model.Message,
) error {
	if ev == nil || ev.Response == nil || len(replacements) == 0 {
		return nil
	}
	byID := make(map[string]model.Message, len(replacements))
	for _, msg := range replacements {
		byID[msg.ToolID] = msg
	}
	choices := make([]model.Choice, 0, len(ev.Response.Choices))
	seen := make(map[string]struct{}, len(replacements))
	for _, choice := range ev.Response.Choices {
		toolID := toolChoiceID(choice)
		if toolID == "" {
			return errors.New("after tool messages: original tool result choice missing tool id")
		}
		msg, ok := byID[toolID]
		if !ok {
			return fmt.Errorf(
				"after tool messages: replacement missing tool id %q",
				toolID,
			)
		}
		choices = append(choices, replaceChoiceToolMessage(choice, msg))
		seen[toolID] = struct{}{}
	}
	for _, msg := range replacements {
		if _, ok := seen[msg.ToolID]; !ok {
			return fmt.Errorf(
				"after tool messages: replacement contains unknown tool id %q",
				msg.ToolID,
			)
		}
	}
	ev.Response.Choices = choices
	return nil
}

func replaceChoiceToolMessage(choice model.Choice, msg model.Message) model.Choice {
	updated := choice
	if updated.Message.ToolID != "" {
		updated.Message = msg
	}
	if updated.Delta.ToolID != "" {
		updated.Delta = msg
	}
	if updated.Message.ToolID == "" && updated.Delta.ToolID == "" {
		updated.Message = msg
	}
	return updated
}

func toolResultMessagesFromEvent(ev *event.Event) []model.Message {
	if ev == nil || ev.Response == nil {
		return nil
	}
	out := make([]model.Message, 0, len(ev.Response.Choices))
	for _, choice := range ev.Response.Choices {
		msg := choice.Message
		if msg.ToolID == "" && choice.Delta.ToolID != "" {
			msg = choice.Delta
		}
		if msg.ToolID != "" {
			out = append(out, msg)
		}
	}
	return cloneMessages(out)
}

func toolChoiceID(choice model.Choice) string {
	if choice.Message.ToolID != "" {
		return choice.Message.ToolID
	}
	return choice.Delta.ToolID
}

// Close implements agent.PluginManager.
func (m *Manager) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var errs []error
	for i := len(m.plugins) - 1; i >= 0; i-- {
		p := m.plugins[i]
		c, ok := p.(Closer)
		if !ok {
			continue
		}
		if err := c.Close(ctx); err != nil {
			errs = append(
				errs,
				fmt.Errorf(
					"plugin %q: %w",
					p.Name(),
					err,
				),
			)
		}
	}
	return errors.Join(errs...)
}

func replaceTailMessages(
	messages []model.Message,
	oldTailLen int,
	replacement []model.Message,
) []model.Message {
	if oldTailLen < 0 || oldTailLen > len(messages) {
		return cloneMessages(messages)
	}
	out := make([]model.Message, 0, len(messages)-oldTailLen+len(replacement))
	out = append(out, messages[:len(messages)-oldTailLen]...)
	out = append(out, replacement...)
	return cloneMessages(out)
}

func cloneMessages(messages []model.Message) []model.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]model.Message, len(messages))
	copy(out, messages)
	return out
}
