//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"fmt"
	"strings"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// toolSetExposure holds the per-ToolSet exposure configured on an agent. A
// nil value exposes ToolSets with qualified names and leaves deferral to each
// member's own preference.
type toolSetExposure struct {
	nameModes map[string]tool.ToolSetToolNameMode
	deferrals map[string]*itool.Deferral
}

func (o *Options) toolSetExposure() *toolSetExposure {
	return &toolSetExposure{
		nameModes: o.toolSetToolNameModes,
		deferrals: o.deferredToolSets,
	}
}

// namedToolSet wraps toolSet with its configured name mode and deferral.
func (e *toolSetExposure) namedToolSet(toolSet tool.ToolSet) *itool.NamedToolSet {
	if e == nil {
		return itool.NewNamedToolSetWithMode(toolSet, tool.ToolSetToolNameModeQualified)
	}
	named := itool.NewNamedToolSetWithMode(toolSet, toolSetToolNameMode(e.nameModes, toolSet))
	if toolSet == nil || len(e.deferrals) == 0 {
		return named
	}
	return named.WithDeferral(e.deferrals[strings.TrimSpace(toolSet.Name())])
}

func validateAndNormalizeDeferredToolSets(options *Options) error {
	if options == nil || len(options.deferredToolSets) == 0 {
		return nil
	}
	normalized := make(map[string]*itool.Deferral, len(options.deferredToolSets))
	registeredNames := registeredToolSetNames(options)
	for rawName, deferral := range options.deferredToolSets {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return fmt.Errorf("tool set name for deferred tools must not be empty")
		}
		if !registeredNames[name] {
			return fmt.Errorf("deferred tool set %q is not registered", name)
		}
		normalized[name] = deferral
	}
	options.deferredToolSets = normalized
	return nil
}

// applyDeferredTools exposes tools registered with WithTools with the agent
// deferral decision. Without a decision the tools are returned unchanged.
func applyDeferredTools(tools []tool.Tool, deferral *itool.Deferral) []tool.Tool {
	if deferral == nil || len(tools) == 0 {
		return tools
	}
	out := make([]tool.Tool, len(tools))
	for i, t := range tools {
		out[i] = itool.WithDeferral(t, deferral)
	}
	return out
}
