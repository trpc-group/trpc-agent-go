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
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	httpHeaderAccept        = "Accept"
	httpHeaderContentType   = "Content-Type"
	httpHeaderAuthorization = "Authorization"
	httpHeaderServiceID     = "X-TDAI-Service-Id"
	httpContentTypeJSON     = "application/json"
	httpAuthBearerPrefix    = "Bearer "
	v3LocalBearerToken      = "local"

	httpMethodGet  = "GET"
	httpMethodPost = "POST"

	pathCapture              = "/capture"
	pathRecall               = "/recall"
	pathSearchMemories       = "/search/memories"
	pathSearchConversations  = "/search/conversations"
	pathEndSession           = "/session/end"
	pathHealth               = "/health"
	pathOffloadIngest        = "/v2/offload/ingest"
	pathOffloadCompact       = "/v2/offload/compact"
	pathOffloadReadRef       = "/v2/offload/read-ref"
	pathV3ConversationAdd    = "/v3/conversation/add"
	pathV3ConversationSearch = "/v3/conversation/search"
	pathV3AtomicSearch       = "/v3/atomic/search"
	pathV3ScenarioList       = "/v3/scenario/ls"
	pathV3ScenarioRead       = "/v3/scenario/read"
	pathV3CoreRead           = "/v3/core/read"

	maxErrorBodyPreview           = 512
	maxV3ConversationBatchSize    = 100
	maxV3MessageContentUTF16Units = 8192
	maxV3SearchQueryUTF16Units    = 2048
	v3TruncationMarker            = "\n...[truncated]"
)

// APIError describes a non-2xx response returned by the gateway.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tencentdb memory gateway request failed: status=%d body=%s", e.StatusCode, e.Body)
}

type apiMode uint8

const (
	apiModeLegacy apiMode = iota
	apiModeV3
)

type gatewayClient struct {
	baseURL      string
	hc           *http.Client
	timeout      time.Duration
	maxBodyBytes int64
	apiKey       string
	mode         apiMode
	identity     *serviceIdentity
}

type offloadGatewayClient struct {
	gateway   *gatewayClient
	serviceID string
}

func newGatewayClient(opts Options) (*gatewayClient, error) {
	return newGatewayClientWithMode(opts, apiModeLegacy, nil)
}

func newGatewayClientWithMode(
	opts Options,
	mode apiMode,
	identity *serviceIdentity,
) (*gatewayClient, error) {
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
	apiKey := strings.TrimSpace(opts.APIKey)
	switch mode {
	case apiModeLegacy:
		if identity != nil {
			return nil, errors.New("tencentdb memory: legacy API does not accept service identity")
		}
	case apiModeV3:
		if err := validateServiceIdentity(identity); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("tencentdb memory: unsupported API mode: %d", mode)
	}
	return &gatewayClient{
		baseURL:      baseURL,
		hc:           hc,
		timeout:      opts.Timeout,
		maxBodyBytes: maxBodyBytes,
		apiKey:       apiKey,
		mode:         mode,
		identity:     identity,
	}, nil
}

func newOffloadGatewayClient(opts Options) (*offloadGatewayClient, error) {
	if !validCompactionRatio(opts.ContextOffload.CompactionRatio) {
		return nil, errors.New(
			"tencentdb memory: context offload compaction ratio must be in (0, 2]",
		)
	}
	offloadOpts := opts
	if opts.ContextOffload.GatewayURL != "" {
		offloadOpts.GatewayURL = opts.ContextOffload.GatewayURL
	}
	if opts.ContextOffload.APIKey != "" {
		offloadOpts.APIKey = opts.ContextOffload.APIKey
	}
	if strings.TrimSpace(offloadOpts.APIKey) == "" {
		return nil, errors.New("tencentdb memory: context offload API key is required")
	}
	serviceID := strings.TrimSpace(opts.ContextOffload.ServiceID)
	if serviceID == "" {
		return nil, errors.New("tencentdb memory: context offload service ID is required")
	}
	client, err := newGatewayClient(offloadOpts)
	if err != nil {
		return nil, err
	}
	return &offloadGatewayClient{
		gateway:   client,
		serviceID: serviceID,
	}, nil
}

func validCompactionRatio(ratio float64) bool {
	return ratio > 0 && ratio <= 2 && !math.IsNaN(ratio) && !math.IsInf(ratio, 0)
}

