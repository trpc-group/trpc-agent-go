//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// EmbeddingText renders the visible name, description, and top-level parameters
// of a tool declaration for semantic search.
func EmbeddingText(name string, decl *tool.Declaration) string {
	parts := []string{
		fmt.Sprintf("Tool: %s", name),
		fmt.Sprintf("Description: %s", decl.Description),
	}
	if decl.InputSchema != nil && len(decl.InputSchema.Properties) > 0 {
		keys := make([]string, 0, len(decl.InputSchema.Properties))
		for name := range decl.InputSchema.Properties {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		descs := make([]string, 0, len(keys))
		for _, name := range keys {
			info := decl.InputSchema.Properties[name]
			if info == nil {
				continue
			}
			paramType := strings.TrimSpace(info.Type)
			if paramType == "" {
				if info.Items != nil {
					paramType = "array"
				} else if len(info.Properties) > 0 {
					paramType = "object"
				}
			}
			descs = append(descs, fmt.Sprintf("%s (%s): %s", name, paramType, strings.TrimSpace(info.Description)))
		}
		if len(descs) > 0 {
			parts = append(parts, "Parameters: "+strings.Join(descs, ", "))
		}
	}
	return strings.Join(parts, "\n")
}
