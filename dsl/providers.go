// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package dsl defines high-level abstractions for working with engine DSL
// graphs. Providers are runtime dependencies used by the compiler and builtin
// components to resolve logical tool identifiers into concrete implementations.
package dsl

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ToolProvider resolves logical tool names into tool.Tool instances.
// Implementations can apply user/tenant specific access control, dynamic
// loading or caching strategies. The DSL compiler and runtime helpers are
// agnostic to how tools are sourced as long as they satisfy this interface.
type ToolProvider interface {
	// Get returns a single tool by name or an error if the tool is not found.
	Get(name string) (tool.Tool, error)

	// GetMultiple returns a map of name -> tool for the provided names or an
	// error if any of the requested tools cannot be resolved.
	GetMultiple(names []string) (map[string]tool.Tool, error)

	// GetAll returns all tools that are visible through this provider.
	// The returned map may be a defensive copy; callers should treat it as
	// read‑only.
	GetAll() map[string]tool.Tool
}

// ToolSetProvider resolves logical ToolSet names into tool.ToolSet instances.
// It mirrors ToolProvider but operates on collections of tools instead of
// individual tools. Implementations can decide how ToolSets are scoped (for
// example per-tenant file ToolSets with different base directories).
type ToolSetProvider interface {
	// Get returns a single ToolSet by name or an error if the ToolSet is not found.
	Get(name string) (tool.ToolSet, error)

	// GetMultiple returns a map of name -> ToolSet for the provided names or an
	// error if any of the requested ToolSets cannot be resolved.
	GetMultiple(names []string) (map[string]tool.ToolSet, error)

	// GetAll returns all ToolSets that are visible through this provider.
	// The returned map may be a defensive copy; callers should treat it as
	// read‑only.
	GetAll() map[string]tool.ToolSet
}
