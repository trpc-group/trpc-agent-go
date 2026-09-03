//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func validateAndNormalizeToolSetToolNameModes(options *Options) error {
	if options == nil || len(options.toolSetToolNameModes) == 0 {
		return nil
	}
	normalized := make(map[string]tool.ToolSetToolNameMode, len(options.toolSetToolNameModes))
	registeredNames := registeredToolSetNames(options)
	for rawName, mode := range options.toolSetToolNameModes {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return fmt.Errorf("tool set name for tool name mode must not be empty")
		}
		switch mode {
		case tool.ToolSetToolNameModeQualified, tool.ToolSetToolNameModeOriginal:
		default:
			return fmt.Errorf("unsupported tool name mode %d for tool set %q", mode, name)
		}
		if !registeredNames[name] {
			return fmt.Errorf("tool set %q is not registered", name)
		}
		normalized[name] = mode
	}
	options.toolSetToolNameModes = normalized
	return nil
}

func registeredToolSetNames(options *Options) map[string]bool {
	names := make(map[string]bool)
	if options == nil {
		return names
	}
	for _, toolSets := range [][]tool.ToolSet{
		options.ToolSets,
		options.activatableToolSets,
	} {
		for _, toolSet := range toolSets {
			if toolSet == nil {
				continue
			}
			if name := strings.TrimSpace(toolSet.Name()); name != "" {
				names[name] = true
			}
		}
	}
	return names
}

func toolSetToolNameMode(
	toolSetNameModes map[string]tool.ToolSetToolNameMode,
	toolSet tool.ToolSet,
) tool.ToolSetToolNameMode {
	if toolSet == nil {
		return tool.ToolSetToolNameModeQualified
	}
	name := strings.TrimSpace(toolSet.Name())
	if mode, ok := toolSetNameModes[name]; ok {
		return mode
	}
	return tool.ToolSetToolNameModeQualified
}
