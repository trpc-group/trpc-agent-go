//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package bedrock

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
)

type headerHTTPClient struct {
	base    aws.HTTPClient
	headers map[string]string
}

func (h *headerHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultClient
	}
	req2 := req.Clone(req.Context())
	for k, v := range h.headers {
		if req2.Header.Get(k) == "" {
			req2.Header.Set(k, v)
		}
	}
	return base.Do(req2)
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	req2 := req.Clone(req.Context())
	for k, v := range h.headers {
		if req2.Header.Get(k) == "" {
			req2.Header.Set(k, v)
		}
	}
	return base.RoundTrip(req2)
}

func cloneHeaderMap(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	return out
}

func wrapAWSHTTPClient(existing aws.HTTPClient, headers map[string]string) aws.HTTPClient {
	if len(headers) == 0 {
		return existing
	}
	return &headerHTTPClient{
		base:    existing,
		headers: cloneHeaderMap(headers),
	}
}
