//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type pluginManagerChain []agent.PluginManager

type afterToolMessagesManager interface {
	AfterToolMessages(
		context.Context,
		*plugin.AfterToolMessagesArgs,
	) (*plugin.AfterToolMessagesResult, error)
}

type afterRunManager interface {
	AfterRun(context.Context, *plugin.AfterRunArgs) error
}

func newPluginManagerChain(managers ...agent.PluginManager) agent.PluginManager {
	filtered := make([]agent.PluginManager, 0, len(managers))
	for _, manager := range managers {
		if manager != nil {
			filtered = append(filtered, manager)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return pluginManagerChain(filtered)
}

func combineRunPlugins(runnerPlugins agent.PluginManager, runPlugins []agent.PluginManager) agent.PluginManager {
	managers := make([]agent.PluginManager, 0, 1+len(runPlugins))
	managers = append(managers, runnerPlugins)
	managers = append(managers, runPlugins...)
	return newPluginManagerChain(managers...)
}

func (c pluginManagerChain) AgentCallbacks() *agent.Callbacks {
	out := agent.NewCallbacks()
	hasCallbacks := false
	for _, manager := range c {
		callbacks := manager.AgentCallbacks()
		if callbacks == nil {
			continue
		}
		hasCallbacks = true
		current := callbacks
		out.RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
			return current.RunBeforeAgent(ctx, args)
		})
		out.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			return current.RunAfterAgent(ctx, args)
		})
	}
	if !hasCallbacks {
		return nil
	}
	return out
}

func (c pluginManagerChain) ModelCallbacks() *model.Callbacks {
	out := model.NewCallbacks()
	hasCallbacks := false
	for _, manager := range c {
		callbacks := manager.ModelCallbacks()
		if callbacks == nil {
			continue
		}
		hasCallbacks = true
		current := callbacks
		out.RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return current.RunBeforeModel(ctx, args)
		})
		out.RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			return current.RunAfterModel(ctx, args)
		})
	}
	if !hasCallbacks {
		return nil
	}
	return out
}

func (c pluginManagerChain) ToolCallbacks() *tool.Callbacks {
	out := tool.NewCallbacks()
	hasCallbacks := false
	for _, manager := range c {
		callbacks := manager.ToolCallbacks()
		if callbacks == nil {
			continue
		}
		if len(callbacks.BeforeTool) > 0 {
			hasCallbacks = true
			current := callbacks
			out.RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
				return current.RunBeforeTool(ctx, args)
			})
		}
		if len(callbacks.AfterTool) > 0 {
			hasCallbacks = true
			current := callbacks
			out.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
				return current.RunAfterTool(ctx, args)
			})
		}
		if callbacks.ToolResultMessages != nil {
			hasCallbacks = true
			out.RegisterToolResultMessages(callbacks.ToolResultMessages)
		}
	}
	if !hasCallbacks {
		return nil
	}
	return out
}

func (c pluginManagerChain) OnEvent(ctx context.Context, invocation *agent.Invocation, e *event.Event) (*event.Event, error) {
	current := e
	for _, manager := range c {
		updated, err := manager.OnEvent(ctx, invocation, current)
		if err != nil {
			return nil, err
		}
		if updated != nil {
			current = updated
		}
	}
	return current, nil
}

func (c pluginManagerChain) AfterRun(ctx context.Context, args *plugin.AfterRunArgs) error {
	var errs []error
	for _, manager := range c {
		hooks, ok := manager.(afterRunManager)
		if !ok {
			continue
		}
		if err := hooks.AfterRun(ctx, args); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c pluginManagerChain) AfterToolMessages(
	ctx context.Context,
	args *plugin.AfterToolMessagesArgs,
) (*plugin.AfterToolMessagesResult, error) {
	var last *plugin.AfterToolMessagesResult
	for _, manager := range c {
		hooks, ok := manager.(afterToolMessagesManager)
		if !ok {
			continue
		}
		result, err := hooks.AfterToolMessages(ctx, args)
		if err != nil {
			return result, err
		}
		if result != nil && len(result.ToolResultMessages) > 0 {
			last = result
		}
	}
	return last, nil
}

func (c pluginManagerChain) Close(ctx context.Context) error {
	var errs []error
	for i := len(c) - 1; i >= 0; i-- {
		if err := c[i].Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("plugin manager %d: %w", i, err))
		}
	}
	return errors.Join(errs...)
}
