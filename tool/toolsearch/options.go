//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultSearchToolName = "tool_search"
	defaultMaxResults     = 8
	defaultMaxLoaded      = 32
	defaultCatalogTTL     = 5 * time.Second
)

// StateScope controls whether loaded-tool state is kept only for the current
// invocation or also persisted to the session for later user turns.
type StateScope string

const (
	// StateScopeInvocation keeps loaded tools only in invocation state.
	StateScopeInvocation StateScope = "invocation"
	// StateScopeSession persists loaded tools to session state and rehydrates the
	// next invocation from that state.
	StateScopeSession StateScope = "session"
)

// CatalogRefreshPolicy controls how deferred catalogs are rebuilt.
type CatalogRefreshPolicy struct {
	// TTL controls how long one built catalog snapshot may be reused before the
	// ToolSet consults source ToolSets again. Zero disables caching.
	TTL time.Duration
}

// Analyzer tokenizes text for local lexical retrieval.
type Analyzer interface {
	Analyze(text string) []string
}

// AnalyzerFunc adapts a function to Analyzer.
type AnalyzerFunc func(text string) []string

// Analyze implements Analyzer.
func (f AnalyzerFunc) Analyze(text string) []string {
	return f(text)
}

type config struct {
	name                string
	searchToolName      string
	searchToolDesc      string
	stateNamespace      string
	maxResults          int
	maxLoaded           int
	alwaysInclude       []string
	refreshPolicy       CatalogRefreshPolicy
	stateScope          StateScope
	directTools         []tool.Tool
	toolSets            []tool.ToolSet
	analyzer            Analyzer
	manageToolSetCloser bool
}

// Option configures a DeferredToolSet.
type Option func(*config)

// WithName sets the ToolSet name. Leave empty to keep the visible tool names
// unchanged when the runtime wraps the ToolSet with NamedToolSet.
func WithName(name string) Option {
	return func(cfg *config) {
		cfg.name = strings.TrimSpace(name)
	}
}

// WithSearchToolName sets the visible function-tool name used for deferred
// search.
func WithSearchToolName(name string) Option {
	return func(cfg *config) {
		cfg.searchToolName = strings.TrimSpace(name)
	}
}

// WithSearchToolDescription overrides the search tool description.
func WithSearchToolDescription(desc string) Option {
	return func(cfg *config) {
		cfg.searchToolDesc = strings.TrimSpace(desc)
	}
}

// WithStateNamespace sets the stable state namespace used for invocation and
// session persistence.
func WithStateNamespace(namespace string) Option {
	return func(cfg *config) {
		cfg.stateNamespace = strings.TrimSpace(namespace)
	}
}

// WithMaxResults limits how many tools one search call loads.
func WithMaxResults(limit int) Option {
	return func(cfg *config) {
		if limit > 0 {
			cfg.maxResults = limit
		}
	}
}

// WithMaxLoaded limits how many loaded tools remain visible at once.
func WithMaxLoaded(limit int) Option {
	return func(cfg *config) {
		if limit > 0 {
			cfg.maxLoaded = limit
		}
	}
}

// WithAlwaysInclude keeps the provided tool names visible on every model step.
func WithAlwaysInclude(names ...string) Option {
	return func(cfg *config) {
		if len(names) == 0 {
			return
		}
		cfg.alwaysInclude = append(cfg.alwaysInclude, names...)
	}
}

// WithCatalogRefreshPolicy sets the catalog snapshot refresh policy.
func WithCatalogRefreshPolicy(policy CatalogRefreshPolicy) Option {
	return func(cfg *config) {
		cfg.refreshPolicy = policy
	}
}

// WithPersistLoadedTools toggles whether loaded tools are also persisted into
// session state.
func WithPersistLoadedTools(persist bool) Option {
	return func(cfg *config) {
		if persist {
			cfg.stateScope = StateScopeSession
			return
		}
		cfg.stateScope = StateScopeInvocation
	}
}

// WithStateScope sets how loaded-tool state is persisted.
func WithStateScope(scope StateScope) Option {
	return func(cfg *config) {
		switch scope {
		case StateScopeSession:
			cfg.stateScope = StateScopeSession
		default:
			cfg.stateScope = StateScopeInvocation
		}
	}
}

// WithAnalyzer overrides the local lexical analyzer.
func WithAnalyzer(analyzer Analyzer) Option {
	return func(cfg *config) {
		if analyzer != nil {
			cfg.analyzer = analyzer
		}
	}
}

// WithTools adds direct tools to the deferred catalog.
func WithTools(tools ...tool.Tool) Option {
	return func(cfg *config) {
		if len(tools) == 0 {
			return
		}
		cfg.directTools = append(cfg.directTools, tools...)
	}
}

// WithToolSets adds source ToolSets to the deferred catalog.
func WithToolSets(toolSets ...tool.ToolSet) Option {
	return func(cfg *config) {
		if len(toolSets) == 0 {
			return
		}
		cfg.toolSets = append(cfg.toolSets, toolSets...)
	}
}

// WithManageToolSetClosers controls whether DeferredToolSet.Close closes source
// ToolSets that were provided through WithToolSets.
func WithManageToolSetClosers(manage bool) Option {
	return func(cfg *config) {
		cfg.manageToolSetCloser = manage
	}
}
