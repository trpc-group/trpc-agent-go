//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package client provides Wikipedia API client.
package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client represents a Wikipedia API client
type Client struct {
	baseURL    string
	userAgent  string
	httpClient *http.Client
}

// New creates a new Wikipedia API client
func New(baseURL, userAgent string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    baseURL,
		userAgent:  userAgent,
		httpClient: httpClient,
	}
}

// SearchResponse represents the Wikipedia API search response
type SearchResponse struct {
	BatchComplete string `json:"batchcomplete"` // BatchComplete indicates if the request was completed
	Query         struct {
		SearchInfo struct {
			TotalHits int `json:"totalhits"` // TotalHits is the total number of search results
		} `json:"searchinfo"`
		Search []SearchResult `json:"search"` // Search contains the list of search results
	} `json:"query"`
}

// SearchResult represents a single search result
type SearchResult struct {
	NS        int    `json:"ns"`        // NS is the namespace ID of the page
	Title     string `json:"title"`     // Title is the title of the page
	PageID    int    `json:"pageid"`    // PageID is the unique page identifier
	Size      int    `json:"size"`      // Size is the page size in bytes
	WordCount int    `json:"wordcount"` // WordCount is the number of words in the page
	Snippet   string `json:"snippet"`   // Snippet is a short excerpt of the page content
	Timestamp string `json:"timestamp"` // Timestamp is the last modification time
}

// validateQuery validates and normalizes query parameters
func validateQuery(query string) error {
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("query cannot be empty")
	}
	return nil
}

// normalizeLimit ensures limit is positive, returns default if <= 0
func normalizeLimit(limit, defaultLimit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	return limit
}

// newSearchParams creates base search parameters
func newSearchParams(query string, limit int) url.Values {
	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	params.Set("srsearch", query)
	params.Set("format", "json")
	params.Set("srlimit", fmt.Sprintf("%d", limit))
	return params
}

// formatNamespaces converts namespace slice to API format
func formatNamespaces(namespaces []int) string {
	if len(namespaces) == 0 {
		return ""
	}
	var builder strings.Builder
	for i, ns := range namespaces {
		if i > 0 {
			builder.WriteByte('|')
		}
		fmt.Fprintf(&builder, "%d", ns)
	}
	return builder.String()
}

// Search performs a basic Wikipedia search
func (c *Client) Search(query string, limit int) (*SearchResponse, error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	params := newSearchParams(query, normalizeLimit(limit, 5))
	params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size")
	return c.executeSearch(params)
}

// QuickSearch performs a fast basic search with minimal metadata
func (c *Client) QuickSearch(query string, limit int) (*SearchResponse, error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	params := newSearchParams(query, normalizeLimit(limit, 5))
	params.Set("srprop", "snippet")
	return c.executeSearch(params)
}

// DetailedSearch performs a comprehensive search with all available metadata
func (c *Client) DetailedSearch(query string, limit int, includeAll bool) (*SearchResponse, error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	params := newSearchParams(query, normalizeLimit(limit, 5))
	if includeAll {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size|categorysnippet|sectionsnippet|redirectsnippet|redirecttitle|sectiontitle|hasrelated")
	} else {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size")
	}
	return c.executeSearch(params)
}

// ExactTitleSearch searches for an exact article title match
func (c *Client) ExactTitleSearch(title string) (*SearchResponse, error) {
	if err := validateQuery(title); err != nil {
		return nil, err
	}
	params := newSearchParams(fmt.Sprintf("intitle:\"%s\"", title), 1)
	params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size")
	params.Set("srwhat", "title")
	return c.executeSearch(params)
}

// PrefixSearch finds articles whose titles start with the given prefix
func (c *Client) PrefixSearch(prefix string, limit int) (*SearchResponse, error) {
	if err := validateQuery(prefix); err != nil {
		return nil, err
	}
	params := newSearchParams(fmt.Sprintf("prefix:%s", prefix), normalizeLimit(limit, 10))
	params.Set("srprop", "snippet")
	params.Set("srwhat", "title")
	return c.executeSearch(params)
}

