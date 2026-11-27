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
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// Option configures a Client before its first use.
type Option func(*Client)

// WithAPIKey sets the bearer credential. An empty key omits Authorization for
// unauthenticated local servers. Credentials are never read from the environment.
func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

// WithHTTPClient sets the HTTP client. NewClient copies its configuration,
// disables redirects on the copy, and shares its transport without closing it.
// The caller must not mutate the supplied client concurrently with construction.
// A nil client uses the standard HTTP transport.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.httpClient = client }
}

// WithDefaultModel sets the model sent when Request.Model is empty. An empty
// default omits model, allowing Laya routing; Jev callers must configure a model.
func WithDefaultModel(model string) Option {
	return func(c *Client) { c.defaultModel = model }
}

// WithTimeout sets a per-call deadline, including response reading. The default
// is 30 seconds; zero disables this deadline, and negative values are invalid.
// An earlier context deadline or supplied HTTP client timeout still applies.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) { c.timeout = timeout }
}

func (c *Client) validateOptions() error {
	if c.timeout < 0 {
		return fmt.Errorf("systemone: timeout must not be negative")
	}
	if strings.ContainsAny(c.apiKey, "\r\n") {
		return fmt.Errorf("systemone: invalid api key")
	}
	return nil
}

func normalizeBaseURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u == nil {
		return "", fmt.Errorf("systemone: invalid base url")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("systemone: invalid base url")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || strings.Contains(baseURL, "#") {
		return "", fmt.Errorf("systemone: invalid base url")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
