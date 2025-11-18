//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package search provides a Google Search tool set.
package search

import (
	"context"
	"fmt"

	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type config struct {
	apiKey   string
	engineID string
	baseURL  string
	size     int
	offset   int
	lang     string
}

// Option is a function that configures the Google search tool.
type Option func(*config)

// WithAPIKey sets the API key for the Google search tool.
func WithAPIKey(key string) Option {
	return func(c *config) {
		c.apiKey = key
	}
}

// WithEngineID sets the search engine ID for the Google search tool.
func WithEngineID(id string) Option {
	return func(c *config) {
		c.engineID = id
	}
}

// WithBaseURL sets the base URL for the Google search tool.
func WithBaseURL(url string) Option {
	return func(c *config) {
		c.baseURL = url
	}
}

// WithSize sets the size for the Google search tool.
// Number of search results to return.
// Default is 5.
func WithSize(size int) Option {
	return func(c *config) {
		c.size = size
	}
}

// WithOffset sets the offset for the Google search tool.
// The index of the first result to return.
// Default is 0.
func WithOffset(offset int) Option {
	return func(c *config) {
		c.offset = offset
	}
}

// WithLanguage sets the language for the Google search tool.
// Default is "en".
func WithLanguage(lang string) Option {
	return func(c *config) {
		c.lang = lang
	}
}

// ToolSet represents a Google search tool
type ToolSet struct {
	srv   *customsearch.Service
	name  string
	cfg   *config
	tools []tool.Tool
}

// Tools implements the ToolSet interface.
func (t *ToolSet) Tools(ctx context.Context) []tool.Tool {
	return t.tools
}

// Close implements the ToolSet interface.
func (t *ToolSet) Close() error {
	return nil
}

// Name implements the ToolSet interface.
func (t *ToolSet) Name() string {
	return t.name
}

// NewToolSet creates a new Google Search with the provided options.
func NewToolSet(ctx context.Context, opts ...Option) (*ToolSet, error) {
	cfg := &config{
		size:   5,
		offset: 0,
		lang:   "en",
	}

	for _, opt := range opts {
		opt(cfg)
	}
	t := &ToolSet{
		name: "google",
		cfg:  cfg,
	}

	var tools []tool.Tool
	clientOpts := make([]option.ClientOption, 0)
	if t.cfg.apiKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	clientOpts = append(clientOpts, option.WithAPIKey(t.cfg.apiKey))
	if t.cfg.engineID == "" {
		return nil, fmt.Errorf("search engine id is required")
	}
	if t.cfg.baseURL != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(t.cfg.baseURL))
	}
	svr, err := customsearch.NewService(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	t.srv = svr
	tools = append(tools, function.NewFunctionTool(
		t.search,
		function.WithName("search"),
		function.WithDescription("Grounding with Google Search connects the LLM model to real-time web content and works with all available languages. "+
			"This allows the model to provide more accurate answers and cite verifiable sources beyond its knowledge cutoff."),
	))
	t.tools = tools
	return t, nil
}
