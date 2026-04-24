//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/html"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type codeSearchBackend interface {
	search(context.Context, webSearchInput) ([]webSearchHit, error)
}

type duckDuckGoSearchBackend struct {
	client    *http.Client
	baseURL   string
	userAgent string
	size      int
	offset    int
}

type googleSearchBackend struct {
	client  *http.Client
	options *WebSearchOptions
}

func newWebSearchTool(options *WebSearchOptions) (tool.Tool, error) {
	target, err := newSearchBackend(options)
	if err != nil {
		return nil, err
	}
	return function.NewFunctionTool(
		func(ctx context.Context, in webSearchInput) (webSearchOutput, error) {
			if strings.TrimSpace(in.Query) == "" {
				return webSearchOutput{}, fmt.Errorf("query is required")
			}
			if len(in.AllowedDomains) > 0 && len(in.BlockedDomains) > 0 {
				return webSearchOutput{}, fmt.Errorf("cannot specify both allowed_domains and blocked_domains")
			}
			start := time.Now()
			hits, err := target.search(ctx, in)
			results := make([]webSearchResult, 0, 1)
			if len(hits) > 0 {
				results = append(results, webSearchResult{
					ToolUseID: uuid.NewString(),
					Content:   hits,
				})
			}
			return webSearchOutput{
				Query:           in.Query,
				Results:         results,
				DurationSeconds: max(time.Since(start).Seconds(), 0.001),
			}, err
		},
		function.WithName(toolWebSearch),
		function.WithDescription(webSearchDescription()),
	), nil
}

func newSearchBackend(options *WebSearchOptions) (codeSearchBackend, error) {
	provider := "duckduckgo"
	if options != nil && strings.TrimSpace(options.Provider) != "" {
		provider = strings.ToLower(strings.TrimSpace(options.Provider))
	}
	client := &http.Client{Timeout: defaultHTTPTimeout}
	if options != nil && options.Timeout > 0 {
		client.Timeout = options.Timeout
	}
	switch provider {
	case "duckduckgo":
		baseURL := "https://html.duckduckgo.com/html/"
		userAgent := ""
		if options != nil {
			if strings.TrimSpace(options.BaseURL) != "" {
				baseURL = strings.TrimSpace(options.BaseURL)
			}
			userAgent = strings.TrimSpace(options.UserAgent)
		}
		return &duckDuckGoSearchBackend{
			client:    client,
			baseURL:   baseURL,
			userAgent: userAgent,
			size:      max(0, webSearchSize(options)),
			offset:    max(0, webSearchOffset(options)),
		}, nil
	case "google":
		return &googleSearchBackend{client: client, options: options}, nil
	default:
		return nil, fmt.Errorf("unsupported web search provider: %s", provider)
	}
}

func webSearchDescription() string {
	return fmt.Sprintf(`Search the web for current information.

Usage:
- Use %s for open-ended discovery, current events, recent documentation, or when you do not yet know the exact page to fetch.
- query is required.
- allowed_domains and blocked_domains may constrain the search, but you must not set both at the same time.
- Results contain titles, URLs, and snippets, grouped into Claude-style search result blocks.
- After choosing a relevant result, use %s to read the destination page in detail.`, toolWebSearch, toolWebFetch)
}

func (b *duckDuckGoSearchBackend) search(
	ctx context.Context,
	in webSearchInput,
) ([]webSearchHit, error) {
	u, err := url.Parse(b.baseURL)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("q", in.Query)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if b.userAgent != "" {
		req.Header.Set("User-Agent", b.userAgent)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := readHTTPBody(resp, 16*1024, 16*1024)
		return nil, fmt.Errorf("duckduckgo search request failed: status=%d body=%s", resp.StatusCode, body)
	}
	body, err := readHTTPBody(resp, 512*1024, 512*1024)
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoHTML(body, in, b.offset, b.size), nil
}

