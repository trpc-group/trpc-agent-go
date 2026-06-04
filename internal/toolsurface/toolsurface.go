//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolsurface resolves the effective tool surface for an invocation:
// the base surface exposed by the agent plus the run-scoped tools, with the
// run-scoped tool filter applied. It is the single source of truth shared by
// the LLM flow (which uses it to build the model request) and by helpers such
// as the dynamic AgentTool (which derives a child capability surface from a
// parent invocation). Keeping the logic here avoids both behavioral drift and
// an import cycle between the flow engine and the tool packages.
package toolsurface

import (
	"context"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// UserToolsProvider is an optional interface that agents implement to expose
// their user tools (those registered via WithTools/WithToolSets), so the filter
// can distinguish them from framework-managed tools.
type UserToolsProvider interface {
	UserTools() []tool.Tool
}

// ToolFilterProvider is an optional interface that agents implement to provide
// a pre-resolved tool list for an invocation.
type ToolFilterProvider interface {
	FilterTools(ctx context.Context) []tool.Tool
}

// ResolveBase resolves the pre-run-option tool surface for an invocation along
// with user-tool tracking, before RunOptions tools/filter are applied.
//
// The returned map classifies user tools; a nil map means the agent does not
// support user-tool tracking, in which case callers must treat all tools as
// user tools.
func ResolveBase(
	ctx context.Context,
	invocation *agent.Invocation,
) ([]tool.Tool, map[string]bool, bool) {
	var allTools []tool.Tool
	var userToolNames map[string]bool
	hasUserToolTracking := false
	if provider, ok := invocation.Agent.(agent.InvocationToolSurfaceProvider); ok {
		allTools, userToolNames = provider.InvocationToolSurface(ctx, invocation)
		hasUserToolTracking = userToolNames != nil
	} else if provider, ok := invocation.Agent.(ToolFilterProvider); ok {
		allTools = provider.FilterTools(ctx)
	} else {
		allTools = invocation.Agent.Tools()
	}

	// User tools are those explicitly registered via WithTools and
	// WithToolSets. Framework tools (Knowledge, SubAgents) are never filtered.
	if !hasUserToolTracking {
		if provider, ok := invocation.Agent.(UserToolsProvider); ok {
			userTools := provider.UserTools()
			hasUserToolTracking = true
			userToolNames = make(map[string]bool, len(userTools))
			for _, t := range userTools {
				if name := toolName(t); name != "" {
					userToolNames[name] = true
				}
			}
		}
	}
	return allTools, userToolNames, hasUserToolTracking
}

// AppendRunOptionTools appends RunOptions.AdditionalTools and ExternalTools to
// allTools (de-duplicated by name), tracking user-tool classification and the
// names of any external tools that were added.
func AppendRunOptionTools(
	allTools []tool.Tool,
	userToolNames map[string]bool,
	hasUserToolTracking bool,
	opts agent.RunOptions,
) ([]tool.Tool, map[string]bool, bool, map[string]bool) {
	if len(opts.AdditionalTools) == 0 && len(opts.ExternalTools) == 0 {
		return allTools, userToolNames, hasUserToolTracking, nil
	}
	allTools = append([]tool.Tool(nil), allTools...)
	if hasUserToolTracking {
		userToolNames = copyToolNames(userToolNames)
	}
	serverNames := collectToolNames(allTools)
	seen := copyToolNames(serverNames)
	allTools, userToolNames = appendRunOptionToolList(
		allTools,
		userToolNames,
		hasUserToolTracking,
		seen,
		opts.AdditionalTools,
	)
	externalNames := make(map[string]bool, len(opts.ExternalTools))
	for _, tl := range opts.ExternalTools {
		name := toolName(tl)
		if name == "" || serverNames[name] {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		allTools = append(allTools, tl)
		externalNames[name] = true
		if hasUserToolTracking {
			if userToolNames == nil {
				userToolNames = make(map[string]bool)
			}
			userToolNames[name] = true
		}
	}
	return allTools, userToolNames, hasUserToolTracking, externalNames
}

// ApplyToolFilter applies the run-scoped ToolFilter to allTools, always keeping
// framework tools and keeping user tools only when the filter passes. The
// result is sorted by name for stable prompt-cache behavior. It assumes
// opts.ToolFilter != nil.
func ApplyToolFilter(
	ctx context.Context,
	allTools []tool.Tool,
	userToolNames map[string]bool,
	hasUserToolTracking bool,
	opts agent.RunOptions,
) []tool.Tool {
	filtered := make([]tool.Tool, 0, len(allTools))
	for _, t := range allTools {
		name := toolName(t)
		if name == "" {
			continue
		}

		// Determine if this is a user tool or framework tool.
		isUserTool := !hasUserToolTracking || userToolNames[name]

		// Framework tools are always included (never filtered).
		if !isUserTool {
			filtered = append(filtered, t)
			continue
		}

		// User tool: apply the filter function.
		if opts.ToolFilter(ctx, t) {
			filtered = append(filtered, t)
		}
	}

	// Sort tools by name to ensure stable order for better prompt cache hit
	// rate. Map iteration order is random in Go, so sorting ensures consistent
	// tool ordering across requests, which improves cache efficiency.
	sort.Slice(filtered, func(i, j int) bool {
		return toolName(filtered[i]) < toolName(filtered[j])
	})
	return filtered
}

// Effective returns the effective tool surface for the invocation: the base
// surface (InvocationToolSurface / FilterTools / Tools) plus
// RunOptions.AdditionalTools and ExternalTools, with the run-scoped
// RunOptions.ToolFilter applied. It mirrors exactly what the LLM flow exposes
// to the model, but it does not read or mutate invocation state (it
// recomputes), so it is safe for callers that need to inspect a parent
// invocation's current surface — for example a dynamic sub-agent tool deriving
// a child capability surface — including when the invocation passed in is a
// clone produced by parallel tool execution.
//
// The second return value classifies user tools (a nil map means the agent does
// not support user-tool tracking; callers should treat all returned tools as
// user tools).
func Effective(
	ctx context.Context,
	invocation *agent.Invocation,
) ([]tool.Tool, map[string]bool) {
	tools, userToolNames, _ := EffectiveWithExternal(ctx, invocation)
	return tools, userToolNames
}

// EffectiveWithExternal is like Effective but also returns the set of external
// (caller-executed) tool names contributed by RunOptions.ExternalTools.
//
// External tools are visible-but-not-executed for the parent run; a synchronous
// sub-agent has no channel to hand a caller-executed tool back to the original
// caller, and folding them into a child's executed tool surface would silently
// change their semantics. Callers deriving a child capability surface use this
// set to exclude external tools from what the model may select.
func EffectiveWithExternal(
	ctx context.Context,
	invocation *agent.Invocation,
) ([]tool.Tool, map[string]bool, map[string]bool) {
	if invocation == nil || invocation.Agent == nil {
		return nil, nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	allTools, userToolNames, hasUserToolTracking := ResolveBase(ctx, invocation)
	allTools, userToolNames, hasUserToolTracking, externalNames :=
		AppendRunOptionTools(
			allTools,
			userToolNames,
			hasUserToolTracking,
			invocation.RunOptions,
		)
	if invocation.RunOptions.ToolFilter == nil {
		return allTools, userToolNames, externalNames
	}
	return ApplyToolFilter(
		ctx,
		allTools,
		userToolNames,
		hasUserToolTracking,
		invocation.RunOptions,
	), userToolNames, externalNames
}

func appendRunOptionToolList(
	allTools []tool.Tool,
	userToolNames map[string]bool,
	hasUserToolTracking bool,
	seen map[string]bool,
	tools []tool.Tool,
) ([]tool.Tool, map[string]bool) {
	for _, tl := range tools {
		name := toolName(tl)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		allTools = append(allTools, tl)
		if hasUserToolTracking {
			if userToolNames == nil {
				userToolNames = make(map[string]bool)
			}
			userToolNames[name] = true
		}
	}
	return allTools, userToolNames
}

func collectToolNames(tools []tool.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, tl := range tools {
		if name := toolName(tl); name != "" {
			names[name] = true
		}
	}
	return names
}

func copyToolNames(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for name, ok := range src {
		dst[name] = ok
	}
	return dst
}

func toolName(tl tool.Tool) string {
	if tl == nil {
		return ""
	}
	decl := tl.Declaration()
	if decl == nil {
		return ""
	}
	return decl.Name
}
