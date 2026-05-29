//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolorder provides deterministic ordering helpers for model tools.
package toolorder

import (
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// SortedTools returns tools in deterministic request order.
//
// Model adapters and telemetry both use this helper so the exported tool
// definitions match the order sent to model providers.
func SortedTools(tools map[string]tool.Tool) []tool.Tool {
	names := make([]string, 0, len(tools))
	for name, t := range tools {
		if t == nil || t.Declaration() == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		result = append(result, tools[name])
	}
	return result
}