func (c *gatewayClient) capture(ctx context.Context, req captureRequest) (*captureResponse, error) {
	if c.usesV3API() {
		return c.captureV3(ctx, req)
	}
	var rsp captureResponse
	if err := c.doJSON(ctx, httpMethodPost, pathCapture, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) recall(ctx context.Context, req recallRequest) (*recallResponse, error) {
	if c.usesV3API() {
		return c.recallV3(ctx, req)
	}
	var rsp recallResponse
	if err := c.doJSON(ctx, httpMethodPost, pathRecall, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) searchMemories(ctx context.Context, req searchMemoriesRequest) (*searchMemoriesResponse, error) {
	if c.usesV3API() {
		return c.searchMemoriesV3(ctx, req)
	}
	var rsp searchMemoriesResponse
	if err := c.doJSON(ctx, httpMethodPost, pathSearchMemories, req, &rsp); err != nil {
		return nil, err
	}
	return &rsp, nil
}

func (c *gatewayClient) searchConversations(ctx context.Context, req searchConversationsRequest) (*searchConversationsResponse, error) {
	if c.usesV3API() {
		return c.searchConversationsV3(ctx, req)
	}
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

func (c *offloadGatewayClient) ingest(
	ctx context.Context,
	req offloadIngestRequest,
) (*offloadIngestData, error) {
	return doOffloadJSON[offloadIngestData](ctx, c, pathOffloadIngest, req)
}

func (c *offloadGatewayClient) compact(
	ctx context.Context,
	req offloadCompactRequest,
) (*offloadCompactData, error) {
	return doOffloadJSON[offloadCompactData](ctx, c, pathOffloadCompact, req)
}

func (c *offloadGatewayClient) readRef(
	ctx context.Context,
	req offloadReadRefRequest,
) (*offloadReadRefData, error) {
	return doOffloadJSON[offloadReadRefData](ctx, c, pathOffloadReadRef, req)
}

func doOffloadJSON[T any](
	ctx context.Context,
	client *offloadGatewayClient,
	path string,
	req any,
) (*T, error) {
	if client == nil || client.gateway == nil {
		return nil, errors.New("tencentdb memory: context offload gateway is unavailable")
	}
	var envelope offloadResponseEnvelope[T]
	if err := client.gateway.doJSONWithHeaders(
		ctx,
		httpMethodPost,
		path,
		req,
		&envelope,
		map[string]string{httpHeaderServiceID: client.serviceID},
	); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf(
			"tencentdb memory: context offload request failed: code=%d message=%s request_id=%s",
			envelope.Code,
			envelope.Message,
			envelope.RequestID,
		)
	}
	if envelope.Data == nil {
		return nil, errors.New("tencentdb memory: context offload response data is missing")
	}
	return envelope.Data, nil
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

func validateServiceIdentity(identity *serviceIdentity) error {
	if identity == nil {
		return errors.New("tencentdb memory: service identity is required")
	}
	if identity.serviceID == "" {
		return errors.New("tencentdb memory: service identity service id is required")
	}
	if identity.teamID == "" {
		return errors.New("tencentdb memory: service identity team id is required")
	}
	if identity.agentID == "" {
		return errors.New("tencentdb memory: service identity agent id is required")
	}
	return nil
}

func (c *gatewayClient) usesV3API() bool {
	return c != nil && c.mode == apiModeV3
}

func (c *gatewayClient) captureV3(
	ctx context.Context,
	req captureRequest,
) (*captureResponse, error) {
	messages := make([]v3Message, 0, len(req.Messages))
	for _, message := range req.Messages {
		content, truncated := truncateV3MessageContent(message.Content)
		if truncated {
			log.Warnf(
				"tencentdb memory: v3 capture truncated oversized message: role=%s original_utf16_units=%d limit=%d",
				message.Role,
				v3UTF16Length(message.Content),
				maxV3MessageContentUTF16Units,
			)
		}
		messages = append(messages, v3Message{
			Role:      message.Role,
			Content:   content,
			Timestamp: formatV3Timestamp(message.Timestamp),
		})
	}
	recorded := 0
	for start := 0; start < len(messages); start += maxV3ConversationBatchSize {
		end := start + maxV3ConversationBatchSize
		if end > len(messages) {
			end = len(messages)
		}
		data, err := doV3JSON[v3ConversationAddData](
			ctx,
			c,
			pathV3ConversationAdd,
			v3ConversationAddRequest{
				v3Isolation: c.v3Isolation(req.UserID, req.SessionID),
				Messages:    messages[start:end],
			},
		)
		if err != nil {
			// The V3 API has no client write-idempotency contract. Treat the
			// complete capture as failed so the service checkpoint stays put and
			// a later ingest replays the transcript instead of dropping messages.
			return nil, err
		}
		batchSize := end - start
		if len(data.AcceptedIDs) < batchSize || data.TotalCount < batchSize {
			return nil, fmt.Errorf(
				"tencentdb memory: v3 capture partially accepted messages: accepted_ids=%d total_count=%d submitted=%d",
				len(data.AcceptedIDs),
				data.TotalCount,
				batchSize,
			)
		}
		recorded += len(data.AcceptedIDs)
	}
	return &captureResponse{
		L0Recorded: recorded,
	}, nil
}

func (c *gatewayClient) recallV3(
	ctx context.Context,
	req recallRequest,
) (*recallResponse, error) {
	isolation := c.v3Isolation(req.UserID, "")
	var (
		atomicData   *v3AtomicSearchData
		atomicErr    error
		scenarioData *v3ScenarioListData
		scenarioErr  error
		coreData     *v3CoreFile
		coreErr      error
		wg           sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		atomicData, atomicErr = doV3JSON[v3AtomicSearchData](
			ctx,
			c,
			pathV3AtomicSearch,
			v3AtomicSearchRequest{
				v3Isolation: isolation,
				Query:       truncateV3SearchQuery(req.Query),
				Limit:       defaultSearchLimit,
			},
		)
	}()
	go func() {
		defer wg.Done()
		scenarioData, scenarioErr = doV3JSON[v3ScenarioListData](
			ctx,
			c,
			pathV3ScenarioList,
			v3ScenarioListRequest{v3Isolation: isolation},
		)
	}()
	go func() {
		defer wg.Done()
		coreData, coreErr = doV3JSON[v3CoreFile](
			ctx,
			c,
			pathV3CoreRead,
			v3CoreReadRequest{v3Isolation: isolation},
		)
	}()
	wg.Wait()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	errs := []error{atomicErr, scenarioErr, coreErr}
	if atomicErr != nil && scenarioErr != nil && coreErr != nil {
		return nil, fmt.Errorf("tencentdb memory: v3 recall failed: %w", errors.Join(errs...))
	}
	for _, err := range errs {
		if err != nil {
			log.Warnf("tencentdb memory: partial v3 recall failed: %v", err)
		}
	}
	return buildV3RecallResponse(atomicData, scenarioData, coreData), nil
}

func (c *gatewayClient) searchMemoriesV3(
	ctx context.Context,
	req searchMemoriesRequest,
) (*searchMemoriesResponse, error) {
	if strings.TrimSpace(req.Scene) != "" {
		return nil, errors.New("tencentdb memory: scene filtering is not supported by the identity-scoped API")
	}
	data, err := doV3JSON[v3AtomicSearchData](
		ctx,
		c,
		pathV3AtomicSearch,
		v3AtomicSearchRequest{
			v3Isolation: c.v3Isolation(req.UserID, ""),
			Query:       truncateV3SearchQuery(req.Query),
			Limit:       req.Limit,
			Type:        req.Type,
		},
	)
	if err != nil {
		return nil, err
	}
	return &searchMemoriesResponse{
		Results:  formatV3AtomicItems(data.Items),
		Total:    len(data.Items),
		Strategy: "v3-atomic",
	}, nil
}

func (c *gatewayClient) searchConversationsV3(
	ctx context.Context,
	req searchConversationsRequest,
) (*searchConversationsResponse, error) {
	data, err := doV3JSON[v3ConversationSearchData](
		ctx,
		c,
		pathV3ConversationSearch,
		v3ConversationSearchRequest{
			v3Isolation: c.v3Isolation(req.UserID, req.SessionID),
			Query:       truncateV3SearchQuery(req.Query),
			Limit:       req.Limit,
		},
	)
	if err != nil {
		return nil, err
	}
	return &searchConversationsResponse{
		Results: formatV3ConversationHits(data.Messages),
		Total:   len(data.Messages),
	}, nil
}

func (c *gatewayClient) readScenarioV3(
	ctx context.Context,
	userID string,
	path string,
) (*v3ScenarioFile, error) {
	if !c.usesV3API() {
		return nil, errors.New(
			"tencentdb memory: scenario read requires the V3 API",
		)
	}
	return doV3JSON[v3ScenarioFile](
		ctx,
		c,
		pathV3ScenarioRead,
		v3ScenarioReadRequest{
			v3Isolation: c.v3Isolation(userID, ""),
			Path:        strings.TrimSpace(path),
		},
	)
}

func (c *gatewayClient) v3Isolation(userID, sessionID string) v3Isolation {
	return v3Isolation{
		TeamID:    c.identity.teamID,
		AgentID:   c.identity.agentID,
		UserID:    strings.TrimSpace(userID),
		SessionID: strings.TrimSpace(sessionID),
	}
}

func doV3JSON[T any](
	ctx context.Context,
	client *gatewayClient,
	path string,
	req any,
) (*T, error) {
	headers := map[string]string{
		httpHeaderServiceID: client.identity.serviceID,
	}
	if client.apiKey == "" {
		// The self-hosted gateway still parses a non-empty Bearer token when
		// its shared-secret check is disabled. Match the upstream client so
		// unauthenticated local V3 deployments remain usable.
		headers[httpHeaderAuthorization] = httpAuthBearerPrefix + v3LocalBearerToken
	}
	var envelope v3ResponseEnvelope[T]
	if err := client.doJSONWithHeaders(
		ctx,
		httpMethodPost,
		path,
		req,
		&envelope,
		headers,
	); err != nil {
		return nil, err
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf(
			"tencentdb memory: v3 request failed: path=%s code=%d message=%s request_id=%s",
			path,
			envelope.Code,
			envelope.Message,
			envelope.RequestID,
		)
	}
	if envelope.Data == nil {
		return nil, fmt.Errorf("tencentdb memory: v3 response data is missing: path=%s", path)
	}
	return envelope.Data, nil
}

func formatV3Timestamp(timestamp int64) string {
	if timestamp <= 0 {
		return ""
	}
	return time.UnixMilli(timestamp).UTC().Format(time.RFC3339Nano)
}

func truncateV3MessageContent(content string) (string, bool) {
	if v3UTF16Length(content) <= maxV3MessageContentUTF16Units {
		return content, false
	}
	budget := maxV3MessageContentUTF16Units -
		v3UTF16Length(v3TruncationMarker)
	return truncateV3UTF16(content, budget) +
		v3TruncationMarker, true
}

func truncateV3SearchQuery(query string) string {
	return truncateV3UTF16(query, maxV3SearchQueryUTF16Units)
}

func truncateV3UTF16(value string, maxUnits int) string {
	if maxUnits <= 0 {
		return ""
	}
	units := 0
	for i, r := range value {
		width := v3UTF16RuneWidth(r)
		if units+width > maxUnits {
			return value[:i]
		}
		units += width
	}
	return value
}

func v3UTF16Length(value string) int {
	units := 0
	for _, r := range value {
		units += v3UTF16RuneWidth(r)
	}
	return units
}

func v3UTF16RuneWidth(r rune) int {
	if r > 0xffff {
		return 2
	}
	return 1
}

func buildV3RecallResponse(
	atomicData *v3AtomicSearchData,
	scenarioData *v3ScenarioListData,
	coreData *v3CoreFile,
) *recallResponse {
	systemParts := make([]string, 0, 2)
	var prependContext string
	memoryCount := 0
	remaining := maxV3RecallContextBytes
	if atomicData != nil && len(atomicData.Items) > 0 {
		prependContext = formatV3RecallSection(
			"relevant-memories",
			formatV3AtomicItems(atomicData.Items),
			min(maxV3AtomicRecallSectionBytes, remaining),
		)
		if prependContext != "" {
			remaining -= len(prependContext)
			memoryCount += len(atomicData.Items)
		}
	}
	if coreData != nil {
		if content := strings.TrimSpace(coreData.Content); content != "" {
			// L3 core is shared by the service/team/agent, not a per-user profile.
			var added bool
			systemParts, remaining, added = appendV3RecallSystemSection(
				systemParts,
				remaining,
				"agent-core",
				content,
				maxV3CoreRecallSectionBytes,
			)
			if added {
				memoryCount++
			}
		}
	}
	if scenarioData != nil && len(scenarioData.Entries) > 0 {
		context, count := formatV3ScenarioEntries(scenarioData.Entries)
		if context != "" {
			var added bool
			systemParts, remaining, added = appendV3RecallSystemSection(
				systemParts,
				remaining,
				"scene-navigation",
				context,
				remaining,
			)
			if added {
				memoryCount += count
			}
		}
	}
	return &recallResponse{
		AppendSystemContext: strings.Join(systemParts, "\n\n"),
		PrependContext:      prependContext,
		Strategy:            "v3-identity-scoped",
		MemoryCount:         memoryCount,
	}
}

func formatV3AtomicItems(items []v3AtomicSearchHit) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		memoryType := strings.TrimSpace(item.Type)
		if memoryType == "" {
			memoryType = "memory"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", memoryType, content))
	}
	return strings.Join(lines, "\n")
}

func formatV3ConversationHits(items []v3ConversationSearchHit) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = "message"
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", role, content))
	}
	return strings.Join(lines, "\n")
}
