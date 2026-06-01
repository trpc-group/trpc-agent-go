//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package openviking exposes the OpenViking context database
// (https://github.com/volcengine/OpenViking) as a set of agent tools.
//
// OpenViking organizes an agent's memories, resources, and skills as a virtual
// filesystem under viking:// URIs with tiered (L0/L1/L2) loading. Rather than a
// one-shot retriever, the tools follow OpenViking's native "search then read"
// pattern: the model calls viking_search/viking_find to locate relevant URIs
// (with summaries) and then viking_read to fetch full content only where needed,
// which is what keeps token usage low.
package openviking

import (
	"context"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/openviking/internal/client"
)

// Profile selects which tools a ToolSet exposes.
type Profile string

const (
	// ProfileRetrieval exposes only read-only retrieval tools.
	ProfileRetrieval Profile = "retrieval"
	// ProfileAgent exposes retrieval plus write tools (store, add_resource,
	// add_skill). This is the default.
	ProfileAgent Profile = "agent"
	// ProfileAdmin exposes the agent tools plus destructive operations.
	ProfileAdmin Profile = "admin"
)

// config holds resolved ToolSet configuration.
type config struct {
	baseURL     string
	apiKey      string
	account     string
	user        string
	agent       string
	httpClient  *http.Client
	timeout     time.Duration
	profile     Profile
	toolNames   []string
	allowForget bool
}

// Option configures a ToolSet.
type Option func(*config)

// WithBaseURL sets the OpenViking server URL (default http://localhost:1933).
func WithBaseURL(url string) Option {
	return func(c *config) { c.baseURL = url }
}

// WithAPIKey sets the API key sent as the X-API-Key header.
func WithAPIKey(key string) Option {
	return func(c *config) { c.apiKey = key }
}

// WithAccount sets the OpenViking account identity.
func WithAccount(account string) Option {
	return func(c *config) { c.account = account }
}

// WithUser sets the OpenViking user identity.
func WithUser(user string) Option {
	return func(c *config) { c.user = user }
}

// WithAgent sets the OpenViking agent identity.
func WithAgent(agent string) Option {
	return func(c *config) { c.agent = agent }
}

// WithHTTPClient sets a custom HTTP client used to call OpenViking.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) { c.httpClient = hc }
}

// WithTimeout sets the per-request timeout. Ignored when a custom HTTP client
// is provided. Non-positive values are ignored.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithProfile selects the tool profile (retrieval, agent, admin).
func WithProfile(p Profile) Option {
	return func(c *config) { c.profile = p }
}

// WithToolNames explicitly selects the tools to expose by name. When set, it
// overrides the profile selection.
func WithToolNames(names ...string) Option {
	return func(c *config) { c.toolNames = names }
}

// WithAllowForget additionally exposes the destructive viking_forget tool.
func WithAllowForget(allow bool) Option {
	return func(c *config) { c.allowForget = allow }
}

// ToolSet exposes OpenViking primitives as agent tools and implements
// tool.ToolSet.
type ToolSet struct {
	client *client.Client
	tools  []tool.Tool
}

// NewToolSet creates a ToolSet backed by an OpenViking server.
func NewToolSet(opts ...Option) (*ToolSet, error) {
	cfg := &config{
		profile: ProfileAgent,
		timeout: client.DefaultTimeout,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	httpClient := cfg.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.timeout}
	}

	c := client.New(client.Config{
		BaseURL:    cfg.baseURL,
		APIKey:     cfg.apiKey,
		Account:    cfg.account,
		User:       cfg.user,
		Agent:      cfg.agent,
		HTTPClient: httpClient,
	})

	tools, err := buildTools(c, selectToolNames(cfg))
	if err != nil {
		return nil, err
	}
	return &ToolSet{client: c, tools: tools}, nil
}

// Tools implements tool.ToolSet.
func (s *ToolSet) Tools(_ context.Context) []tool.Tool { return s.tools }

// Name implements tool.ToolSet. It returns an empty string on purpose so the
// tools keep their native viking_* names without a toolset prefix; the agent
// instructions, tool descriptions, and hints all reference these unprefixed
// names, and a non-empty name would make llmagent register openviking_viking_*.
func (s *ToolSet) Name() string { return "" }

// Close implements tool.ToolSet.
func (s *ToolSet) Close() error { return s.client.Close() }

// selectToolNames resolves the final ordered tool name list from the config.
func selectToolNames(cfg *config) []string {
	// The destructive viking_forget tool is gated behind the admin profile or
	// an explicit WithAllowForget(true), regardless of how tools are selected.
	forgetAllowed := cfg.profile == ProfileAdmin || cfg.allowForget
	if len(cfg.toolNames) > 0 {
		if forgetAllowed {
			return cfg.toolNames
		}
		return removeString(cfg.toolNames, toolForget)
	}
	retrieval := []string{
		toolFind,
		toolSearch,
		toolBrowse,
		toolRead,
		toolGrep,
		toolHealth,
	}
	var names []string
	switch cfg.profile {
	case ProfileRetrieval:
		names = retrieval
	case ProfileAdmin:
		names = append(retrieval, toolStore, toolAddResource, toolAddSkill, toolForget)
	default: // ProfileAgent
		names = append(retrieval, toolStore, toolAddResource, toolAddSkill)
	}
	if forgetAllowed && !contains(names, toolForget) {
		names = append(names, toolForget)
	}
	return names
}

// removeString returns items without any element equal to target.
func removeString(items []string, target string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}

func contains(names []string, target string) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}
