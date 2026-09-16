//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package duckduckgo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

const maxSERPBodyBytes = 512 * 1024

var errSERPChallenge = errors.New(
	"duckduckgo returned an anti-bot challenge page",
)

var errAPIFallbackNoResults = errors.New(
	"api fallback returned no results",
)

func (t *ddgTool) searchSERPWithFallback(
	ctx context.Context,
	req searchRequest,
) (searchResponse, error) {
	return t.searchSERPWithFallbackForBackend(
		ctx,
		req,
		t.backend,
		t.baseURL,
	)
}

func (t *ddgTool) searchSERPWithFallbackForBackend(
	ctx context.Context,
	req searchRequest,
	backend string,
	baseURL string,
) (searchResponse, error) {
	result, err := t.searchSERPWithSchemeFallback(
		ctx,
		req,
		backend,
		baseURL,
	)
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, err
	}
	fallbackBackend := fallbackSERPBackend(backend)
	fallbackURL := fallbackSERPBaseURL(backend, baseURL)
	if fallbackBackend == "" || fallbackURL == "" {
		return result, err
	}
	fallback, fallbackErr := t.searchSERPWithSchemeFallback(
		ctx,
		req,
		fallbackBackend,
		fallbackURL,
	)
	if fallbackErr == nil {
		if strings.TrimSpace(fallback.Summary) != "" {
			fallback.Summary += fmt.Sprintf(
				" (fallback from %s)",
				backend,
			)
		}
		return fallback, nil
	}
	if isSERPRouteBlocker(err, fallbackErr) &&
		!isDefaultSERPBaseURL(backend, baseURL) {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: "DuckDuckGo html and lite search pages are both " +
				"unavailable for this query due to transport errors " +
				"or anti-bot challenge pages; use direct URLs with " +
				"web_fetch/browser or another configured search " +
				"provider instead of immediately retrying DuckDuckGo",
		}, nil
	}
	apiFallback, apiFallbackErr := t.searchAPIFallbackAfterSERPFailure(
		ctx,
		req,
		backend,
		baseURL,
	)
	if apiFallbackErr == nil {
		if strings.TrimSpace(apiFallback.Summary) != "" {
			apiFallback.Summary += fmt.Sprintf(
				" (fallback from %s/%s after SERP failure)",
				backend,
				fallbackBackend,
			)
		}
		return apiFallback, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(apiFallbackErr, ctxErr) {
			return apiFallback, apiFallbackErr
		}
		return apiFallback, fmt.Errorf(
			"%w: api fallback failed: %w",
			ctxErr,
			apiFallbackErr,
		)
	}
	if isSERPRouteBlocker(err, fallbackErr) &&
		errors.Is(apiFallbackErr, errAPIFallbackNoResults) {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: "DuckDuckGo html and lite search pages are both " +
				"unavailable for this query due to transport errors " +
				"or anti-bot challenge pages, and the Instant Answer " +
				"API fallback did not return web results; use direct " +
				"URLs with web_fetch/browser or another configured " +
				"search provider instead of immediately retrying " +
				"DuckDuckGo",
		}, nil
	}
	if isSERPRouteBlocker(err, fallbackErr) &&
		isAPIFallbackTransportIncompatible(apiFallbackErr) {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: "DuckDuckGo html and lite search pages are both " +
				"unavailable for this query due to transport errors " +
				"or anti-bot challenge pages, and the Instant Answer " +
				"API fallback also failed due to HTTPS transport " +
				"incompatibility; use direct URLs with web_fetch/" +
				"browser or another configured search provider " +
				"instead of immediately retrying DuckDuckGo",
		}, nil
	}
	if isSERPRouteBlocker(err, fallbackErr) &&
		isRetryableAPIStatus(apiFallbackErr) {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: "DuckDuckGo html and lite search pages are both " +
				"unavailable for this query due to transport errors " +
				"or anti-bot challenge pages, and the Instant Answer " +
				"API fallback returned a retryable unavailable status; " +
				"use direct URLs with web_fetch/browser or another " +
				"configured search provider instead of immediately " +
				"retrying DuckDuckGo",
		}, nil
	}
	result.Summary = fmt.Sprintf(
		"%s; fallback %s failed: %v; api fallback failed: %v",
		result.Summary,
		fallbackBackend,
		fallbackErr,
		apiFallbackErr,
	)
	return result, fmt.Errorf(
		"%w; fallback %s failed: %w; api fallback failed: %w",
		err,
		fallbackBackend,
		fallbackErr,
		apiFallbackErr,
	)
}