func parseDuckDuckGoHTML(body []byte, in webSearchInput, offset int, limit int) []webSearchHit {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	type partialResult struct {
		Title   string
		URL     string
		Snippet string
	}
	results := make([]partialResult, 0, 10)
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && htmlHasClass(node, "result__a") {
			title := strings.TrimSpace(htmlNodeText(node))
			link := ""
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					link = strings.TrimSpace(attr.Val)
					break
				}
			}
			results = append(results, partialResult{Title: title, URL: link})
		}
		if node.Type == html.ElementNode && htmlHasClass(node, "result__snippet") {
			if len(results) > 0 && results[len(results)-1].Snippet == "" {
				results[len(results)-1].Snippet = strings.TrimSpace(htmlNodeText(node))
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	hits := make([]webSearchHit, 0, len(results))
	seen := map[string]struct{}{}
	for _, item := range results {
		normalizedURL := normalizeDuckDuckGoResultURL(item.URL)
		if normalizedURL == "" || !matchSearchDomainFilters(normalizedURL, in.AllowedDomains, in.BlockedDomains) {
			continue
		}
		if _, ok := seen[normalizedURL]; ok {
			continue
		}
		seen[normalizedURL] = struct{}{}
		hits = append(hits, webSearchHit{
			Title:   item.Title,
			URL:     normalizedURL,
			Snippet: collapseWhitespace(item.Snippet),
		})
	}
	return applySearchWindow(hits, offset, limit)
}

func normalizeDuckDuckGoResultURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	if uddg := strings.TrimSpace(parsed.Query().Get("uddg")); uddg != "" {
		return uddg
	}
	return trimmed
}

func htmlHasClass(node *html.Node, className string) bool {
	for _, attr := range node.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, candidate := range strings.Fields(attr.Val) {
			if candidate == className {
				return true
			}
		}
	}
	return false
}

func htmlNodeText(node *html.Node) string {
	parts := make([]string, 0, 8)
	var visit func(*html.Node)
	visit = func(current *html.Node) {
		if current.Type == html.TextNode {
			text := strings.TrimSpace(current.Data)
			if text != "" {
				parts = append(parts, text)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(node)
	return strings.Join(parts, " ")
}

func (b *googleSearchBackend) search(
	ctx context.Context,
	in webSearchInput,
) ([]webSearchHit, error) {
	if b.options == nil {
		return nil, fmt.Errorf("google search config is required")
	}
	apiKey := strings.TrimSpace(b.options.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(envGoogleAPIKey))
	}
	engineID := strings.TrimSpace(b.options.EngineID)
	if engineID == "" {
		engineID = strings.TrimSpace(os.Getenv(envGoogleEngineID))
	}
	if apiKey == "" || engineID == "" {
		return nil, fmt.Errorf("google search requires api_key and engine_id")
	}
	baseURL := strings.TrimSpace(b.options.BaseURL)
	if baseURL == "" {
		baseURL = "https://www.googleapis.com/customsearch/v1"
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("key", apiKey)
	query.Set("cx", engineID)
	query.Set("q", in.Query)
	if b.options.Size > 0 {
		query.Set("num", strconv.Itoa(b.options.Size))
	}
	if b.options.Offset > 0 {
		query.Set("start", strconv.Itoa(b.options.Offset+1))
	}
	if strings.TrimSpace(b.options.Lang) != "" {
		query.Set("lr", "lang_"+strings.TrimSpace(b.options.Lang))
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := readHTTPBody(resp, 16*1024, 16*1024)
		return nil, fmt.Errorf("google search request failed: status=%d body=%s", resp.StatusCode, body)
	}
	var decoded struct {
		Items []struct {
			Link    string `json:"link"`
			Title   string `json:"title"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	hits := make([]webSearchHit, 0, len(decoded.Items))
	seen := map[string]struct{}{}
	for _, item := range decoded.Items {
		link := strings.TrimSpace(item.Link)
		if link == "" || !matchSearchDomainFilters(link, in.AllowedDomains, in.BlockedDomains) {
			continue
		}
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		hits = append(hits, webSearchHit{
			Title:   item.Title,
			URL:     link,
			Snippet: item.Snippet,
		})
	}
	return hits, nil
}

func applySearchWindow(hits []webSearchHit, offset int, limit int) []webSearchHit {
	if len(hits) == 0 {
		return nil
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(hits) {
		return nil
	}
	hits = hits[offset:]
	if limit > 0 && limit < len(hits) {
		return hits[:limit]
	}
	return hits
}

func webSearchSize(options *WebSearchOptions) int {
	if options == nil || options.Size <= 0 {
		return 0
	}
	return options.Size
}

func webSearchOffset(options *WebSearchOptions) int {
	if options == nil || options.Offset <= 0 {
		return 0
	}
	return options.Offset
}