// FullTextSearch performs a deep search across article content
func (c *Client) FullTextSearch(query string, limit int, namespaces []int, includeSnippet bool) (*SearchResponse, error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	params := newSearchParams(query, normalizeLimit(limit, 5))
	params.Set("srwhat", "text")
	// Set namespaces if provided
	if nsStr := formatNamespaces(namespaces); nsStr != "" {
		params.Set("srnamespace", nsStr)
	}
	// Set properties based on snippet inclusion
	if includeSnippet {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size|sectionsnippet")
	} else {
		params.Set("srprop", "timestamp|wordcount|size")
	}
	return c.executeSearch(params)
}

// AdvancedSearch performs a search with custom parameters
func (c *Client) AdvancedSearch(options SearchOptions) (*SearchResponse, error) {
	if err := validateQuery(options.Query); err != nil {
		return nil, err
	}
	params := newSearchParams(options.Query, normalizeLimit(options.Limit, 5))
	// Set search type if provided
	if options.SearchWhat != "" {
		params.Set("srwhat", options.SearchWhat)
	}
	if options.Properties != "" {
		params.Set("srprop", options.Properties)
	} else {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount")
	}
	if nsStr := formatNamespaces(options.Namespaces); nsStr != "" {
		params.Set("srnamespace", nsStr)
	}
	if options.Sort != "" {
		params.Set("srsort", options.Sort)
	}
	if options.Offset > 0 {
		params.Set("sroffset", fmt.Sprintf("%d", options.Offset))
	}
	if options.EnableRedirects {
		params.Set("srredirects", "1")
	}
	// Execute the search
	return c.executeSearch(params)
}

// SearchOptions provides advanced search configuration
type SearchOptions struct {
	Query           string // Search query (required)
	Limit           int    // Maximum results (default: 5)
	Offset          int    // Pagination offset
	SearchWhat      string // "title", "text", or "nearmatch"
	Properties      string // Comma-separated list of properties
	Namespaces      []int  // Namespaces to search (0=articles, 14=categories, etc.)
	Sort            string // Sort order: "relevance", "create_timestamp_desc", etc.
	EnableRedirects bool   // Include redirects in results
}

// executeSearch is a helper method to execute search requests
func (c *Client) executeSearch(params url.Values) (*SearchResponse, error) {
	reqURL := fmt.Sprintf("%s?%s", c.baseURL, params.Encode())
	// Create the HTTP request
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	// Set headers
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	// Perform the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()
	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	// Parse the JSON response
	var response SearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w\n%s", err, string(body))
	}

	return &response, nil
}

// newPageParams creates base page query parameters
func newPageParams(title string) url.Values {
	params := url.Values{}
	params.Set("action", "query")
	params.Set("prop", "extracts|info")
	params.Set("titles", title)
	params.Set("format", "json")
	params.Set("explaintext", "1")
	params.Set("inprop", "url")
	return params
}

// executePage performs page content request
func (c *Client) executePage(params url.Values) (*PageContentResponse, error) {
	reqURL := fmt.Sprintf("%s?%s", c.baseURL, params.Encode())
	// Create the HTTP request
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()
	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}
	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	// Parse the JSON response
	var response PageContentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// GetPageContent retrieves the full content of a Wikipedia page by title
func (c *Client) GetPageContent(title string) (*PageContentResponse, error) {
	if err := validateQuery(title); err != nil {
		return nil, err
	}
	params := newPageParams(title)
	params.Set("exintro", "0") // Full article
	return c.executePage(params)
}

// PageContentResponse represents the response from page content API
type PageContentResponse struct {
	Query struct {
		Pages map[string]PageContent `json:"pages"` // Pages contains page content indexed by page ID
	} `json:"query"`
}

// PageContent represents a Wikipedia page's content
type PageContent struct {
	PageID  int    `json:"pageid"`  // PageID is the unique page identifier
	NS      int    `json:"ns"`      // NS is the namespace ID of the page
	Title   string `json:"title"`   // Title is the title of the page
	Extract string `json:"extract"` // Extract is the text content of the page
	FullURL string `json:"fullurl"` // FullURL is the complete URL to the page
}

// GetPageSummary retrieves a short summary of a Wikipedia page
func (c *Client) GetPageSummary(title string) (*PageContentResponse, error) {
	if err := validateQuery(title); err != nil {
		return nil, err
	}
	params := newPageParams(title)
	params.Set("exintro", "1") // Only introduction
	return c.executePage(params)
}
