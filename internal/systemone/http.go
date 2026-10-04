//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
)

const maxResponseBytes = 8 << 20

// newHTTPClient copies caller configuration while keeping transport ownership
// with the caller. A nil source uses the zero-value HTTP client defaults.
func newHTTPClient(source *http.Client) *http.Client {
	client := http.Client{}
	if source != nil {
		client = *source
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

// do performs one HTTP exchange and owns the response body until it is read
// and closed. No response stream or transport ownership escapes this method.
func (c *Client) do(ctx context.Context, body []byte) ([]byte, string, error) {
	req, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("systemone: send request: %w", err)
	}
	defer resp.Body.Close()

	body, err = readResponse(resp)
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("X-Request-ID"), nil
}

func (c *Client) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("systemone: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

// readResponse applies HTTP status and size limits before protocol decoding.
// Non-2xx responses retain their status even when their error body is oversized.
func readResponse(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("systemone: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		truncated := len(body) > maxResponseBytes
		if truncated {
			body = body[:maxResponseBytes]
		}
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			RequestID:  resp.Header.Get("X-Request-ID"),
			RetryAfter: resp.Header.Get("Retry-After"),
			Body:       body,
			Truncated:  truncated,
		}
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("systemone: response exceeds %d bytes", maxResponseBytes)
	}
	return body, nil
}

// HTTPError reports a non-2xx response and can be inspected with errors.As.
// Error excludes the response body, which may contain sensitive provider data.
type HTTPError struct {
	StatusCode int
	RequestID  string
	// RetryAfter is the unparsed Retry-After header; the client does not retry.
	RetryAfter string
	// Body contains at most 8 MiB of the provider's unmodified error body.
	Body      []byte
	Truncated bool
}

// Error returns the HTTP status without including credentials or response data.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("systemone: http status %d", e.StatusCode)
}
