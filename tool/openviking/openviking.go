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
	"fmt"
	"net/http"
	"time"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
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
	baseURL   string
	apiKey    string
	account   string
	user      string
	agent     string
	timeout   time.Duration
	profile   Profile
	toolNames []ToolName
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

// WithTimeout sets the per-request timeout. The default is no timeout, so
// long-running calls (e.g. viking_add_resource) rely on context cancellation
// instead. Non-positive values are ignored.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithProfile selects the tool profile (retrieval, agent, admin). It is the
// primary way to choose which tools are exposed; use the exported Tool*
// constants with WithSpecificTools only when a profile does not fit.
//
// Precedence: WithSpecificTools, when non-empty, fully overrides the profile's tool
// list. The profile then only governs the viking_forget safety gate (see
// WithSpecificTools). When WithSpecificTools is not set, the profile alone decides the tools.
func WithProfile(p Profile) Option {
	return func(c *config) { c.profile = p }
}

// WithSpecificTools explicitly selects the tools to expose, using the exported Tool*
// constants (e.g. ToolSearch, ToolRead). When non-empty it takes precedence
// over WithProfile and fully replaces the profile's tool list; unknown names
// make NewToolSet fail fast.
//
// The profile is not fully ignored: the destructive ToolForget is dropped
// unless WithProfile(ProfileAdmin) is also set, so naming it here cannot bypass
// the admin gate.
func WithSpecificTools(names ...ToolName) Option {
	return func(c *config) { c.toolNames = names }
}

// ToolSet exposes OpenViking primitives as agent tools and implements
// tool.ToolSet.
type ToolSet struct {
	client *client.Client
	tools  []tool.Tool
}

// NewToolSet creates a ToolSet backed by an OpenViking server.
func NewToolSet(opts ...Option) (*ToolSet, error) {
	cfg := &config{profile: ProfileAgent}
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate profile to prevent typos from silently exposing write tools.
	switch cfg.profile {
	case ProfileRetrieval, ProfileAgent, ProfileAdmin:
		// valid
	default:
		return nil, fmt.Errorf("openviking: unknown profile %q: must be retrieval, agent, or admin", cfg.profile)
	}

	// Default to no client-level timeout (cfg.timeout == 0); callers control
	// cancellation via context, and WithTimeout can opt into a hard deadline.
	c := client.New(client.Config{
		BaseURL:    cfg.baseURL,
		APIKey:     cfg.apiKey,
		Account:    cfg.account,
		User:       cfg.user,
		Agent:      cfg.agent,
		HTTPClient: &http.Client{Timeout: cfg.timeout},
	})

	tools, err := buildTools(c, selectToolNames(cfg))
	if err != nil {
		return nil, err
	}
	return &ToolSet{client: c, tools: tools}, nil
}

// Tools implements tool.ToolSet. Each tool is wrapped in an unprefixed NamedTool
// so that llmagent recognizes them as user tools and applies filters correctly,
// while preserving the native viking_* names.
func (s *ToolSet) Tools(_ context.Context) []tool.Tool {
	wrapped := make([]tool.Tool, len(s.tools))
	for i, t := range s.tools {
		wrapped[i] = itool.NewUnprefixedNamedTool(t)
	}
	return wrapped
}

// Name implements tool.ToolSet. It returns an empty string on purpose so the
// tools keep their native viking_* names without a toolset prefix; the agent
// instructions, tool descriptions, and hints all reference these unprefixed
// names, and a non-empty name would make llmagent register openviking_viking_*.
func (s *ToolSet) Name() string { return "" }

// Close implements tool.ToolSet.
func (s *ToolSet) Close() error { return s.client.Close() }

// selectToolNames resolves the final ordered tool name list from the config.
func selectToolNames(cfg *config) []ToolName {
	// The destructive viking_forget tool is gated behind the admin profile,
	// regardless of how tools are selected.
	if len(cfg.toolNames) > 0 {
		if cfg.profile == ProfileAdmin {
			return cfg.toolNames
		}
		return removeTool(cfg.toolNames, ToolForget)
	}
	retrieval := []ToolName{
		ToolFind,
		ToolSearch,
		ToolBrowse,
		ToolRead,
		ToolGrep,
		ToolHealth,
	}
	switch cfg.profile {
	case ProfileRetrieval:
		return retrieval
	case ProfileAdmin:
		return append(retrieval, ToolStore, ToolAddResource, ToolAddSkill, ToolForget)
	case ProfileAgent:
		return append(retrieval, ToolStore, ToolAddResource, ToolAddSkill)
	}
	// Unreachable: NewToolSet validates profile. Return retrieval-only as a safe fallback.
	return retrieval
}

// removeTool returns items without any element equal to target.
func removeTool(items []ToolName, target ToolName) []ToolName {
	out := make([]ToolName, 0, len(items))
	for _, s := range items {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}

func containsTool(names []ToolName, target ToolName) bool {
	for _, n := range names {
		if n == target {
			return true
		}
	}
	return false
}
