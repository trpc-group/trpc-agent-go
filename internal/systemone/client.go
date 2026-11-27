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
	"context"
	"fmt"
	"net/http"
	"time"
)

// Client calls a System One HTTP endpoint. Construct it with NewClient; the zero
// value is not usable. A Client supports concurrent calls if its transport does.
// It has no background workers and does not own the supplied transport.
type Client struct {
	baseURL      string
	apiKey       string
	defaultModel string
	httpClient   *http.Client
	timeout      time.Duration
}

// NewClient creates a client for an explicit HTTP(S) API root, such as
// https://api.typesafe.ai or http://localhost:8000. A gateway path prefix is
// allowed; the client appends /v1/systemone. Query, fragment, and userinfo are
// rejected. There is no provider-specific model default. Nil options are ignored.
func NewClient(baseURL string, options ...Option) (*Client, error) {
	baseURL, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	c := &Client{baseURL: baseURL, timeout: defaultTimeout}
	for _, option := range options {
		if option != nil {
			option(c)
		}
	}
	if err := c.validateOptions(); err != nil {
		return nil, err
	}
	c.httpClient = newHTTPClient(c.httpClient)
	return c, nil
}

// SystemOne evaluates all named questions against the supplied state. Requests
// are encoded once and are not mutated. Callers must not mutate request data
// while the call is running. No automatic retries or redirects are performed.
// Malformed or mismatched answers return an error and no partial response.
// Responses larger than 8 MiB are rejected. Nil contexts and requests are errors.
func (c *Client) SystemOne(ctx context.Context, req *Request) (*Response, error) {
	if err := c.validateCall(ctx); err != nil {
		return nil, err
	}

	body, questions, err := encodeRequest(req, c.defaultModel)
	if err != nil {
		return nil, fmt.Errorf("systemone: encode request: %w", err)
	}

	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	body, requestID, err := c.do(ctx, body)
	if err != nil {
		return nil, err
	}

	result, err := decodeResponse(body, questions)
	if err != nil {
		return nil, fmt.Errorf("systemone: decode response: %w", err)
	}
	result.RequestID = requestID
	return result, nil
}

func (c *Client) validateCall(ctx context.Context) error {
	if c == nil || c.httpClient == nil {
		return fmt.Errorf("systemone: client is not initialized")
	}
	if ctx == nil {
		return fmt.Errorf("systemone: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("systemone: %w", err)
	}
	return nil
}
