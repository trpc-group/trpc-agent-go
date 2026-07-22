//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxHTTPAttempts = 3
	initialBackoff  = 100 * time.Millisecond
)

type apiClient struct {
	baseURL        string
	httpClient     *http.Client
	headers        http.Header
	timeout        time.Duration
	ownedTransport *http.Transport
}

type requestSpec struct {
	method           string
	path             string
	body             []byte
	expectedStatuses map[int]struct{}
}

type attemptResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

type apiError struct {
	statusCode int
	code       string
	message    string
	traceID    string
	retryAfter string
}

func (e *apiError) Error() string {
	detail := e.message
	if detail == "" {
		detail = e.code
	}
	if detail == "" {
		detail = http.StatusText(e.statusCode)
	}
	if e.traceID != "" {
		return fmt.Sprintf("chromadb status %d: %s (trace %s)", e.statusCode, detail, e.traceID)
	}
	return fmt.Sprintf("chromadb status %d: %s", e.statusCode, detail)
}

func newAPIClient(opts serviceOpts) (*apiClient, error) {
	parsed, err := url.Parse(opts.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("base URL scheme must be http or https: %s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, errors.New("base URL host is required")
	}

	httpClient := opts.httpClient
	var ownedTransport *http.Transport
	if httpClient == nil {
		ownedTransport = http.DefaultTransport.(*http.Transport).Clone()
		httpClient = &http.Client{Transport: ownedTransport}
	}

	headers := make(http.Header, len(opts.headers)+2)
	for name, value := range opts.headers {
		headers.Set(name, value)
	}
	if opts.apiKey != "" {
		headers.Set("X-Chroma-Token", opts.apiKey)
	}
	if opts.bearer != "" {
		headers.Set("Authorization", "Bearer "+opts.bearer)
	}

	return &apiClient{
		baseURL:        strings.TrimRight(opts.baseURL, "/"),
		httpClient:     httpClient,
		headers:        headers,
		timeout:        opts.timeout,
		ownedTransport: ownedTransport,
	}, nil
}

func (cl *apiClient) closeIdleConnections() {
	if cl.ownedTransport != nil {
		cl.ownedTransport.CloseIdleConnections()
	}
}

func (cl *apiClient) doJSON(ctx context.Context, spec requestSpec, in any, out any) error {
	if in != nil {
		body, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		spec.body = body
	}
	return cl.do(ctx, spec, out)
}

func (cl *apiClient) do(ctx context.Context, spec requestSpec, out any) error {
	requestCtx, cancel := context.WithTimeout(ctx, cl.timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt < maxHTTPAttempts; attempt++ {
		resp, err := cl.doAttempt(requestCtx, spec)
		if err == nil {
			return decodeAttemptResponse(resp, spec.expectedStatuses, out)
		}
		lastErr = err
		if !shouldRetry(err, attempt) {
			return err
		}
		if err := waitBeforeRetry(requestCtx, err, attempt); err != nil {
			return err
		}
	}
	return lastErr
}

func (cl *apiClient) doAttempt(
	ctx context.Context,
	spec requestSpec,
) (result *attemptResponse, resultErr error) {
	requestURL := cl.baseURL + "/" + strings.TrimLeft(spec.path, "/")
	req, err := http.NewRequestWithContext(ctx, spec.method, requestURL, bytes.NewReader(spec.body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for name, values := range cl.headers {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	req.Header.Set("Accept", "application/json")
	if len(spec.body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil && resultErr == nil {
			result = nil
			resultErr = fmt.Errorf("close response: %w", err)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	result = &attemptResponse{
		statusCode: resp.StatusCode,
		header:     resp.Header.Clone(),
		body:       body,
	}
	if _, ok := spec.expectedStatuses[resp.StatusCode]; ok {
		return result, nil
	}
	return nil, decodeAPIError(result)
}

func decodeAttemptResponse(resp *attemptResponse, expected map[int]struct{}, out any) error {
	if _, ok := expected[resp.statusCode]; !ok {
		return decodeAPIError(resp)
	}
	if out == nil || len(bytes.TrimSpace(resp.body)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(resp.body))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return err
	}
	return nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode response: multiple JSON values")
		}
		return fmt.Errorf("decode response trailer: %w", err)
	}
	return nil
}

func decodeAPIError(resp *attemptResponse) error {
	payload := struct {
		Code    string `json:"error"`
		Message string `json:"message"`
	}{}
	if len(bytes.TrimSpace(resp.body)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(resp.body))
		decoder.UseNumber()
		_ = decoder.Decode(&payload)
	}
	return &apiError{
		statusCode: resp.statusCode,
		code:       payload.Code,
		message:    payload.Message,
		traceID:    resp.header.Get("chroma-trace-id"),
		retryAfter: resp.header.Get("Retry-After"),
	}
}

func shouldRetry(err error, attempt int) bool {
	if attempt >= maxHTTPAttempts-1 {
		return false
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		switch apiErr.statusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}
	return true
}

func waitBeforeRetry(ctx context.Context, err error, attempt int) error {
	delay := initialBackoff << attempt
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		if retryAfter := retryAfterDuration(apiErr); retryAfter > 0 {
			delay = retryAfter
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryAfterDuration(err *apiError) time.Duration {
	return parseRetryAfter(err.retryAfter, time.Now())
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.Atoi(value)
	if err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func isStatus(err error, statusCode int) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.statusCode == statusCode
}

func statusSet(statusCodes ...int) map[int]struct{} {
	statuses := make(map[int]struct{}, len(statusCodes))
	for _, statusCode := range statusCodes {
		statuses[statusCode] = struct{}{}
	}
	return statuses
}

func escapePath(value string) string {
	return url.PathEscape(value)
}

type checklistResponse struct {
	MaxBatchSize           int  `json:"max_batch_size"`
	SupportsBase64Encoding bool `json:"supports_base64_encoding"`
}

type identityResponse struct {
	UserID    string   `json:"user_id"`
	Tenant    string   `json:"tenant"`
	Databases []string `json:"databases"`
}

type vectorIndexConfig struct {
	Space string `json:"space"`
}

type collectionConfiguration struct {
	HNSW  *vectorIndexConfig `json:"hnsw"`
	SPANN *vectorIndexConfig `json:"spann"`
}

type collectionResponse struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	ConfigurationJSON collectionConfiguration `json:"configuration_json"`
	Metadata          map[string]any          `json:"metadata"`
	Dimension         *int                    `json:"dimension"`
	Tenant            string                  `json:"tenant"`
	Database          string                  `json:"database"`
}

type createCollectionRequest struct {
	Name          string                        `json:"name"`
	Metadata      map[string]any                `json:"metadata,omitempty"`
	Configuration createCollectionConfiguration `json:"configuration"`
	GetOrCreate   bool                          `json:"get_or_create"`
}

type createCollectionConfiguration struct {
	HNSW vectorIndexConfig `json:"hnsw"`
}

type addRecordsRequest struct {
	IDs        []string         `json:"ids"`
	Embeddings [][]float32      `json:"embeddings"`
	Documents  []*string        `json:"documents"`
	Metadatas  []map[string]any `json:"metadatas"`
}

type updateRecordsRequest struct {
	IDs        []string         `json:"ids"`
	Embeddings [][]float32      `json:"embeddings,omitempty"`
	Documents  []*string        `json:"documents,omitempty"`
	Metadatas  []map[string]any `json:"metadatas,omitempty"`
}

type getRecordsRequest struct {
	IDs     []string       `json:"ids,omitempty"`
	Where   map[string]any `json:"where,omitempty"`
	Limit   *int           `json:"limit,omitempty"`
	Offset  *int           `json:"offset,omitempty"`
	Include *[]string      `json:"include,omitempty"`
}

type getRecordsResponse struct {
	IDs       []string          `json:"ids"`
	Documents *[]*string        `json:"documents"`
	Metadatas *[]map[string]any `json:"metadatas"`
	Include   []string          `json:"include"`
}

type queryRecordsRequest struct {
	Where           map[string]any `json:"where,omitempty"`
	QueryEmbeddings [][]float32    `json:"query_embeddings"`
	NResults        int            `json:"n_results"`
	Include         []string       `json:"include"`
}

type queryRecordsResponse struct {
	IDs       [][]string          `json:"ids"`
	Documents *[][]*string        `json:"documents"`
	Metadatas *[][]map[string]any `json:"metadatas"`
	Distances *[][]*float32       `json:"distances"`
	Include   []string            `json:"include"`
}

type deleteRecordsRequest struct {
	IDs   []string       `json:"ids,omitempty"`
	Where map[string]any `json:"where,omitempty"`
	Limit *int           `json:"limit,omitempty"`
}

type deleteRecordsResponse struct {
	Deleted int `json:"deleted"`
}

type databaseRef struct {
	tenant   string
	database string
}

type collectionRef struct {
	databaseRef
	id   string
	name string
}

func (cl *apiClient) checklist(ctx context.Context) (*checklistResponse, error) {
	response := &checklistResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:           http.MethodGet,
		path:             "/api/v2/pre-flight-checks",
		expectedStatuses: statusSet(http.StatusOK),
	}, nil, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (cl *apiClient) identity(ctx context.Context) (*identityResponse, error) {
	response := &identityResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:           http.MethodGet,
		path:             "/api/v2/auth/identity",
		expectedStatuses: statusSet(http.StatusOK),
	}, nil, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (cl *apiClient) getCollection(
	ctx context.Context,
	database databaseRef,
	name string,
) (*collectionResponse, error) {
	response := &collectionResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:           http.MethodGet,
		path:             collectionBasePath(database) + "/" + escapePath(name),
		expectedStatuses: statusSet(http.StatusOK),
	}, nil, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (cl *apiClient) createCollection(
	ctx context.Context,
	database databaseRef,
	request createCollectionRequest,
) (*collectionResponse, error) {
	response := &collectionResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:           http.MethodPost,
		path:             collectionBasePath(database),
		expectedStatuses: statusSet(http.StatusOK),
	}, request, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (cl *apiClient) addRecords(
	ctx context.Context,
	collection collectionRef,
	request addRecordsRequest,
) error {
	return cl.doJSON(ctx, requestSpec{
		method:           http.MethodPost,
		path:             recordsPath(collection, "add"),
		expectedStatuses: statusSet(http.StatusCreated),
	}, request, nil)
}

func (cl *apiClient) updateRecords(
	ctx context.Context,
	collection collectionRef,
	request updateRecordsRequest,
) error {
	return cl.doJSON(ctx, requestSpec{
		method:           http.MethodPost,
		path:             recordsPath(collection, "update"),
		expectedStatuses: statusSet(http.StatusOK),
	}, request, nil)
}

func (cl *apiClient) getRecords(
	ctx context.Context,
	collection collectionRef,
	request getRecordsRequest,
) (*getRecordsResponse, error) {
	response := &getRecordsResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:           http.MethodPost,
		path:             recordsPath(collection, "get"),
		expectedStatuses: statusSet(http.StatusOK),
	}, request, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (cl *apiClient) queryRecords(
	ctx context.Context,
	collection collectionRef,
	request queryRecordsRequest,
) (*queryRecordsResponse, error) {
	response := &queryRecordsResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:           http.MethodPost,
		path:             recordsPath(collection, "query"),
		expectedStatuses: statusSet(http.StatusOK),
	}, request, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (cl *apiClient) deleteRecords(
	ctx context.Context,
	collection collectionRef,
	request deleteRecordsRequest,
) (*deleteRecordsResponse, error) {
	if len(request.IDs) == 0 && len(request.Where) == 0 {
		return nil, errors.New("delete records requires ids or where")
	}
	response := &deleteRecordsResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:           http.MethodPost,
		path:             recordsPath(collection, "delete"),
		expectedStatuses: statusSet(http.StatusOK),
	}, request, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func collectionBasePath(database databaseRef) string {
	return "/api/v2/tenants/" + escapePath(database.tenant) +
		"/databases/" + escapePath(database.database) + "/collections"
}

func recordsPath(collection collectionRef, operation string) string {
	return collectionBasePath(collection.databaseRef) + "/" +
		escapePath(collection.id) + "/" + operation
}
