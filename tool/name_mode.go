//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

// ToolNameMode controls how a ToolSet's tools are named when exposed to a
// model. The ToolSet name remains the stable identity used by the host.
//
// A ToolSet can optionally implement a ToolNameMode() ToolNameMode method to
// select the mode. ToolSets that do not implement that method use the
// qualified mode for backward compatibility.
type ToolNameMode int

const (
	// ToolNameModeQualified prefixes each tool name with the ToolSet name.
	// This is the default and preserves the existing naming behavior.
	ToolNameModeQualified ToolNameMode = iota
	// ToolNameModeOriginal exposes each tool using its original declaration
	// name without adding the ToolSet name as a prefix.
	ToolNameModeOriginal
)

// ToolNameModeOf returns the model-facing naming mode configured by a ToolSet.
// ToolSets that do not implement the optional ToolNameMode() method use
// ToolNameModeQualified for backward compatibility.
func ToolNameModeOf(toolSet ToolSet) ToolNameMode {
	if toolSet == nil {
		return ToolNameModeQualified
	}
	provider, ok := toolSet.(interface {
		ToolNameMode() ToolNameMode
	})
	if !ok {
		return ToolNameModeQualified
	}
	switch provider.ToolNameMode() {
	case ToolNameModeOriginal:
		return ToolNameModeOriginal
	default:
		return ToolNameModeQualified
	}
}