func (t *ddgTool) searchSERPWithSchemeFallback(
	ctx context.Context,
	req searchRequest,
	backend string,
	baseURL string,
) (searchResponse, error) {
	result, err := t.searchSERP(ctx, req, backend, baseURL)
	if err == nil || ctx.Err() != nil || !shouldRetrySERPWithHTTP(err) {
		return result, err
	}
	httpURL := httpFallbackSERPBaseURL(baseURL)
	if httpURL == "" {
		return result, err
	}
	fallback, fallbackErr := t.searchSERP(ctx, req, backend, httpURL)
	if fallbackErr == nil {
		if strings.TrimSpace(fallback.Summary) != "" {
			fallback.Summary += " (http fallback from https)"
		}
		return fallback, nil
	}
	result.Summary = fmt.Sprintf(
		"%s; http fallback failed: %v",
		result.Summary,
		fallbackErr,
	)
	return result, fmt.Errorf(
		"%w; http fallback failed: %w",
		err,
		fallbackErr,
	)
}

func (t *ddgTool) searchSERP(
	ctx context.Context,
	req searchRequest,
	backend string,
	baseURL string,
) (searchResponse, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return searchResponse{}, err
	}
	query := u.Query()
	query.Set("q", req.Query)
	u.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		u.String(),
		nil,
	)
	if err != nil {
		return searchResponse{}, err
	}
	if strings.TrimSpace(t.userAgent) != "" {
		httpReq.Header.Set("User-Agent", t.userAgent)
	}
	httpReq.Header.Set(
		"Accept",
		"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	)
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: fmt.Sprintf("Error performing search: %v", err),
		}, fmt.Errorf("error performing search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSERPBodyBytes))
	if err != nil {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: fmt.Sprintf("Error reading search response: %v", err),
		}, fmt.Errorf("error reading search response: %w", err)
	}
	if resp.StatusCode < http.StatusOK ||
		resp.StatusCode >= http.StatusMultipleChoices {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: fmt.Sprintf("Search returned status %d", resp.StatusCode),
		}, fmt.Errorf("search returned status %d", resp.StatusCode)
	}
	if isDuckDuckGoChallenge(body) {
		return searchResponse{
			Query:   req.Query,
			Results: []resultItem{},
			Summary: "DuckDuckGo returned an anti-bot challenge page; " +
				"use direct URLs with web_fetch/browser or another " +
				"configured search provider instead of immediately " +
				"retrying the same query",
		}, errSERPChallenge
	}

	results := parseSERPResults(body)
	summary := fmt.Sprintf(
		"Found %d %s results for query '%s'",
		len(results),
		backend,
		req.Query,
	)
	return searchResponse{
		Query:   req.Query,
		Results: results,
		Summary: summary,
	}, nil
}

func isSERPChallengeError(err error) bool {
	return errors.Is(err, errSERPChallenge)
}

func isSERPRouteBlocker(err error, fallbackErr error) bool {
	return isSERPUnavailableError(err) &&
		isSERPUnavailableError(fallbackErr)
}

func isSERPUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if isSERPChallengeError(err) || shouldRetrySERPWithHTTP(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "search returned status 429") ||
		strings.Contains(msg, "search returned status 502") ||
		strings.Contains(msg, "search returned status 503")
}

func isAPIFallbackTransportIncompatible(err error) bool {
	return shouldRetrySERPWithHTTP(err)
}

func fallbackSERPBackend(backend string) string {
	switch backend {
	case backendHTML:
		return backendLite
	case backendLite:
		return backendHTML
	default:
		return ""
	}
}

func (t *ddgTool) searchAPIFallbackAfterSERPFailure(
	ctx context.Context,
	req searchRequest,
	backend string,
	baseURL string,
) (searchResponse, error) {
	if err := ctx.Err(); err != nil {
		return searchResponse{}, err
	}
	if !isDefaultSERPBaseURL(backend, baseURL) {
		return searchResponse{}, fmt.Errorf(
			"api fallback is disabled for non-default %s base URL %q",
			backend,
			baseURL,
		)
	}
	result, err := t.searchAPIWithDefaultBaseURL(ctx, req)
	if err != nil {
		return searchResponse{}, err
	}
	if len(result.Results) == 0 {
		return searchResponse{}, errAPIFallbackNoResults
	}
	return result, nil
}

