//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import "trpc.group/trpc-go/trpc-agent-go/model"

// Config holds all configurable values for ToolSearch.
// It is mutated by Option functions and then applied when constructing the searcher.
type Config struct {
	// Name is the plugin name used by plugin.Manager.
	// If empty, ToolSearch uses a default name.
	Name          string
	Model         model.Model
	toolKnowledge *ToolKnowledge
	SystemPrompt  string
	MaxTools      int
	AlwaysInclude []string
	// FailOpen controls whether ToolSearch should "fail open" and fallback to
	// the original full tool set (i.e. do nothing)
	FailOpen bool
}

// Option configures ToolSearch by mutating a Config.
type Option func(*Config)

// WithName sets the plugin name for ToolSearch.
// Names must be unique per Runner.
func WithName(name string) Option {
	return func(c *Config) { c.Name = name }
}

// WithToolKnowledge sets the tool knowledge used for tool selection.
func WithToolKnowledge(k *ToolKnowledge) Option {
	return func(c *Config) { c.toolKnowledge = k }
}

// WithSystemPrompt sets the system prompt used for tool selection.
func WithSystemPrompt(prompt string) Option {
	return func(c *Config) { c.SystemPrompt = prompt }
}

// WithMaxTools sets the maximum number of tools to select.
// If maxTools <= 0, the maximum number of tools is defaultMaxTools.
func WithMaxTools(maxTools int) Option {
	return func(c *Config) { c.MaxTools = maxTools }
}

const defaultMaxTools = 10000

// WithAlwaysInclude adds tool names that are always included regardless of
// selection. These do not count against `maxTools`.
func WithAlwaysInclude(names ...string) Option {
	return func(c *Config) {
		c.AlwaysInclude = append(c.AlwaysInclude, names...)
	}
}

// WithFailOpen enables fail-open behavior: ToolSearch will not return an error and will not mutate req.Tools.
func WithFailOpen() Option {
	return func(c *Config) { c.FailOpen = true }
}
