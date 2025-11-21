//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package webfetch

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
)

const (
	defaultTimeout = 30 * time.Second
	maxURLs        = 20
)

// Option configures the WebFetch tool.
type Option func(*config)

type config struct {
	httpClient *http.Client
}

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *config) {
		cfg.httpClient = c
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
		client: cfg.httpClient,
	}

	return function.NewFunctionTool(
		t.fetch,
		function.WithName("web_fetch"),
		function.WithDescription("Fetches and extracts text content from a list of URLs. "+
			"Supports up to 20 URLs. Useful for summarizing, comparing, or extracting information from web pages."),
	)
}

type webFetchTool struct {
	client *http.Client
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
			content, statusCode, err := t.fetchOne(ctx, urlStr)
			if err != nil {
				results[index] = resultItem{
					RetrievedURL: urlStr,
					StatusCode:   statusCode,
					Error:        err.Error(),
				}
			} else {
				results[index] = resultItem{
					RetrievedURL: urlStr,
					StatusCode:   statusCode,
					Content:      content,
				}
			}
		}(i, u)
	}
	wg.Wait()

	return fetchResponse{
		Results: results,
		Summary: fmt.Sprintf("Fetched %d URLs", len(targetURLs)),
	}, nil
}

func (t *webFetchTool) fetchOne(ctx context.Context, urlStr string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "trpc-agent-go/web-fetch")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", resp.StatusCode, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	// Parse media type (ignore parameters like charset)
	mediaType := strings.Split(contentType, ";")[0]
	mediaType = strings.TrimSpace(mediaType)

	if mediaType == "text/html" {
		content, err := convertHTMLToMarkdown(resp.Body)
		return content, resp.StatusCode, err
	}

	if isSupportedTextType(mediaType) {
		content, err := readBodyAsString(resp.Body)
		return content, resp.StatusCode, err
	}

	return "", resp.StatusCode, fmt.Errorf("unsupported content type: %s", mediaType)
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
