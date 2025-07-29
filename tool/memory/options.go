//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

// Package memory provides memory-related tools for the agent system.
package memory

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MemoryToolsOptions represents the options for memory tools.
type MemoryToolsOptions struct {
	// Tool configurations.
	AddTool    tool.Tool // AddTool is the add tool.
	UpdateTool tool.Tool // UpdateTool is the update tool.
	DeleteTool tool.Tool // DeleteTool is the delete tool.
	ClearTool  tool.Tool // ClearTool is the clear tool.
	SearchTool tool.Tool // SearchTool is the search tool.
	LoadTool   tool.Tool // LoadTool is the load tool.

	// Service and context.
	memoryService memory.Service // memoryService is the memory service.
	appName       string         // appName is the app name.
	userID        string         // userID is the user id.
}

// MemoryToolsOption is a function that configures MemoryToolsOptions.
type MemoryToolsOption func(*MemoryToolsOptions)

// WithAddTool sets a custom add tool.
func WithAddTool(customTool tool.Tool) MemoryToolsOption {
	return func(o *MemoryToolsOptions) {
		o.AddTool = customTool
	}
}

// WithUpdateTool sets a custom update tool.
func WithUpdateTool(customTool tool.Tool) MemoryToolsOption {
	return func(o *MemoryToolsOptions) {
		o.UpdateTool = customTool
	}
}

// WithDeleteTool sets a custom delete tool.
func WithDeleteTool(customTool tool.Tool) MemoryToolsOption {
	return func(o *MemoryToolsOptions) {
		o.DeleteTool = customTool
	}
}

// WithClearTool sets a custom clear tool.
func WithClearTool(customTool tool.Tool) MemoryToolsOption {
	return func(o *MemoryToolsOptions) {
		o.ClearTool = customTool
	}
}

// WithSearchTool sets a custom search tool.
func WithSearchTool(customTool tool.Tool) MemoryToolsOption {
	return func(o *MemoryToolsOptions) {
		o.SearchTool = customTool
	}
}

// WithLoadTool sets a custom load tool.
func WithLoadTool(customTool tool.Tool) MemoryToolsOption {
	return func(o *MemoryToolsOptions) {
		o.LoadTool = customTool
	}
}

// NewMemoryToolsOptions creates a new MemoryToolsOptions with default settings.
func NewMemoryToolsOptions(memoryService memory.Service, appName string, userID string) *MemoryToolsOptions {
	return &MemoryToolsOptions{
		memoryService: memoryService,
		appName:       appName,
		userID:        userID,
	}
}

// BuildTools builds the tools based on the options.
func (o *MemoryToolsOptions) BuildTools() []tool.Tool {
	var tools []tool.Tool

	// Helper function to add tool if provided or use default.
	addTool := func(customTool tool.Tool, defaultToolFactory func() tool.Tool) {
		if customTool != nil {
			tools = append(tools, customTool)
		} else {
			tools = append(tools, defaultToolFactory())
		}
	}

	// Add tools based on options with lazy loading.
	addTool(o.AddTool, func() tool.Tool { return NewAddTool(o.memoryService, o.appName, o.userID) })
	addTool(o.UpdateTool, func() tool.Tool { return NewUpdateTool(o.memoryService, o.appName, o.userID) })
	addTool(o.DeleteTool, func() tool.Tool { return NewDeleteTool(o.memoryService, o.appName, o.userID) })
	addTool(o.ClearTool, func() tool.Tool { return NewClearTool(o.memoryService, o.appName, o.userID) })
	addTool(o.SearchTool, func() tool.Tool { return NewSearchTool(o.memoryService, o.appName, o.userID) })
	addTool(o.LoadTool, func() tool.Tool { return NewLoadTool(o.memoryService, o.appName, o.userID) })

	return tools
}
