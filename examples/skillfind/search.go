//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	webSearchToolName = "web_search"

	ddgFormQueryKey    = "q"
	ddgFormContentType = "application/x-www-form-urlencoded"
	ddgHTMLSearchURL   = "https://html.duckduckgo.com/html/"
	ddgHTTPTimeout     = 30 * time.Second

	defaultMaxResults = 5
	maxSearchResults  = 20

	httpPrefix  = "http://"
	httpsPrefix = "https://"

	duckDuckGoHost = "duckduckgo.com"
)

const ddgUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/120.0.0.0 Safari/537.36"

const (
	ddgLinkPattern = `class="result__a"[^>]*href="([^"]+)"[^>]*>` +
		`([^<]+)</a>`
	ddgSnippetPattern = `class="result__snippet"[^>]*>([^<]+)</a>`
	ddgRedirectPath   = "/l/"
	ddgRedirectKey    = "uddg"
)

type webSearchRequest struct {
	Query string `json:"query" jsonschema:"description=Search query"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Max results"`
}

type webSearchResponse struct {
	Query   string            `json:"query"`
	Results []webSearchResult `json:"results"`
	Summary string            `json:"summary"`
	Error   string            `json:"error,omitempty"`
}

type webSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func newWebSearchTool() tool.Tool {
	return function.NewFunctionTool(
		webSearch,
		function.WithName(webSearchToolName),
		function.WithDescription(
			"Search the public web with DuckDuckGo HTML results. "+
				"Useful for finding GitHub pages that contain "+
				"Agent Skills and SKILL.md files.",
		),
	)
}

func webSearch(
	ctx context.Context,
	req webSearchRequest,
) (webSearchResponse, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return webSearchResponse{Error: "query is required"}, nil
	}

	limit := sanitizeSearchLimit(req.Limit)

	formData := url.Values{}
	formData.Set(ddgFormQueryKey, query)

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		ddgHTMLSearchURL,
		strings.NewReader(formData.Encode()),
	)
	if err != nil {
		return webSearchResponse{
			Error: fmt.Sprintf("create request: %v", err),
		}, nil
	}
	httpReq.Header.Set("Content-Type", ddgFormContentType)
	httpReq.Header.Set("User-Agent", ddgUserAgent)

	client := &http.Client{Timeout: ddgHTTPTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return webSearchResponse{
			Error: fmt.Sprintf("request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return webSearchResponse{
			Error: fmt.Sprintf("HTTP error: %d", resp.StatusCode),
		}, nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return webSearchResponse{
			Error: fmt.Sprintf("read response: %v", err),
		}, nil
	}

	results := parseDDGHTML(string(bodyBytes), limit)
	return webSearchResponse{
		Query:   query,
		Results: results,
		Summary: fmt.Sprintf(
			"Found %d results for %q",
			len(results),
			query,
		),
	}, nil
}

func parseDDGHTML(html string, limit int) []webSearchResult {
	limit = sanitizeSearchLimit(limit)

	linkRe := regexp.MustCompile(ddgLinkPattern)
	linkMatches := linkRe.FindAllStringSubmatch(html, -1)

	snippetRe := regexp.MustCompile(ddgSnippetPattern)
	snippetMatches := snippetRe.FindAllStringSubmatch(html, -1)

	results := make([]webSearchResult, 0, limit)
	for i, match := range linkMatches {
		if len(results) >= limit {
			break
		}
		if len(match) < 3 {
			continue
		}

		targetURL := normalizeSearchURL(match[1])
		title := cleanHTML(strings.TrimSpace(match[2]))
		if title == "" || !isSearchResultURL(targetURL) {
			continue
		}

		snippet := ""
		if i < len(snippetMatches) && len(snippetMatches[i]) > 1 {
			snippet = cleanHTML(snippetMatches[i][1])
		}

		results = append(results, webSearchResult{
			Title:   title,
			URL:     targetURL,
			Snippet: snippet,
		})
	}
	return results
}

func sanitizeSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultMaxResults
	}
	if limit > maxSearchResults {
		return maxSearchResults
	}
	return limit
}

func normalizeSearchURL(raw string) string {
	replaced := strings.ReplaceAll(raw, "&amp;", "&")
	parsed, err := url.Parse(replaced)
	if err != nil {
		return replaced
	}
	if !strings.EqualFold(parsed.Host, "duckduckgo.com") ||
		parsed.Path != ddgRedirectPath {
		return replaced
	}
	target := strings.TrimSpace(parsed.Query().Get(ddgRedirectKey))
	if target == "" {
		return replaced
	}
	return target
}

func isHTTPURL(value string) bool {
	return strings.HasPrefix(value, httpPrefix) ||
		strings.HasPrefix(value, httpsPrefix)
}

func isSearchResultURL(value string) bool {
	if !isHTTPURL(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	return !strings.EqualFold(parsed.Host, duckDuckGoHost)
}

func cleanHTML(value string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	value = re.ReplaceAllString(value, "")

	value = strings.ReplaceAll(value, "&amp;", "&")
	value = strings.ReplaceAll(value, "&lt;", "<")
	value = strings.ReplaceAll(value, "&gt;", ">")
	value = strings.ReplaceAll(value, "&quot;", "\"")
	value = strings.ReplaceAll(value, "&#39;", "'")
	value = strings.ReplaceAll(value, "&#x27;", "'")
	value = strings.ReplaceAll(value, "&nbsp;", " ")
	return strings.TrimSpace(value)
}
