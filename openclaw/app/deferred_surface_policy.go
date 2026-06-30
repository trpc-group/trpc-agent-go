//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	deferToolSurfaceModeOff  = "off"
	deferToolSurfaceModeOn   = "on"
	deferToolSurfaceModeAuto = "auto"

	defaultDeferToolSurfaceThresholdChars = 4000
)

var defaultDeferToolSurfaceDirectTools = []string{
	configKeyExecCommand,
	configKeyWriteStdin,
	configKeyKillSession,
}

func normalizeDeferToolSurfaceMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "off", "false", "never", "disabled":
		return deferToolSurfaceModeOff, nil
	case "on", "true", "always", "enabled":
		return deferToolSurfaceModeOn, nil
	case deferToolSurfaceModeAuto:
		return deferToolSurfaceModeAuto, nil
	default:
		return "", fmt.Errorf(
			"invalid defer tool surface mode %q: want off|on|auto",
			raw,
		)
	}
}

func resolveDeferredToolSurface(
	cfg agentConfig,
	baseTools []tool.Tool,
	toolSets []tool.ToolSet,
) (bool, []tool.Tool, error) {
	mode, err := normalizeDeferToolSurfaceMode(cfg.DeferToolSurfaceMode)
	if err != nil {
		return false, nil, err
	}
	if cfg.DeferToolSurface {
		mode = deferToolSurfaceModeOn
	}
	enabled := false
	switch mode {
	case deferToolSurfaceModeOn:
		enabled = true
	case deferToolSurfaceModeAuto:
		threshold := deferToolSurfaceThresholdChars(
			cfg.DeferToolSurfaceThresholdChars,
		)
		enabled = toolSurfaceDeclarationChars(
			context.Background(),
			baseTools,
			toolSets,
		) >= threshold
	case deferToolSurfaceModeOff:
		enabled = false
	}
	if !enabled {
		return false, nil, nil
	}
	return true, directToolSurfaceTools(
		context.Background(),
		directToolSurfaceNames(
			deferToolSurfaceDefaultDirectToolsEnabled(
				cfg.DeferToolSurfaceDefaultDirectTools,
			),
			cfg.DeferToolSurfaceDirectTools,
		),
		baseTools,
		toolSets,
	), nil
}

func deferToolSurfaceThresholdChars(value int) int {
	if value > 0 {
		return value
	}
	return defaultDeferToolSurfaceThresholdChars
}

func deferToolSurfaceDefaultDirectToolsEnabled(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func directToolSurfaceNames(includeDefaults bool, names []string) []string {
	all := make(
		[]string,
		0,
		len(defaultDeferToolSurfaceDirectTools)+len(names),
	)
	if includeDefaults {
		all = append(all, defaultDeferToolSurfaceDirectTools...)
	}
	all = append(all, names...)
	return normalizeStringList(all)
}

func toolSurfaceDeclarationChars(
	ctx context.Context,
	baseTools []tool.Tool,
	toolSets []tool.ToolSet,
) int {
	total := 0
	for _, t := range deferredCapabilityTools(ctx, baseTools, toolSets) {
		total += toolDeclarationChars(t)
	}
	return total
}

func toolDeclarationChars(t tool.Tool) int {
	if t == nil {
		return 0
	}
	decl := t.Declaration()
	if decl == nil {
		return 0
	}
	if data, err := json.Marshal(decl); err == nil {
		return len(data)
	}
	return len(decl.Name) + len(decl.Description)
}

func directToolSurfaceTools(
	ctx context.Context,
	names []string,
	baseTools []tool.Tool,
	toolSets []tool.ToolSet,
) []tool.Tool {
	wanted := stringSet(normalizeStringList(names))
	if len(wanted) == 0 {
		return nil
	}
	allTools := deferredCapabilityTools(ctx, baseTools, toolSets)
	out := make([]tool.Tool, 0, len(wanted))
	seen := map[string]bool{}
	for _, t := range allTools {
		name := strings.TrimSpace(toolDeclName(t))
		if name == "" || !wanted[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, t)
	}
	return out
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
