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
// It is mutated by Option functions and then applied when constructing the toolIndex.
type Config struct {
	Model         model.Model
	toolKnowledge *ToolKnowledge
	SystemPrompt  string
	MaxTools      int
	AlwaysInclude []string
}

// Option configures ToolSearch by mutating a Config.
type Option func(*Config)

// WithModel sets the model used for tool selection.
func WithModel(m model.Model) Option {
	return func(c *Config) { c.Model = m }
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
// If maxTools <= 0, there is no limit.
func WithMaxTools(maxTools int) Option {
	return func(c *Config) { c.MaxTools = maxTools }
}

// WithAlwaysInclude adds tool names that are always included regardless of
// selection. These do not count against `maxTools`.
func WithAlwaysInclude(names ...string) Option {
	return func(c *Config) {
		c.AlwaysInclude = append(c.AlwaysInclude, names...)
	}
}
