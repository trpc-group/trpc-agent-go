//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package wikipedia provides Wikipedia Search API tools for AI agents.
package wikipedia

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/wikipedia/internal/client"
)

// Default configuration constants
const (
	defaultBaseURL   = "https://en.wikipedia.org/w/api.php"
	defaultUserAgent = "trpc-agent-go-wikipedia/1.0"
	defaultTimeout   = 30 * time.Second
	defaultLanguage  = "en"
	maxResults       = 5
	defaultName      = "wikipedia"
)

// config holds the configuration for the Wikipedia search tool set
type config struct {
	baseURL    string
	userAgent  string
	httpClient *http.Client
	language   string
	maxResults int
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

// WikipediaToolSet implements the ToolSet interface for Wikipedia operations.
type WikipediaToolSet struct {
	tools []tool.Tool
}

// Tools implements the ToolSet interface.
func (w *WikipediaToolSet) Tools(_ context.Context) []tool.Tool {
	return w.tools
}

// Name implements the ToolSet interface.
func (w *WikipediaToolSet) Name() string {
	return defaultName
}

// Close implements the ToolSet interface.
func (w *WikipediaToolSet) Close() error {
	// No resources to clean up for Wikipedia tools.
	return nil
}

// NewToolSet creates a new Wikipedia tool set with the given options.
func NewToolSet(opts ...Option) (*WikipediaToolSet, error) {
	// Apply default configuration
	cfg := &config{
		baseURL:   defaultBaseURL,
		userAgent: defaultUserAgent,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		language:   defaultLanguage,
		maxResults: maxResults,
	}
	// Apply user-provided options
	for _, opt := range opts {
		opt(cfg)
	}
	// Create the client
	wikipediaClient := client.New(cfg.baseURL, cfg.userAgent, cfg.httpClient)
	tools := []tool.Tool{createWikipediaSearchTool(wikipediaClient, cfg)}

	return &WikipediaToolSet{
		tools: tools,
	}, nil
}

// ===== Wikipedia Search Tool =====

type wikipediaSearchRequest struct {
	Query      string `json:"query" jsonschema:"description=Search query for Wikipedia"`
	Limit      int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results (default: 5)"`
	IncludeAll bool   `json:"include_all,omitempty" jsonschema:"description=Include all available metadata"`
}

type wikipediaSearchResponse struct {
	Query      string                `json:"query"`                 // Query is the original query string
	Results    []wikipediaResultItem `json:"results"`               // Results is the list of search results
	TotalHits  int                   `json:"total_hits"`            // TotalHits is the total number of hits
	Summary    string                `json:"summary"`               // Summary is a summary of the search results
	SearchTime string                `json:"search_time,omitempty"` // SearchTime is the time taken for the search
}

type wikipediaResultItem struct {
	Title       string `json:"title"`         // Title is the title of the page
	URL         string `json:"url"`           // URL is the URL of the page
	Description string `json:"description"`   // Description is the description of the page
	PageID      int    `json:"page_id"`       // PageID is the ID of the page
	WordCount   int    `json:"word_count"`    // WordCount is the word count of the page
	Size        int    `json:"size_bytes"`    // Size is the size of the page in bytes
	Timestamp   string `json:"last_modified"` // Timestamp is the last modified timestamp of the page
	Namespace   int    `json:"namespace"`
}

func createWikipediaSearchTool(wikipediaClient *client.Client, cfg *config) tool.CallableTool {
	searchFunc := func(ctx context.Context, req wikipediaSearchRequest) (wikipediaSearchResponse, error) {
		limit := req.Limit
		if limit <= 0 || limit > cfg.maxResults {
			limit = cfg.maxResults // use configured max as upper bound
		}

		startTime := time.Now()
		response, err := wikipediaClient.DetailedSearch(req.Query, limit, req.IncludeAll)
		searchDuration := time.Since(startTime)

		if err != nil {
			return wikipediaSearchResponse{
				Query:   req.Query,
				Results: []wikipediaResultItem{},
				Summary: fmt.Sprintf("Error: %v", err),
			}, err
		}

		var results []wikipediaResultItem
		for _, page := range response.Query.Search {
			snippetContent, processErr := convertHTMLToMarkdown(strings.NewReader(page.Snippet))
			// convert to plain text if markdown conversion fails
			if processErr != nil {
				snippetContent = cleanHTMLTags(page.Snippet)
			}
			item := wikipediaResultItem{
				Title:       page.Title,
				URL:         fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", cfg.language, strings.ReplaceAll(url.PathEscape(page.Title), "%20", "_")),
				Description: snippetContent,
				PageID:      page.PageID,
				WordCount:   page.WordCount,
				Size:        page.Size,
				Timestamp:   page.Timestamp,
				Namespace:   page.NS,
			}
			results = append(results, item)
		}

		return wikipediaSearchResponse{
			Query:      req.Query,
			Results:    results,
			TotalHits:  response.Query.SearchInfo.TotalHits,
			Summary:    fmt.Sprintf("Found %d results (total: %d)", len(results), response.Query.SearchInfo.TotalHits),
			SearchTime: fmt.Sprintf("%.2fms", float64(searchDuration.Microseconds())/1000.0),
		}, nil
	}

	return function.NewFunctionTool(
		searchFunc,
		function.WithName("wikipedia_search"),
		function.WithDescription(fmt.Sprintf("🔍 WIKIPEDIA SEARCH - Comprehensive Wikipedia search with rich metadata. "+
			"Use when: you need detailed information about any topic, want article statistics, or need to research a subject. "+
			"Returns: title, URL, description, page ID, word count, page size, last modified date, namespace, and more. "+
			"Best for: research, fact-checking, getting comprehensive information about topics, academic use. "+
			"Default limit: %d results.", cfg.maxResults)),
	)
}

// convert wikipedia search API response html to markdown
func convertHTMLToMarkdown(r io.Reader) (string, error) {
	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
		),
	)

	bodyBytes, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}

	markdown, err := conv.ConvertString(string(bodyBytes))
	if err != nil {
		return "", err
	}

	return markdown, nil
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
