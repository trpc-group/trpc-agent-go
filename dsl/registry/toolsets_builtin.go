//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"trpc.group/trpc-go/trpc-agent-go/tool/file"
)

// DefaultToolSetRegistry is the global default ToolSet registry.
// Built-in ToolSets are automatically registered here when you import:
//
//	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
var DefaultToolSetRegistry = NewToolSetRegistry()

// RegisterBuiltinToolSets registers all built-in ToolSets to the given registry.
func RegisterBuiltinToolSets(registry *ToolSetRegistry) error {
	// Register file ToolSet with current directory as base
	fileToolSet, err := file.NewToolSet(
		file.WithBaseDir("."),
		file.WithName("file"),
	)
	if err != nil {
		return err
	}
	registry.MustRegister("file", fileToolSet)

	return nil
}

// NewToolSetRegistryWithBuiltins creates a new ToolSetRegistry with built-in ToolSets pre-registered.
func NewToolSetRegistryWithBuiltins() (*ToolSetRegistry, error) {
	reg := NewToolSetRegistry()
	if err := RegisterBuiltinToolSets(reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// GetBuiltinToolSets returns a list of built-in ToolSet names.
func GetBuiltinToolSets() []string {
	return []string{
		"file",
	}
}

// IsBuiltinToolSet checks if a ToolSet name is a built-in ToolSet.
func IsBuiltinToolSet(name string) bool {
	builtins := GetBuiltinToolSets()
	for _, builtin := range builtins {
		if name == builtin {
			return true
		}
	}
	return false
}

// GetBuiltinToolSet returns a built-in ToolSet by name.
// Returns nil if the ToolSet is not a built-in ToolSet.
func GetBuiltinToolSet(name string) (interface{}, error) {
	return DefaultToolSetRegistry.Get(name)
}

