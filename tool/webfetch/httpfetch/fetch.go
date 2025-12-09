//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package httpfetch provides the HTTP webfetch tool.
package httpfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/internal/urlfilter"
)

const (
	defaultTimeout = 30 * time.Second
	maxURLs        = 20
)

// Option configures the WebFetch tool.
type Option func(*config)

type config struct {
	httpClient            *http.Client
	maxContentLength      int
	maxTotalContentLength int
	allowedDomains        []string
	blockedDomains        []string
}

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *config) {
		cfg.httpClient = c
	}
}

// WithMaxContentLength sets the maximum content length for a single URL.
// 0 means unlimited.
func WithMaxContentLength(limit int) Option {
	return func(cfg *config) {
		cfg.maxContentLength = limit
	}
}

// WithMaxTotalContentLength sets the maximum total content length for all URLs.
// 0 means unlimited.
func WithMaxTotalContentLength(limit int) Option {
	return func(cfg *config) {
		cfg.maxTotalContentLength = limit
	}
}

// WithAllowedDomains sets the list of allowed domains or URL patterns.
// If provided, only URLs matching one of these patterns (host and optional path prefix) will be allowed.
// Examples: "example.com" (allows all paths), "example.com/docs" (allows /docs/...).
func WithAllowedDomains(domains []string) Option {
	return func(cfg *config) {
		cfg.allowedDomains = domains
	}
}

// WithBlockedDomains sets the list of blocked domains or URL patterns.
// URLs matching one of these patterns will be blocked.
func WithBlockedDomains(domains []string) Option {
	return func(cfg *config) {
		cfg.blockedDomains = domains
	}
}

// fetchRequest is the input for the tool.
type fetchRequest struct {
	URLS []string `json:"urls" jsonschema:"description=The list of URLs to fetch content from"`
}

// fetchResponse is the output.
type fetchResponse struct {
	Results []resultItem `json:"results"`
	Summary string       `json:"summary"`
}

