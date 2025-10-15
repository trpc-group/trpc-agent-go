//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import "context"

// ToolSet defines an interface for managing a set of tools.
// It provides methods to retrieve the current tools and to perform cleanup.
type ToolSet interface {
	// Tools returns a slice of Tool instances available in the set based on the provided context.
	Tools(context.Context) []Tool

	// Close releases any resources held by the ToolSet.
	Close() error

	// Name returns the name of the ToolSet for identification and conflict resolution.
	Name() string
}
