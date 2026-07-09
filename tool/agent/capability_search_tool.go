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
	"sync"

	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// DefaultCapabilitySearchToolName is the default tool name exposed by
// NewCapabilitySearchTool.
const DefaultCapabilitySearchToolName = "tool_search"

const defaultCapabilitySearchDescription = "Search deferred tool and skill " +
	"metadata for a dynamic sub-agent. Use natural-language queries to find " +
	"relevant capabilities, `select:name1,name2` to fetch exact names, or an " +
	"empty query for a compact name catalog. Pass selected names to " +
	"dynamic_agent.tools or dynamic_agent.skills when running tool-backed work."

const defaultCapabilitySearchLimit = 20
const maxCapabilitySearchLimit = 50

type capabilitySearchOptions struct {
	name           string
	description    string
	toolProvider   CapabilitySurfaceProvider
	skillsProvider CapabilitySkillsProvider
	toolAliases    map[string]string
	defaultLimit   int
	cache          *capabilitySearchCache
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
func WithCapabilitySearchDescription(
	description string,
) CapabilitySearchOption {
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

// WithCapabilitySearchToolAliases adds model-facing aliases to the search
// metadata for canonical tool names. Returned results still use the canonical
// Name so callers can pass it directly to dynamic_agent.tools.
func WithCapabilitySearchToolAliases(
	aliases map[string]string,
) CapabilitySearchOption {
	return func(opts *capabilitySearchOptions) {
		opts.toolAliases = normalizeToolAliases(aliases)
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
	Tools      []CapabilityToolSummary  `json:"tools,omitempty"`
	Skills     []CapabilitySkillSummary `json:"skills,omitempty"`
	Groups     []CapabilityNameGroup    `json:"groups,omitempty"`
	Missing    []string                 `json:"missing,omitempty"`
	Total      int                      `json:"total,omitempty"`
	Truncated  bool                     `json:"truncated,omitempty"`
	SearchMode string                   `json:"search_mode,omitempty"`
	Note       string                   `json:"note,omitempty"`
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

// CapabilityNameGroup is a compact name-only catalog section.
type CapabilityNameGroup struct {
	Kind  string   `json:"kind"`
	Names []string `json:"names,omitempty"`
}

// NewCapabilitySearchTool returns a lightweight tool/skill discovery tool.
func NewCapabilitySearchTool(opts ...CapabilitySearchOption) tool.Tool {
	cfg := capabilitySearchOptions{
		name:         DefaultCapabilitySearchToolName,
		description:  defaultCapabilitySearchDescription,
		defaultLimit: defaultCapabilitySearchLimit,
		cache:        &capabilitySearchCache{},
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
	query := strings.TrimSpace(in.Query)
	items := collectCapabilityItems(ctx, parentInv, cfg)
	if selection, ok := parseCapabilitySelectQuery(query); ok {
		selected, missing := selectCapabilityItems(items, selection)
		return buildCapabilitySearchResult(
			selected,
			len(selected),
			limit,
			"select",
			false,
			missing,
			nil,
		)
	}
	if query == "" {
		return buildCapabilitySearchResult(
			items,
			len(items),
			limit,
			"catalog",
			true,
			nil,
			capabilityNameGroups(items),
		)
	}
	index := cfg.cache.indexFor(items)
	matches := index.search(query)
	return buildCapabilitySearchResult(
		matches,
		len(matches),
		limit,
		"bm25",
		false,
		nil,
		nil,
	)
}

func collectCapabilityItems(
	ctx context.Context,
	parentInv *coreagent.Invocation,
	cfg capabilitySearchOptions,
) []capabilitySearchItem {
	items := searchCapabilityTools(
		ctx, parentInv, cfg.toolProvider, cfg.toolAliases)
	items = append(
		items,
		searchCapabilitySkills(ctx, parentInv, cfg.skillsProvider)...,
	)
	sortCapabilityItems(items)
	return items
}

func buildCapabilitySearchResult(
	items []capabilitySearchItem,
	total int,
	limit int,
	mode string,
	includeGroups bool,
	missing []string,
	groups []CapabilityNameGroup,
) CapabilitySearchResult {
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	tools, skills := capabilitySummaries(items)
	if includeGroups && groups == nil {
		groups = capabilityNameGroups(items)
	}
	return CapabilitySearchResult{
		Tools:      tools,
		Skills:     skills,
		Groups:     groups,
		Missing:    missing,
		Total:      total,
		Truncated:  truncated,
		SearchMode: mode,
		Note:       capabilitySearchResultNote,
	}
}

func searchCapabilityTools(
	ctx context.Context,
	parentInv *coreagent.Invocation,
	provider CapabilitySurfaceProvider,
	toolAliases map[string]string,
) []capabilitySearchItem {
	if provider == nil {
		return nil
	}
	tools, _ := provider(ctx, parentInv)
	out := make([]capabilitySearchItem, 0, len(tools))
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
		aliases := aliasesForTool(name, toolAliases)
		out = append(out, capabilitySearchItem{
			kind:        capabilityKindTool,
			Name:        name,
			Description: desc,
			Aliases:     aliases,
			SearchText:  capabilityToolSearchText(decl, aliases),
		})
	}
	return out
}

func aliasesForTool(
	canonical string,
	toolAliases map[string]string,
) []string {
	canonical = strings.TrimSpace(canonical)
	if canonical == "" || len(toolAliases) == 0 {
		return nil
	}
	aliases := make([]string, 0)
	for alias, target := range toolAliases {
		if target == canonical {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	sort.Strings(aliases)
	return aliases
}

func searchCapabilitySkills(
	ctx context.Context,
	parentInv *coreagent.Invocation,
	provider CapabilitySkillsProvider,
) []capabilitySearchItem {
	if provider == nil {
		return nil
	}
	repo := provider(ctx, parentInv)
	if repo == nil {
		return nil
	}
	summaries := skill.SummariesForContext(ctx, repo)
	out := make([]capabilitySearchItem, 0, len(summaries))
	for _, summary := range summaries {
		name := strings.TrimSpace(summary.Name)
		desc := strings.TrimSpace(summary.Description)
		if name == "" {
			continue
		}
		out = append(out, capabilitySearchItem{
			kind:        capabilityKindSkill,
			Name:        name,
			Description: desc,
			SearchText:  capabilitySkillSearchText(name, desc),
		})
	}
	return out
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

const capabilitySearchResultNote = "Pass selected names to " +
	"dynamic_agent.tools or dynamic_agent.skills when running the " +
	"tool-backed task."

type capabilitySearchCache struct {
	mu          sync.Mutex
	fingerprint string
	index       *capabilitySearchIndex
}

func (c *capabilitySearchCache) indexFor(
	items []capabilitySearchItem,
) *capabilitySearchIndex {
	if c == nil {
		return newCapabilitySearchIndex(items)
	}
	fingerprint := capabilityItemsFingerprint(items)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.index != nil && c.fingerprint == fingerprint {
		return c.index
	}
	c.index = newCapabilitySearchIndex(items)
	c.fingerprint = fingerprint
	return c.index
}