func fallbackSERPBaseURL(backend string, baseURL string) string {
	fallbackBackend := fallbackSERPBackend(backend)
	if fallbackBackend == "" {
		return ""
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" || baseURL == defaultBaseURLForBackend(backend) {
		return defaultBaseURLForBackend(fallbackBackend)
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	from := "/" + backend + "/"
	to := "/" + fallbackBackend + "/"
	if strings.Contains(u.Path, from) {
		u.Path = strings.Replace(u.Path, from, to, 1)
		return u.String()
	}
	from = "/" + backend
	to = "/" + fallbackBackend
	if strings.HasSuffix(u.Path, from) {
		u.Path = strings.TrimSuffix(u.Path, from) + to
		return u.String()
	}
	return ""
}

func isDefaultSERPBaseURL(backend string, baseURL string) bool {
	baseURL = strings.TrimSpace(baseURL)
	return baseURL == "" || baseURL == defaultBaseURLForBackend(backend)
}

func apiFallbackSERPBaseURL(apiBaseURL string) string {
	apiBaseURL = strings.TrimSpace(apiBaseURL)
	if apiBaseURL == "" || apiBaseURL == defaultBaseURL {
		return defaultHTMLBaseURL
	}
	return apiBaseURL
}

func httpFallbackSERPBaseURL(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") {
		return ""
	}
	u.Scheme = "http"
	return u.String()
}

func shouldRetrySERPWithHTTP(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(
		msg,
		"server gave http response to https client",
	) || strings.Contains(
		msg,
		"first record does not look like a tls handshake",
	) || strings.Contains(msg, "wrong version number")
}

func shouldFallbackFromAPIError(err error) bool {
	return shouldRetrySERPWithHTTP(err) ||
		isRetryableAPIStatus(err) ||
		isRetryableAPIParseError(err)
}

func isRetryableAPIStatus(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(
		strings.ToLower(err.Error()),
		"api returned status 202",
	)
}

func isRetryableAPIParseError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(
		strings.ToLower(err.Error()),
		"failed to parse response",
	)
}

func isDuckDuckGoChallenge(body []byte) bool {
	text := string(body)
	return strings.Contains(text, "anomaly-modal") ||
		strings.Contains(text, "Unfortunately, bots use DuckDuckGo too")
}

func parseSERPResults(body []byte) []resultItem {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	type partialResult struct {
		Title       string
		URL         string
		Description string
	}
	results := make([]partialResult, 0, maxResults)
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode &&
			node.Data == "a" &&
			(htmlHasClass(node, "result__a") ||
				htmlHasClass(node, "result-link")) {
			results = append(results, partialResult{
				Title: htmlNodeText(node),
				URL:   htmlAttr(node, "href"),
			})
		}
		if node.Type == html.ElementNode &&
			(htmlHasClass(node, "result__snippet") ||
				htmlHasClass(node, "result-snippet")) {
			if len(results) > 0 &&
				results[len(results)-1].Description == "" {
				results[len(results)-1].Description = htmlNodeText(node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)

	out := make([]resultItem, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, item := range results {
		normalizedURL := normalizeSERPURL(item.URL)
		if normalizedURL == "" || isSERPAdURL(normalizedURL) {
			continue
		}
		if _, ok := seen[normalizedURL]; ok {
			continue
		}
		seen[normalizedURL] = struct{}{}
		out = append(out, resultItem{
			Title:       strings.TrimSpace(item.Title),
			URL:         normalizedURL,
			Description: collapseWhitespace(item.Description),
		})
		if len(out) >= maxResults {
			break
		}
	}
	return out
}

func normalizeSERPURL(rawURL string) string {
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
	if strings.HasPrefix(trimmed, "//") {
		return "https:" + trimmed
	}
	return trimmed
}

func isSERPAdURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	path := strings.ToLower(parsed.EscapedPath())
	query := strings.ToLower(parsed.RawQuery)
	if host == "duckduckgo.com" &&
		(path == "/y.js" ||
			strings.Contains(query, "ad_domain=") ||
			strings.Contains(query, "ad_provider=")) {
		return true
	}
	if host == "bing.com" && strings.HasPrefix(path, "/aclick") {
		return true
	}
	return false
}

func htmlAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
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
	return collapseWhitespace(strings.Join(parts, " "))
}

func collapseWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
