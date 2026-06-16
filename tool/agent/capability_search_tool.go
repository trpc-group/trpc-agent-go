//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"sort"
	"strings"

	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// DefaultCapabilitySearchToolName is the default tool name exposed by
// NewCapabilitySearchTool.
const DefaultCapabilitySearchToolName = "tool_search"

const defaultCapabilitySearchDescription = "Search the tools and skills " +
	"available to a dynamic sub-agent. Use this to discover exact tool or skill " +
	"names, then pass those names to dynamic_agent when running tool-backed " +
	"work."

const defaultCapabilitySearchLimit = 20
const maxCapabilitySearchLimit = 50

type capabilitySearchOptions struct {
	name           string
	description    string
	toolProvider   CapabilitySurfaceProvider
	skillsProvider CapabilitySkillsProvider
	defaultLimit   int
}

// CapabilitySearchOption configures NewCapabilitySearchTool.
type CapabilitySearchOption func(*capabilitySearchOptions)

// WithCapabilitySearchName overrides the model-facing search tool name.
func WithCapabilitySearchName(name string) CapabilitySearchOption {
	return func(opts *capabilitySearchOptions) {
		opts.name = strings.TrimSpace(name)
	}
}

// WithCapabilitySearchDescription overrides the search tool description.
func WithCapabilitySearchDescription(description string) CapabilitySearchOption {
	return func(opts *capabilitySearchOptions) {
		opts.description = strings.TrimSpace(description)
	}
}

// WithCapabilitySearchProvider sets the tool capability provider.
func WithCapabilitySearchProvider(
	provider CapabilitySurfaceProvider,
) CapabilitySearchOption {
	return func(opts *capabilitySearchOptions) {
		opts.toolProvider = provider
	}
}

// WithCapabilitySearchSkillsProvider sets the skill capability provider.
func WithCapabilitySearchSkillsProvider(
	provider CapabilitySkillsProvider,
) CapabilitySearchOption {
	return func(opts *capabilitySearchOptions) {
		opts.skillsProvider = provider
	}
}

// WithCapabilitySearchDefaultLimit sets the default result limit.
func WithCapabilitySearchDefaultLimit(limit int) CapabilitySearchOption {
	return func(opts *capabilitySearchOptions) {
		opts.defaultLimit = normalizeCapabilitySearchLimit(limit)
	}
}

// CapabilitySearchInput is the input schema for tool_search.
type CapabilitySearchInput struct {
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// CapabilitySearchResult is the output schema for tool_search.
type CapabilitySearchResult struct {
	Tools     []CapabilityToolSummary  `json:"tools,omitempty"`
	Skills    []CapabilitySkillSummary `json:"skills,omitempty"`
	Truncated bool                     `json:"truncated,omitempty"`
	Note      string                   `json:"note,omitempty"`
}

// CapabilityToolSummary describes one available tool capability.
type CapabilityToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CapabilitySkillSummary describes one available skill capability.
type CapabilitySkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// NewCapabilitySearchTool returns a lightweight tool/skill discovery tool.
func NewCapabilitySearchTool(opts ...CapabilitySearchOption) tool.Tool {
	cfg := capabilitySearchOptions{
		name:         DefaultCapabilitySearchToolName,
		description:  defaultCapabilitySearchDescription,
		defaultLimit: defaultCapabilitySearchLimit,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.name == "" {
		cfg.name = DefaultCapabilitySearchToolName
	}
	if cfg.description == "" {
		cfg.description = defaultCapabilitySearchDescription
	}
	return function.NewFunctionTool(
		func(ctx context.Context, in CapabilitySearchInput) (
			CapabilitySearchResult,
			error,
		) {
			return searchCapabilities(ctx, cfg, in), nil
		},
		function.WithName(cfg.name),
		function.WithDescription(cfg.description),
	)
}

func searchCapabilities(
	ctx context.Context,
	cfg capabilitySearchOptions,
	in CapabilitySearchInput,
) CapabilitySearchResult {
	if ctx == nil {
		ctx = context.Background()
	}
	parentInv, _ := coreagent.InvocationFromContext(ctx)
	limit := normalizeCapabilitySearchLimit(in.Limit)
	if limit == 0 {
		limit = cfg.defaultLimit
	}
	query := strings.ToLower(strings.TrimSpace(in.Query))
	tools := searchCapabilityTools(ctx, parentInv, cfg.toolProvider, query)
	skills := searchCapabilitySkills(ctx, parentInv, cfg.skillsProvider, query)
	total := len(tools) + len(skills)
	truncated := total > limit
	for total > limit && len(skills) > 0 {
		skills = skills[:len(skills)-1]
		total--
	}
	for total > limit && len(tools) > 0 {
		tools = tools[:len(tools)-1]
		total--
	}
	return CapabilitySearchResult{
		Tools:     tools,
		Skills:    skills,
		Truncated: truncated,
		Note: "Pass selected names to dynamic_agent.tools or " +
			"dynamic_agent.skills when running the tool-backed task.",
	}
}

func searchCapabilityTools(
	ctx context.Context,
	parentInv *coreagent.Invocation,
	provider CapabilitySurfaceProvider,
	query string,
) []CapabilityToolSummary {
	if provider == nil {
		return nil
	}
	tools, _ := provider(ctx, parentInv)
	out := make([]CapabilityToolSummary, 0, len(tools))
	seen := map[string]bool{}
	for _, t := range tools {
		if t == nil {
			continue
		}
		decl := t.Declaration()
		if decl == nil || strings.TrimSpace(decl.Name) == "" {
			continue
		}
		name := strings.TrimSpace(decl.Name)
		if seen[name] {
			continue
		}
		seen[name] = true
		desc := strings.TrimSpace(decl.Description)
		if !capabilitySearchMatches(query, name, desc) {
			continue
		}
		out = append(out, CapabilityToolSummary{
			Name:        name,
			Description: desc,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func searchCapabilitySkills(
	ctx context.Context,
	parentInv *coreagent.Invocation,
	provider CapabilitySkillsProvider,
	query string,
) []CapabilitySkillSummary {
	if provider == nil {
		return nil
	}
	repo := provider(ctx, parentInv)
	if repo == nil {
		return nil
	}
	summaries := skill.SummariesForContext(ctx, repo)
	out := make([]CapabilitySkillSummary, 0, len(summaries))
	for _, summary := range summaries {
		name := strings.TrimSpace(summary.Name)
		desc := strings.TrimSpace(summary.Description)
		if name == "" || !capabilitySearchMatches(query, name, desc) {
			continue
		}
		out = append(out, CapabilitySkillSummary{
			Name:        name,
			Description: desc,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func capabilitySearchMatches(query string, values ...string) bool {
	if query == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func normalizeCapabilitySearchLimit(limit int) int {
	if limit < 0 {
		return 0
	}
	if limit > maxCapabilitySearchLimit {
		return maxCapabilitySearchLimit
	}
	return limit
}
