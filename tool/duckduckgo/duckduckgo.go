// Package duckduckgo provides a DuckDuckGo Instant Answer API tool for AI agents.
// This tool is designed for factual, encyclopedic information such as entity
// details, definitions, and mathematical calculations. It is NOT suitable for
// real-time data like current weather, latest news, or live stock prices.
package duckduckgo

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo/internal/client"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// maxResults is the maximum number of search results to return.
	maxResults = 5
	// maxTitleLength is the maximum length for extracted titles.
	maxTitleLength = 50
	// defaultBaseURL is the default base URL for DuckDuckGo Instant Answer API.
	defaultBaseURL = "https://api.duckduckgo.com"
	// defaultUserAgent is the default user agent for HTTP requests.
	defaultUserAgent = "trpc-agent-go-duckduckgo/1.0"
	// defaultTimeout is the default timeout for HTTP requests.
	defaultTimeout = 30 * time.Second
)

// Option is a functional option for configuring the DuckDuckGo tool.
type Option func(*config)

// config holds the configuration for the DuckDuckGo tool.
type config struct {
	baseURL    string
	userAgent  string
	httpClient *http.Client
}

// WithBaseURL sets the base URL for the DuckDuckGo API.
func WithBaseURL(baseURL string) Option {
	return func(c *config) {
		c.baseURL = baseURL
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

// searchRequest represents the input for the DuckDuckGo search tool.
type searchRequest struct {
	Query string `json:"query" jsonschema:"description=The search query to execute on DuckDuckGo"`
}

// searchResponse represents the output from the DuckDuckGo search tool.
type searchResponse struct {
	Query   string       `json:"query"`
	Results []resultItem `json:"results"`
	Summary string       `json:"summary"`
}

// resultItem represents a single search result.
type resultItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// ddgTool represents the DuckDuckGo search tool.
type ddgTool struct {
	client *client.Client
}

// NewTool creates a new DuckDuckGo search tool with the provided options.
func NewTool(opts ...Option) tool.CallableTool {
	// Apply default configuration.
	cfg := &config{
		baseURL:   defaultBaseURL,
		userAgent: defaultUserAgent,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}

	// Apply user-provided options.
	for _, opt := range opts {
		opt(cfg)
	}

	// Create the client with the configured values.
	ddgClient := client.New(cfg.baseURL, cfg.userAgent, cfg.httpClient)

	searchTool := &ddgTool{
		client: ddgClient,
	}

	return function.NewFunctionTool(
		searchTool.search,
		function.WithName("duckduckgo_search"),
		function.WithDescription("Search using DuckDuckGo's Instant Answer API for "+
			"factual, encyclopedic information. Works best for: entity information "+
			"(people, companies, places like 'Steve Jobs', 'Tesla company', 'Microsoft'), "+
			"definitions ('algorithm', 'photosynthesis'), mathematical calculations "+
			"('2+2', 'convert 100 feet to meters'), and historical facts. "+
			"NOT suitable for: real-time data (current weather, live stock prices, "+
			"latest news), recent events, or time-sensitive information. "+
			"Returns structured results with abstracts, definitions, and related topics."),
	)
}

// search performs the actual search operation.
func (t *ddgTool) search(req searchRequest) searchResponse {
	if strings.TrimSpace(req.Query) == "" {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: "Error: Empty search query provided",
		}
	}

	// Perform the search.
	response, err := t.client.Search(req.Query)
	if err != nil {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: fmt.Sprintf("Error performing search: %v", err),
		}
	}

	// Convert the response to our format.
	var results []resultItem
	var summaryParts []string

	// Add instant answer if available.
	if response.Answer != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("Answer: %s", response.Answer))
	}

	// Add abstract if available.
	if response.AbstractText != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("Abstract: %s", response.AbstractText))
		if response.AbstractSource != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("Source: %s", response.AbstractSource))
		}
	}

	// Add definition if available.
	if response.Definition != "" {
		summaryParts = append(summaryParts, fmt.Sprintf("Definition: %s", response.Definition))
		if response.DefinitionSource != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("Definition Source: %s", response.DefinitionSource))
		}
	}

	// Process related topics as results.
	for i, topic := range response.RelatedTopics {
		if i >= maxResults {
			break
		}
		if topic.Text != "" && topic.FirstURL != "" {
			results = append(results, resultItem{
				Title:       extractTitleFromTopic(topic.Text),
				URL:         topic.FirstURL,
				Description: topic.Text,
			})
		}
	}

	// If no results from related topics, create a summary result.
	if len(results) == 0 && len(summaryParts) > 0 {
		results = append(results, resultItem{
			Title:       fmt.Sprintf("DuckDuckGo search: %s", req.Query),
			URL:         fmt.Sprintf("https://duckduckgo.com/?q=%s", strings.ReplaceAll(req.Query, " ", "+")),
			Description: strings.Join(summaryParts, " | "),
		})
	}

	summary := fmt.Sprintf("Found %d results for query '%s'", len(results), req.Query)
	if len(summaryParts) > 0 {
		summary = strings.Join(summaryParts, " | ")
	}

	return searchResponse{
		Query:   req.Query,
		Results: results,
		Summary: summary,
	}
}

// extractTitleFromTopic extracts a title from a topic text.
func extractTitleFromTopic(text string) string {
	var title string

	// Split by " - " and take the first part as title.
	parts := strings.Split(text, " - ")
	if len(parts) > 0 && parts[0] != "" {
		title = strings.TrimSpace(parts[0])
	} else {
		title = strings.TrimSpace(text)
	}

	// Apply length limit.
	if len(title) > maxTitleLength {
		return title[:maxTitleLength-3] + "..."
	}

	return title
}
