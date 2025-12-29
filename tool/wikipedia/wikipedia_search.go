//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package wiki provides Wikipedia Search API tools for AI agents.
package wikipedia

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/wiki/internal/client"
)

// Default configuration constants
const (
	defaultBaseURL   = "https://en.wikipedia.org/w/api.php"
	defaultUserAgent = "trpc-agent-go-wiki/1.0"
	defaultTimeout   = 30 * time.Second
	defaultLanguage  = "en"
	maxResults       = 5
	defaultName      = "wiki"
)

// config holds the configuration for the Wikipedia search tool set
type config struct {
	baseURL       string
	userAgent     string
	httpClient    *http.Client
	language      string
	maxResults    int
	searchEnabled bool
}

// Option is a functional option for configuring the Wikipedia tool set
type Option func(*config)

// WithLanguage sets the Wikipedia language (e.g., "en", "zh", "es")
func WithLanguage(language string) Option {
	return func(c *config) {
		c.language = language
		// Update baseURL to use the specified language
		c.baseURL = fmt.Sprintf("https://%s.wikipedia.org/w/api.php", language)
	}
}

// WithMaxResults sets the maximum number of search results
func WithMaxResults(maxResults int) Option {
	return func(c *config) {
		c.maxResults = maxResults
	}
}

// WithTimeout sets the HTTP client timeout
func WithTimeout(timeout time.Duration) Option {
	return func(c *config) {
		c.httpClient.Timeout = timeout
	}
}

// WithUserAgent sets the User-Agent string for requests
func WithUserAgent(userAgent string) Option {
	return func(c *config) {
		c.userAgent = userAgent
	}
}

// wikiToolSet implements the ToolSet interface for Wikipedia operations.
type wikiToolSet struct {
	searchEnabled bool
	tools         []tool.Tool
}

// Tools implements the ToolSet interface.
func (w *wikiToolSet) Tools(_ context.Context) []tool.Tool {
	return w.tools
}

// Name implements the ToolSet interface.
func (w *wikiToolSet) Name() string {
	return defaultName
}

// Close implements the ToolSet interface.
func (w *wikiToolSet) Close() error {
	// No resources to clean up for Wikipedia tools.
	return nil
}

// NewToolSet creates a new Wikipedia tool set with the given options.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	// Apply default configuration
	cfg := &config{
		baseURL:   defaultBaseURL,
		userAgent: defaultUserAgent,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		language:      defaultLanguage,
		maxResults:    maxResults,
		searchEnabled: true,
	}

	// Apply user-provided options
	for _, opt := range opts {
		opt(cfg)
	}

	// Create function tools based on enabled features.
	var tools []tool.Tool
	if cfg.searchEnabled {
		// Create the client
		wikiClient := client.New(cfg.baseURL, cfg.userAgent, cfg.httpClient)
		tools = append(tools, createWikiSearchTool(wikiClient, cfg))
	}

	wikiToolSet := &wikiToolSet{
		searchEnabled: cfg.searchEnabled,
		tools:         tools,
	}

	return wikiToolSet, nil
}

// ===== Wiki Search Tool =====

type wikiSearchRequest struct {
	Query      string `json:"query" jsonschema:"description=Search query for Wikipedia"`
	Limit      int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results (default: 5)"`
	IncludeAll bool   `json:"include_all,omitempty" jsonschema:"description=Include all available metadata"`
}

type wikiSearchResponse struct {
	Query      string           `json:"query"`
	Results    []wikiResultItem `json:"results"`
	TotalHits  int              `json:"total_hits"`
	Summary    string           `json:"summary"`
	SearchTime string           `json:"search_time,omitempty"`
}

type wikiResultItem struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
	PageID      int      `json:"page_id"`
	WordCount   int      `json:"word_count"`
	Size        int      `json:"size_bytes"`
	Timestamp   string   `json:"last_modified"`
	Namespace   int      `json:"namespace"`
	Categories  []string `json:"categories,omitempty"`
	Relevance   float64  `json:"relevance_score,omitempty"`
}

func createWikiSearchTool(wikiClient *client.Client, cfg *config) tool.CallableTool {
	searchFunc := func(ctx context.Context, req wikiSearchRequest) (wikiSearchResponse, error) {
		limit := req.Limit
		if limit <= 0 || limit > cfg.maxResults {
			limit = cfg.maxResults // use configured max as upper bound
		}

		startTime := time.Now()
		response, err := wikiClient.DetailedSearch(req.Query, limit, req.IncludeAll)
		searchDuration := time.Since(startTime)

		if err != nil {
			return wikiSearchResponse{
				Query:   req.Query,
				Results: []wikiResultItem{},
				Summary: fmt.Sprintf("Error: %v", err),
			}, err
		}

		var results []wikiResultItem
		for _, page := range response.Query.Search {
			item := wikiResultItem{
				Title:       page.Title,
				URL:         fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", cfg.language, strings.ReplaceAll(page.Title, " ", "_")),
				Description: cleanHTMLTags(page.Snippet),
				PageID:      page.PageID,
				WordCount:   page.WordCount,
				Size:        page.Size,
				Timestamp:   page.Timestamp,
				Namespace:   page.NS,
			}
			results = append(results, item)
		}

		return wikiSearchResponse{
			Query:      req.Query,
			Results:    results,
			TotalHits:  response.Query.SearchInfo.TotalHits,
			Summary:    fmt.Sprintf("Found %d results (total: %d)", len(results), response.Query.SearchInfo.TotalHits),
			SearchTime: fmt.Sprintf("%.2fms", float64(searchDuration.Microseconds())/1000.0),
		}, nil
	}

	return function.NewFunctionTool(
		searchFunc,
		function.WithName("wiki_search"),
		function.WithDescription(fmt.Sprintf("🔍 WIKI SEARCH - Comprehensive Wikipedia search with rich metadata. "+
			"Use when: you need detailed information about any topic, want article statistics, or need to research a subject. "+
			"Returns: title, URL, description, page ID, word count, page size, last modified date, namespace, and more. "+
			"Best for: research, fact-checking, getting comprehensive information about topics, academic use. "+
			"Default limit: %d results.", cfg.maxResults)),
	)
}

// cleanHTMLTags removes HTML tags from text
func cleanHTMLTags(text string) string {
	// Remove HTML tags
	re := regexp.MustCompile(`<[^>]*>`)
	cleaned := re.ReplaceAllString(text, "")

	// Replace common HTML entities
	cleaned = strings.ReplaceAll(cleaned, "&amp;", "&")
	cleaned = strings.ReplaceAll(cleaned, "&lt;", "<")
	cleaned = strings.ReplaceAll(cleaned, "&gt;", ">")
	cleaned = strings.ReplaceAll(cleaned, "&quot;", "\"")
	cleaned = strings.ReplaceAll(cleaned, "&#39;", "'")
	cleaned = strings.ReplaceAll(cleaned, "&nbsp;", " ")

	// Clean up extra whitespace
	cleaned = regexp.MustCompile(`\s+`).ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)

	return cleaned
}
