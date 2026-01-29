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

// Manager composes multiple plugins into callback sets.
//
// Manager implements agent.PluginManager.
type Manager struct {
	plugins        []Plugin
	agentCallbacks *agent.Callbacks
	modelCallbacks *model.Callbacks
	toolCallbacks  *tool.Callbacks
	eventHooks     []namedEventHook
}

type namedEventHook struct {
	name string
	hook EventHook
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
