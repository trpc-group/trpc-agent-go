//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package openapi provides a toolset for a given openapi API specification.
package openapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	openapi "github.com/getkin/kin-openapi/openapi3"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// defaultUserAgent is the default user agent for HTTP requests.
	defaultUserAgent = "trpc-agent-go-openapi/1.0"
	// defaultOpenAPIToolSetName is the default name for the OpenAPI tool set.
	defaultOpenAPIToolSetName = "openapi"
	// defaultTimeout is the default timeout for HTTP requests.
	defaultTimeout = 30 * time.Second
)

// Option is a functional option for configuring the OpenAPI tool.
type Option func(*config)

// config holds the configuration for the OpenAPI tool.
type config struct {
	name      string
	userAgent string

	specLoader Loader
	httpClient *http.Client
}

// WithSpecLoader sets the spec loader to use.
func WithSpecLoader(loader Loader) Option {
	return func(c *config) {
		c.specLoader = loader
	}
}

// WithUserAgent sets the user agent for HTTP requests.
func WithUserAgent(userAgent string) Option {
	return func(c *config) {
		c.userAgent = userAgent
	}
}

// WithHTTPClient sets the HTTP client to use.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *config) {
		c.httpClient = httpClient
	}
}

// WithName sets the name of the openAPIToolSet.
func WithName(name string) Option {
	return func(c *config) {
		c.name = name
	}
}

// openAPIToolSet is a set of tools.
type openAPIToolSet struct {
	spec *docProcessor
	name string

	config *config
	tools  []tool.Tool
}

// Tools implements the ToolSet interface.
func (ts *openAPIToolSet) Tools(ctx context.Context) []tool.Tool {
	return ts.tools
}

// Close implements the ToolSet interface.
func (ts *openAPIToolSet) Close() error {
	// No resources to clean up for file tools.
	return nil
}

// Name implements the ToolSet interface.
func (ts *openAPIToolSet) Name() string {
	return ts.config.name
}

// NewToolSet creates a new OpenAPI tool set with the provided options.
func NewToolSet(ctx context.Context, opts ...Option) (tool.ToolSet, error) {
	c := &config{
		userAgent: defaultUserAgent,
		name:      defaultOpenAPIToolSetName,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(c)
	}

	specDoc, err := loadSpec(ctx, c.specLoader)
	if err != nil {
		log.Debugf("load openAPI spec err: %v", err)
		return nil, err
	}
	specProcessor := newDocProcessor(specDoc)
	if err := specProcessor.processOperations(); err != nil {
		return nil, err
	}

	toolSet := &openAPIToolSet{config: c}
	for _, op := range specProcessor.operations {
		tl := newOpenAPITool(c, op)
		toolSet.tools = append(toolSet.tools, tl)
	}

	return toolSet, nil
}

func loadSpec(ctx context.Context, loader Loader) (*openapi.T, error) {
	if loader == nil {
		return nil, fmt.Errorf("OpenAPI spec loader not provided")
	}
	doc, err := loader.Load(ctx)
	if err != nil {
		return nil, err
	}
	if err := doc.Validate(ctx); err != nil {
		return nil, err
	}
	return doc, nil
}
