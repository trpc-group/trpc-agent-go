//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gemini evaluates decisions using the Gemini Decisions REST API.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/decision"
)

const endpoint = "https://generativelanguage.googleapis.com/v1beta/decisions"

// Client sends requests to Google's v1beta Decisions endpoint.
type Client struct {
	model, key string
	http       *http.Client
}

var _ decision.Decider = (*Client)(nil)

// New constructs a client for model (for example "gemini-decision-1") using
// apiKey. A nil httpClient selects a 30 second timeout. Redirects are disabled
// to keep the API key on the configured service.
func New(model, apiKey string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(model) == "" || strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("gemini decisions: model and API key are required")
	}
	h := http.Client{Timeout: 30 * time.Second}
	if httpClient != nil {
		h = *httpClient
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{model: model, key: apiKey, http: &h}, nil
}

// Decide sends one batch. JSON evidence is serialized as text. A blocked
// request or a missing or malformed answer fails the entire batch.
func (c *Client) Decide(ctx context.Context, req decision.Request) (*decision.Response, error) {
	if c == nil || c.http == nil || ctx == nil {
		return nil, fmt.Errorf("gemini decisions: initialized client and context are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if len(req.Questions) > 1000 {
		return nil, fmt.Errorf("gemini decisions: maximum 1000 questions")
	}
	questions := make(map[string]question, len(req.Questions))
	for _, q := range req.Questions {
		v, err := encodeQuestion(q)
		if err != nil {
			return nil, fmt.Errorf("gemini decisions: question %q: %w", q.ID, err)
		}
		questions[q.ID] = v
	}
	input := req.Input.Text
	if req.Input.JSON != nil {
		input = string(req.Input.JSON)
	}
	body, err := json.Marshal(struct {
		Model     string              `json:"model"`
		Input     string              `json:"input"`
		Questions map[string]question `json:"questions"`
	}{c.model, input, questions})
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-goog-api-key", c.key)
	res, err := c.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("gemini decisions: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Code json.RawMessage `json:"code"`
			}
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&failure)
		return nil, fmt.Errorf("gemini decisions: HTTP %d (code %s)", res.StatusCode, failure.Error.Code)
	}
	result, err := decodeResponse(res.Body, req, questions)
	if err != nil {
		return nil, err
	}
	result.RequestID = res.Header.Get("X-Request-ID")
	return result, nil
}
