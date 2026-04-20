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
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newWebFetchTool(options WebFetchOptions) (tool.Tool, error) {
	toolOptions := options
	client := &http.Client{
		Timeout: defaultHTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if toolOptions.Timeout > 0 {
		client.Timeout = toolOptions.Timeout
	}
	return function.NewFunctionTool(
		func(ctx context.Context, in webFetchInput) (webFetchOutput, error) {
			if strings.TrimSpace(in.Prompt) == "" {
				return webFetchOutput{}, fmt.Errorf("prompt is required")
			}
			if !matchSearchDomainFilters(in.URL, toolOptions.AllowedDomains, toolOptions.BlockedDomains) {
				return webFetchOutput{}, fmt.Errorf("url is blocked by domain policy: %s", in.URL)
			}
			start := time.Now()
			finalURL, statusCode, statusText, body, contentType, err := fetchURL(ctx, client, in.URL, toolOptions)
			if err != nil {
				return webFetchOutput{}, err
			}
			content := string(body)
			if strings.Contains(strings.ToLower(contentType), "html") {
				content = extractHTMLText(body)
			}
			result, err := processFetchedContent(ctx, toolOptions, in, content, contentType)
			if err != nil {
				return webFetchOutput{}, err
			}
			return webFetchOutput{
				Bytes:      len(body),
				Code:       statusCode,
				CodeText:   statusText,
				Result:     result,
				DurationMs: max(time.Since(start).Milliseconds(), 1),
				URL:        finalURL,
			}, nil
		},
		function.WithName(toolWebFetch),
		function.WithDescription(webFetchDescription()),
	), nil
}

func fetchURL(
	ctx context.Context,
	client *http.Client,
	rawURL string,
	options WebFetchOptions,
) (string, int, string, []byte, string, error) {
	currentURL := rawURL
	originalHost := searchURLHost(rawURL)
	for redirects := 0; redirects < 5; redirects++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return "", 0, "", nil, "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", 0, "", nil, "", err
		}
		if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
			location := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if location == "" {
				return currentURL, resp.StatusCode, resp.Status, nil, "", nil
			}
			nextURL, err := resolveRedirectURL(currentURL, location)
			if err != nil {
				return "", 0, "", nil, "", err
			}
			if searchURLHost(nextURL) != originalHost {
				message := fmt.Sprintf("REDIRECT DETECTED: The URL redirects to a different host.\n\nOriginal URL: %s\nRedirect URL: %s\nStatus: %d %s\n\nTo complete your request, fetch the redirected URL directly.", rawURL, nextURL, resp.StatusCode, resp.Status)
				return rawURL, resp.StatusCode, resp.Status, []byte(message), "text/plain; charset=utf-8", nil
			}
			currentURL = nextURL
			continue
		}
		body, err := readHTTPBody(resp, options.MaxContentLength, options.MaxTotalContentLength)
		contentType := resp.Header.Get("Content-Type")
		statusCode := resp.StatusCode
		statusText := resp.Status
		finalURL := resp.Request.URL.String()
		_ = resp.Body.Close()
		if err != nil {
			return "", 0, "", nil, "", err
		}
		return finalURL, statusCode, statusText, body, contentType, nil
	}
	return "", 0, "", nil, "", fmt.Errorf("too many redirects")
}

func resolveRedirectURL(baseURL string, location string) (string, error) {
	baseParsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	locationParsed, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return baseParsed.ResolveReference(locationParsed).String(), nil
}

func processFetchedContent(
	ctx context.Context,
	options WebFetchOptions,
	in webFetchInput,
	content string,
	contentType string,
) (string, error) {
	if options.PromptProcessor != nil {
		return options.PromptProcessor(ctx, WebFetchPromptInput{
			URL:         in.URL,
			Prompt:      in.Prompt,
			Content:     content,
			ContentType: contentType,
		})
	}
	return trimFetchResult(content, 4000), nil
}

func webFetchDescription() string {
	return fmt.Sprintf(`Fetch one URL and process the fetched content according to a prompt.

Usage:
- url must be a fully formed URL.
- prompt is required and should describe the information to extract or summarize.
- HTML pages are converted into extracted text before prompt processing.
- When PromptProcessor is configured, the fetched content is passed to it for prompt-aware post-processing.
- Same-host redirects are followed automatically. Cross-host redirects return a redirect notice and require a second %s call with the redirected URL.
- Prefer %s first when you need to discover relevant pages, then use %s to read a selected page in depth.
- For GitHub repository, issue, or pull request metadata, prefer %s with gh when possible.`, toolWebFetch, toolWebSearch, toolWebFetch, toolBash)
}

func trimFetchResult(content string, limit int) string {
	trimmed := strings.TrimSpace(content)
	if limit <= 0 || len(trimmed) <= limit {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:limit]) + "\n\n[Content truncated due to length.]"
}
