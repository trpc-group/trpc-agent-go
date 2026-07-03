//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	httpHeaderAccept        = "Accept"
	httpHeaderContentType   = "Content-Type"
	httpHeaderAuthorization = "Authorization"
	httpHeaderAppName       = "X-App-Name"
	httpHeaderUserID        = "X-User-Id"
	httpHeaderSessionID     = "X-Session-Id"
	httpHeaderSessionKey    = "X-Session-Key"
	httpHeaderAgentName     = "X-Agent-Name"
	httpContentTypeJSON     = "application/json"
	httpAuthBearerPrefix    = "Bearer "

	httpMethodGet  = "GET"
	httpMethodPost = "POST"

	pathCapture             = "/capture"
	pathRecall              = "/recall"
	pathSearchMemories      = "/search/memories"
	pathSearchConversations = "/search/conversations"
	pathEndSession          = "/session/end"
	pathHealth              = "/health"
	pathOffloadAfterTool    = "/offload/v1/hooks/after-tool-messages"
	pathOffloadBeforeModel  = "/offload/v1/hooks/before-model"
	pathOffloadReadRef      = "/offload/v1/tools/read-ref"
	pathOffloadReadNode     = "/offload/v1/tools/read-node"
	pathOffloadSearchIndex  = "/offload/v1/tools/search-index"

	maxErrorBodyPreview = 512
)

// APIError describes a non-2xx response returned by the gateway.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tencentdb memory gateway request failed: status=%d body=%s", e.StatusCode, e.Body)
}

type gatewayClient struct {
	baseURL      string
	hc           *http.Client
	timeout      time.Duration
	maxBodyBytes int64
	apiKey       string
}

func newGatewayClient(opts Options) (*gatewayClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.GatewayURL), "/")
	if baseURL == "" {
		return nil, errors.New("tencentdb memory: gateway url is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("tencentdb memory: invalid gateway url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("tencentdb memory: gateway url must be an absolute http(s) URL with host: %q", baseURL)
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	return &gatewayClient{
		baseURL:      baseURL,
		hc:           hc,
		timeout:      opts.Timeout,
		maxBodyBytes: maxBodyBytes,
		apiKey:       strings.TrimSpace(opts.APIKey),
	}, nil
}

func (c *gatewayClient) capture(ctx context.Context, req captureRequest) (*captureResponse, error) {
	var rsp captureResponse
	if err := c.doJSON(ctx, httpMethodPost, pathCapture, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) recall(ctx context.Context, req recallRequest) (*recallResponse, error) {
	var rsp recallResponse
	if err := c.doJSON(ctx, httpMethodPost, pathRecall, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) searchMemories(ctx context.Context, req searchMemoriesRequest) (*searchMemoriesResponse, error) {
	var rsp searchMemoriesResponse
	if err := c.doJSON(ctx, httpMethodPost, pathSearchMemories, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) searchConversations(ctx context.Context, req searchConversationsRequest) (*searchConversationsResponse, error) {
	var rsp searchConversationsResponse
	if err := c.doJSON(ctx, httpMethodPost, pathSearchConversations, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) endSession(ctx context.Context, req endSessionRequest) (*endSessionResponse, error) {
	var rsp endSessionResponse
	if err := c.doJSON(ctx, httpMethodPost, pathEndSession, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) health(ctx context.Context) (*HealthResponse, error) {
	var rsp HealthResponse
	if err := c.doJSON(ctx, httpMethodGet, pathHealth, nil, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) offloadAfterToolMessages(
	ctx context.Context,
	req offloadAfterToolMessagesRequest,
) (*offloadAfterToolMessagesResponse, error) {
	var rsp offloadAfterToolMessagesResponse
	if err := c.doJSONWithHeaders(
		ctx,
		httpMethodPost,
		pathOffloadAfterTool,
		req,
		&rsp,
		offloadScopeHeaders(req.Scope),
	); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) offloadBeforeModel(
	ctx context.Context,
	req offloadBeforeModelRequest,
) (*offloadBeforeModelResponse, error) {
	var rsp offloadBeforeModelResponse
	if err := c.doJSONWithHeaders(
		ctx,
		httpMethodPost,
		pathOffloadBeforeModel,
		req,
		&rsp,
		offloadScopeHeaders(req.Scope),
	); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) offloadReadRef(
	ctx context.Context,
	req offloadReadRefRequest,
) (*offloadReadRefResponse, error) {
	var rsp offloadReadRefResponse
	if err := c.doJSONWithHeaders(
		ctx,
		httpMethodPost,
		pathOffloadReadRef,
		req,
		&rsp,
		offloadScopeHeaders(req.Scope),
	); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) offloadReadNode(
	ctx context.Context,
	req offloadReadNodeRequest,
) (*offloadReadNodeResponse, error) {
	var rsp offloadReadNodeResponse
	if err := c.doJSONWithHeaders(
		ctx,
		httpMethodPost,
		pathOffloadReadNode,
		req,
		&rsp,
		offloadScopeHeaders(req.Scope),
	); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) offloadSearchIndex(
	ctx context.Context,
	req offloadSearchIndexRequest,
) (*offloadSearchIndexResponse, error) {
	var rsp offloadSearchIndexResponse
	if err := c.doJSONWithHeaders(
		ctx,
		httpMethodPost,
		pathOffloadSearchIndex,
		req,
		&rsp,
		offloadScopeHeaders(req.Scope),
	); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	in any,
	out any,
) error {
	return c.doJSONWithHeaders(ctx, method, path, in, out, nil)
}

func (c *gatewayClient) doJSONWithHeaders(
	ctx context.Context,
	method string,
	path string,
	in any,
	out any,
	headers map[string]string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	var payload []byte
	var err error
	if in != nil {
		payload, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("tencentdb memory: marshal request failed: %w", err)
		}
	}
	return c.doJSONOnce(ctx, method, c.baseURL+path, payload, out, path != pathHealth, headers)
}

func (c *gatewayClient) doJSONOnce(
	ctx context.Context,
	method string,
	urlStr string,
	payload []byte,
	out any,
	authorize bool,
	extraHeaders ...map[string]string,
) error {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		return fmt.Errorf("tencentdb memory: build request failed: %w", err)
	}
	req.Header.Set(httpHeaderAccept, httpContentTypeJSON)
	if payload != nil {
		req.Header.Set(httpHeaderContentType, httpContentTypeJSON)
	}
	if authorize && c.apiKey != "" {
		req.Header.Set(httpHeaderAuthorization, httpAuthBearerPrefix+c.apiKey)
	}
	for _, headers := range extraHeaders {
		for key, value := range headers {
			if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
				req.Header.Set(key, value)
			}
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("tencentdb memory: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("tencentdb memory: read response failed: %w", err)
	}
	if int64(len(respBody)) > c.maxBodyBytes {
		return errors.New("tencentdb memory: response body too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := string(respBody)
		if len(preview) > maxErrorBodyPreview {
			preview = preview[:maxErrorBodyPreview] + "...(truncated)"
		}
		return &APIError{StatusCode: resp.StatusCode, Body: preview}
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("tencentdb memory: unmarshal response failed: %w", err)
	}
	return nil
}

func offloadScopeHeaders(scope offloadScope) map[string]string {
	return map[string]string{
		httpHeaderAppName:    scope.AppName,
		httpHeaderUserID:     scope.UserID,
		httpHeaderSessionID:  scope.SessionID,
		httpHeaderSessionKey: scope.SessionKey,
		httpHeaderAgentName:  scope.AgentName,
	}
}