type resultItem struct {
	RetrievedURL string `json:"retrieved_url"`
	StatusCode   int    `json:"status_code,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	Content      string `json:"content,omitempty"`
	Error        string `json:"error,omitempty"`
}

// NewTool creates the web-fetch tool.
func NewTool(opts ...Option) tool.CallableTool {
	cfg := &config{
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	t := &webFetchTool{
		client:                cfg.httpClient,
		maxContentLength:      cfg.maxContentLength,
		maxTotalContentLength: cfg.maxTotalContentLength,
	}

	// Register urlValidators
	// 1. Blocked domains
	for _, blocked := range cfg.blockedDomains {
		t.urlValidators = append(t.urlValidators, urlfilter.URLValidator{
			Filter: urlfilter.NewBlockPatternFilter(blocked),
			ErrMsg: fmt.Sprintf("URL matches blocked pattern: %s", blocked),
		})
	}

	// 2. Allowed domains
	if len(cfg.allowedDomains) > 0 {
		t.urlValidators = append(t.urlValidators, urlfilter.URLValidator{
			Filter: urlfilter.NewAllowPatternsFilter(cfg.allowedDomains),
			ErrMsg: "URL does not match any allowed pattern",
		})
	}

	return function.NewFunctionTool(
		t.fetch,
		function.WithName("web_fetch"),
		function.WithDescription("Fetches and extracts text content from a list of URLs. "+
			"Supports up to 20 URLs. Useful for summarizing, comparing, or extracting information from web pages."),
	)
}

type webFetchTool struct {
	client                *http.Client
	maxContentLength      int
	maxTotalContentLength int
	urlValidators         []urlfilter.URLValidator
}

func (t *webFetchTool) fetch(ctx context.Context, req fetchRequest) (fetchResponse, error) {
	if len(req.URLS) == 0 {
		return fetchResponse{Summary: "No URLs provided"}, nil
	}

	// Deduplicate URLs
	uniqueURLs := make(map[string]struct{})
	var targetURLs []string
	for _, u := range req.URLS {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, exists := uniqueURLs[u]; !exists {
			uniqueURLs[u] = struct{}{}
			targetURLs = append(targetURLs, u)
		}
	}

	if len(targetURLs) > maxURLs {
		targetURLs = targetURLs[:maxURLs]
	}

	var wg sync.WaitGroup
	results := make([]resultItem, len(targetURLs))

	for i, u := range targetURLs {
		wg.Add(1)
		go func(index int, urlStr string) {
			defer wg.Done()
			item := t.fetchOne(ctx, urlStr)
			results[index] = item
		}(i, u)
	}
	wg.Wait()

	// Apply total length limit
	if t.maxTotalContentLength > 0 {
		currentTotal := 0
		for i := range results {
			if results[i].Error != "" {
				continue
			}
			contentLen := len(results[i].Content)
			if currentTotal >= t.maxTotalContentLength {
				results[i].Content = "" // Or maybe a note like "[Truncated due to total limit]"
				results[i].Error = "Content truncated due to total length limit"
			} else if currentTotal+contentLen > t.maxTotalContentLength {
				allowed := t.maxTotalContentLength - currentTotal
				results[i].Content = truncateString(results[i].Content, allowed)
				currentTotal += len(results[i].Content)
			} else {
				currentTotal += contentLen
			}
		}
	}

	return fetchResponse{
		Results: results,
		Summary: fmt.Sprintf("Fetched %d URLs", len(targetURLs)),
	}, nil
}

func (t *webFetchTool) fetchOne(ctx context.Context, urlStr string) resultItem {
	item := resultItem{
		RetrievedURL: urlStr,
	}

	if err := urlfilter.CheckURL(t.urlValidators, urlStr); err != nil {
		item.Error = err.Error()
		return item
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return item
	}
	req.Header.Set("User-Agent", "trpc-agent-go/web-fetch")

	resp, err := t.client.Do(req)
	if err != nil {
		item.Error = err.Error()
		return item
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	// Parse media type (ignore parameters like charset)
	item.ContentType = strings.Split(contentType, ";")[0]
	item.ContentType = strings.TrimSpace(item.ContentType)
	item.StatusCode = resp.StatusCode

	if item.StatusCode < 200 || item.StatusCode >= 300 {
		item.Error = fmt.Sprintf("HTTP status %d", item.StatusCode)
		return item
	}

	var content string
	var processErr error

	if item.ContentType == "text/html" {
		content, processErr = convertHTMLToMarkdown(resp.Body)
	} else if isSupportedTextType(item.ContentType) {
		content, processErr = readBodyAsString(resp.Body)
	} else {
		item.Error = fmt.Sprintf("unsupported content type: %s", item.ContentType)
		return item
	}

	if processErr != nil {
		item.Error = processErr.Error()
		return item
	}

	// Apply per-URL limit
	if t.maxContentLength > 0 && len(content) > t.maxContentLength {
		content = truncateString(content, t.maxContentLength)
	}

	item.Content = content
	return item
}

// truncateString truncates a string to n bytes, ensuring valid UTF-8.
func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// If we cut exactly at n, check if it's a valid boundary.
	// Simple approach: convert to runes if we cared about rune count, but "length" usually implies bytes/storage.
	// However, chopping bytes can split characters.
	// Safe approach: iterate runes until byte count exceeds n.

	if n <= 0 {
		return ""
	}

	var sb strings.Builder
	currentLen := 0
	for _, r := range s {
		rLen := len(string(r))
		if currentLen+rLen > n {
			break
		}
		sb.WriteRune(r)
		currentLen += rLen
	}
	return sb.String()
}

func isSupportedTextType(mediaType string) bool {
	switch mediaType {
	case "application/json",
		"text/plain",
		"text/xml",
		"text/css",
		"text/javascript",
		"text/csv",
		"text/rtf":
		return true
	default:
		return false
	}
}

// readBodyAsString reads the entire content of an io.Reader into a string.
func readBodyAsString(r io.Reader) (string, error) {
	buf := new(strings.Builder)
	_, err := io.Copy(buf, r)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	return buf.String(), nil
}

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
