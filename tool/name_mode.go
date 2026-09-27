//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

// ToolSetToolNameMode controls how a ToolSet's tools are named when exposed to
// a model. The ToolSet name remains the stable identity used by the host. An
// agent can select the mode for each registered ToolSet without requiring the
// ToolSet implementation to implement any additional interface.
type ToolSetToolNameMode int

const (
	// ToolSetToolNameModeQualified prefixes each tool name with the ToolSet name.
	// This is the default and preserves the existing naming behavior.
	ToolSetToolNameModeQualified ToolSetToolNameMode = iota
	// ToolSetToolNameModeOriginal exposes each tool using its original declaration
	// name without adding the ToolSet name as a prefix.
	ToolSetToolNameModeOriginal
)
