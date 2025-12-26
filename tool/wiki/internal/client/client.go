//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
	BatchComplete string `json:"batchcomplete"`
	Query         struct {
		SearchInfo struct {
			TotalHits int `json:"totalhits"`
		} `json:"searchinfo"`
		Search []SearchResult `json:"search"`
	} `json:"query"`
}

// SearchResult represents a single search result
type SearchResult struct {
	NS        int    `json:"ns"`
	Title     string `json:"title"`
	PageID    int    `json:"pageid"`
	Size      int    `json:"size"`
	WordCount int    `json:"wordcount"`
	Snippet   string `json:"snippet"`
	Timestamp string `json:"timestamp"`
}

// Search performs a basic Wikipedia search
func (c *Client) Search(query string, limit int) (*SearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	if limit <= 0 {
		limit = 5
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	params.Set("srsearch", query)
	params.Set("format", "json")
	params.Set("srlimit", fmt.Sprintf("%d", limit))
	params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size")

	return c.executeSearch(params)
}

// QuickSearch performs a fast basic search with minimal metadata
func (c *Client) QuickSearch(query string, limit int) (*SearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	if limit <= 0 {
		limit = 5
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	params.Set("srsearch", query)
	params.Set("format", "json")
	params.Set("srlimit", fmt.Sprintf("%d", limit))
	params.Set("srprop", "snippet") // Minimal properties for speed

	return c.executeSearch(params)
}

// DetailedSearch performs a comprehensive search with all available metadata
func (c *Client) DetailedSearch(query string, limit int, includeAll bool) (*SearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	if limit <= 0 {
		limit = 5
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	params.Set("srsearch", query)
	params.Set("format", "json")
	params.Set("srlimit", fmt.Sprintf("%d", limit))

	// Include comprehensive metadata
	if includeAll {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size|categorysnippet|sectionsnippet|redirectsnippet|redirecttitle|sectiontitle|hasrelated")
	} else {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size")
	}

	return c.executeSearch(params)
}

// ExactTitleSearch searches for an exact article title match
func (c *Client) ExactTitleSearch(title string) (*SearchResponse, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	// Use intitle: prefix for exact title matching
	params.Set("srsearch", fmt.Sprintf("intitle:\"%s\"", title))
	params.Set("format", "json")
	params.Set("srlimit", "1")
	params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size")
	params.Set("srwhat", "title") // Search in titles only

	return c.executeSearch(params)
}

// PrefixSearch finds articles whose titles start with the given prefix
func (c *Client) PrefixSearch(prefix string, limit int) (*SearchResponse, error) {
	if strings.TrimSpace(prefix) == "" {
		return nil, fmt.Errorf("prefix cannot be empty")
	}

	if limit <= 0 {
		limit = 10
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	// Use prefix: operator for prefix matching
	params.Set("srsearch", fmt.Sprintf("prefix:%s", prefix))
	params.Set("format", "json")
	params.Set("srlimit", fmt.Sprintf("%d", limit))
	params.Set("srprop", "snippet")
	params.Set("srwhat", "title")

	return c.executeSearch(params)
}

// FullTextSearch performs a deep search across article content
func (c *Client) FullTextSearch(query string, limit int, namespaces []int, includeSnippet bool) (*SearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	if limit <= 0 {
		limit = 5
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	params.Set("srsearch", query)
	params.Set("format", "json")
	params.Set("srlimit", fmt.Sprintf("%d", limit))
	params.Set("srwhat", "text") // Search in full text, not just titles

	// Set namespaces if specified
	if len(namespaces) > 0 {
		nsStr := ""
		for i, ns := range namespaces {
			if i > 0 {
				nsStr += "|"
			}
			nsStr += fmt.Sprintf("%d", ns)
		}
		params.Set("srnamespace", nsStr)
	}

	// Include snippet and other metadata
	if includeSnippet {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount|size|sectionsnippet")
	} else {
		params.Set("srprop", "timestamp|wordcount|size")
	}

	return c.executeSearch(params)
}

// AdvancedSearch performs a search with custom parameters
func (c *Client) AdvancedSearch(options SearchOptions) (*SearchResponse, error) {
	if strings.TrimSpace(options.Query) == "" {
		return nil, fmt.Errorf("query cannot be empty")
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	params.Set("srsearch", options.Query)
	params.Set("format", "json")

	// Set limit
	if options.Limit > 0 {
		params.Set("srlimit", fmt.Sprintf("%d", options.Limit))
	} else {
		params.Set("srlimit", "5")
	}

	// Set search type
	if options.SearchWhat != "" {
		params.Set("srwhat", options.SearchWhat)
	}

	// Set properties
	if options.Properties != "" {
		params.Set("srprop", options.Properties)
	} else {
		params.Set("srprop", "snippet|titlesnippet|timestamp|wordcount")
	}

	// Set namespaces
	if len(options.Namespaces) > 0 {
		nsStr := ""
		for i, ns := range options.Namespaces {
			if i > 0 {
				nsStr += "|"
			}
			nsStr += fmt.Sprintf("%d", ns)
		}
		params.Set("srnamespace", nsStr)
	}

	// Set sort order
	if options.Sort != "" {
		params.Set("srsort", options.Sort)
	}

	// Set offset for pagination
	if options.Offset > 0 {
		params.Set("sroffset", fmt.Sprintf("%d", options.Offset))
	}

	// Enable/disable redirects
	if options.EnableRedirects {
		params.Set("srredirects", "1")
	}

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

// GetPageContent retrieves the full content of a Wikipedia page by title
func (c *Client) GetPageContent(title string) (*PageContentResponse, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("prop", "extracts|info")
	params.Set("titles", title)
	params.Set("format", "json")
	params.Set("explaintext", "1") // Plain text extract
	params.Set("exintro", "0")     // Full article, not just intro
	params.Set("inprop", "url")    // Include URL

	reqURL := fmt.Sprintf("%s?%s", c.baseURL, params.Encode())

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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var response PageContentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// PageContentResponse represents the response from page content API
type PageContentResponse struct {
	Query struct {
		Pages map[string]PageContent `json:"pages"`
	} `json:"query"`
}

// PageContent represents a Wikipedia page's content
type PageContent struct {
	PageID  int    `json:"pageid"`
	NS      int    `json:"ns"`
	Title   string `json:"title"`
	Extract string `json:"extract"`
	FullURL string `json:"fullurl"`
}

// GetPageSummary retrieves a short summary of a Wikipedia page
func (c *Client) GetPageSummary(title string) (*PageContentResponse, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}

	params := url.Values{}
	params.Set("action", "query")
	params.Set("prop", "extracts|info")
	params.Set("titles", title)
	params.Set("format", "json")
	params.Set("explaintext", "1")
	params.Set("exintro", "1") // Only introduction section
	params.Set("inprop", "url")

	reqURL := fmt.Sprintf("%s?%s", c.baseURL, params.Encode())

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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var response PageContentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}
