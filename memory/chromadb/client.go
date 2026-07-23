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
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	maxHTTPAttempts     = 3
	maxHTTPRedirects    = 10
	maxErrorBodyPreview = 512
	initialBackoff      = 100 * time.Millisecond
)

type apiClient struct {
	baseURL        string
	httpClient     *http.Client
	headers        http.Header
	timeout        time.Duration
	ownedTransport *http.Transport
	secretValues   []string
}

type apiError struct {
	statusCode int
	code       string
	message    string
	traceID    string
	retryAfter string
}

// Error formats a redacted Chroma API failure for callers.
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

type transportError struct {
	err error
}

// Error exposes the underlying request transport failure.
func (e *transportError) Error() string {
	return e.err.Error()
}

// Unwrap allows retry classification to inspect the underlying network error.
func (e *transportError) Unwrap() error {
	return e.err
}

// newAPIClient builds a REST client without taking ownership of injected transports.
func newAPIClient(opts serviceOpts) *apiClient {
	httpClient, ownedTransport := copyHTTPClient(opts.httpClient)
	httpClient.CheckRedirect = checkSecureRedirect

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
		baseURL:        opts.baseURL,
		httpClient:     httpClient,
		headers:        headers,
		timeout:        opts.timeout,
		ownedTransport: ownedTransport,
		secretValues:   collectSecretValues(opts, headers),
	}
}

// copyHTTPClient shallow-copies caller state and returns only an adapter-owned transport.
func copyHTTPClient(client *http.Client) (*http.Client, *http.Transport) {
	if client != nil {
		copied := *client
		return &copied, nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: transport}, transport
}

// collectSecretValues gathers configured credentials for response redaction.
func collectSecretValues(opts serviceOpts, headers http.Header) []string {
	unique := make(map[string]struct{})
	if opts.apiKey != "" {
		unique[opts.apiKey] = struct{}{}
	}
	if opts.bearer != "" {
		unique[opts.bearer] = struct{}{}
	}
	for _, values := range headers {
		for _, value := range values {
			if value != "" {
				unique[value] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		return len(result[i]) > len(result[j])
	})
	return result
}

// checkSecureRedirect permits only same-origin HTTPS redirects within the hop limit.
func checkSecureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxHTTPRedirects {
		return errors.New("stopped after 10 redirects")
	}
	if len(via) == 0 {
		return errors.New("redirect has no original request")
	}
	source := via[0].URL
	target := req.URL
	if !strings.EqualFold(source.Scheme, "https") ||
		!strings.EqualFold(target.Scheme, "https") ||
		!sameOrigin(source, target) {
		return errors.New("chromadb redirect must remain on the same https origin")
	}
	return nil
}

// sameOrigin compares scheme, hostname, and effective port.
func sameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

// effectivePort returns an explicit port or the standard port for the URL scheme.
func effectivePort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	switch strings.ToLower(value.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

// closeIdleConnections closes only connections owned by the adapter-created transport.
func (cl *apiClient) closeIdleConnections() {
	if cl.ownedTransport != nil {
		cl.ownedTransport.CloseIdleConnections()
	}
}

// doJSON encodes one request body before entering the retryable request path.
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

// do executes one logical REST call under a single timeout and retry budget.
func (cl *apiClient) do(ctx context.Context, spec requestSpec, out any) error {
	requestCtx, cancel := context.WithTimeout(ctx, cl.timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt < maxHTTPAttempts; attempt++ {
		err := cl.doAttempt(requestCtx, spec, out)
		if err == nil {
			return nil
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

// doAttempt owns one HTTP response lifecycle and classifies its terminal result.
func (cl *apiClient) doAttempt(
	ctx context.Context,
	spec requestSpec,
	out any,
) (resultErr error) {
	requestURL := cl.baseURL + "/" + strings.TrimLeft(spec.path, "/")
	req, err := http.NewRequestWithContext(ctx, spec.method, requestURL, bytes.NewReader(spec.body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	copyRequestHeaders(req.Header, cl.headers)
	req.Header.Set("Accept", "application/json")
	if len(spec.body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return &transportError{err: fmt.Errorf("send request: %w", err)}
	}
	defer func() {
		if err := resp.Body.Close(); err != nil && resultErr == nil {
			resultErr = fmt.Errorf("close response: %w", err)
		}
	}()

	if resp.StatusCode != spec.expectedStatus {
		return cl.decodeAPIError(resp)
	}
	if out == nil {
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return &transportError{err: fmt.Errorf("read response: %w", err)}
		}
		return nil
	}
	return decodeJSONResponse(resp.Body, out)
}

// copyRequestHeaders copies configured values into a newly constructed request.
func copyRequestHeaders(target, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			target.Add(name, value)
		}
	}
}

// decodeJSONResponse decodes one successful JSON value without numeric precision loss.
func decodeJSONResponse(body io.Reader, out any) error {
	decoder := json.NewDecoder(body)
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return ensureJSONEnd(decoder)
}

// ensureJSONEnd rejects a second JSON value or malformed trailing content.
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

// decodeAPIError reads a bounded response preview and redacts configured secrets.
func (cl *apiClient) decodeAPIError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyPreview+1))
	if err != nil {
		return &transportError{err: fmt.Errorf("read error response: %w", err)}
	}
	truncated := len(body) > maxErrorBodyPreview
	if truncated {
		body = body[:maxErrorBodyPreview]
	}
	payload := struct {
		Code    string `json:"error"`
		Message string `json:"message"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		payload.Message = strings.TrimSpace(string(body))
	}
	if truncated {
		payload.Message += "…"
	}
	return &apiError{
		statusCode: resp.StatusCode,
		code:       cl.redact(payload.Code),
		message:    cl.redact(payload.Message),
		traceID:    cl.redact(resp.Header.Get("chroma-trace-id")),
		retryAfter: resp.Header.Get("Retry-After"),
	}
}

// redact replaces every configured sensitive value before it reaches an error.
func (cl *apiClient) redact(value string) string {
	for _, secret := range cl.secretValues {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	return value
}

// shouldRetry limits retries to temporary transport failures and selected statuses.
func shouldRetry(err error, attempt int) bool {
	if attempt >= maxHTTPAttempts-1 {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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
	var transportErr *transportError
	return errors.As(err, &transportErr) && isTransientNetworkError(transportErr.err)
}

// isTransientNetworkError recognizes temporary or timeout-like network failures.
func isTransientNetworkError(err error) bool {
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ETIMEDOUT)
}

// waitBeforeRetry honors Retry-After or applies exponential full-jitter backoff.
func waitBeforeRetry(ctx context.Context, err error, attempt int) error {
	delay := fullJitter(initialBackoff << attempt)
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		if retryAfter := parseRetryAfter(apiErr.retryAfter, time.Now()); retryAfter > 0 {
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

// fullJitter chooses a cryptographically random delay in the range [0, maxDelay).
func fullJitter(maxDelay time.Duration) time.Duration {
	if maxDelay <= 0 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(maxDelay)))
	if err != nil {
		return maxDelay / 2
	}
	return time.Duration(value.Int64())
}

// parseRetryAfter parses either delta-seconds or an HTTP-date retry instruction.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.Atoi(value)
	if err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

// isStatus reports whether err contains a Chroma response with statusCode.
func isStatus(err error, statusCode int) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.statusCode == statusCode
}

// preflightChecks reads server limits needed by write and clear operations.
func (cl *apiClient) preflightChecks(ctx context.Context) (*preflightChecksResponse, error) {
	response := &preflightChecksResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:         http.MethodGet,
		path:           "/api/v2/pre-flight-checks",
		expectedStatus: http.StatusOK,
	}, nil, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// identity retrieves tenant and database scope for authenticated inference.
func (cl *apiClient) identity(ctx context.Context) (*identityResponse, error) {
	response := &identityResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:         http.MethodGet,
		path:           "/api/v2/auth/identity",
		expectedStatus: http.StatusOK,
	}, nil, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// getCollection resolves a collection by name within one database scope.
func (cl *apiClient) getCollection(
	ctx context.Context,
	database databaseRef,
	name string,
) (*collectionResponse, error) {
	response := &collectionResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:         http.MethodGet,
		path:           collectionBasePath(database) + "/" + url.PathEscape(name),
		expectedStatus: http.StatusOK,
	}, nil, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// createCollection issues a get-or-create request with the adapter's collection schema.
func (cl *apiClient) createCollection(
	ctx context.Context,
	database databaseRef,
	request createCollectionRequest,
) (*collectionResponse, error) {
	response := &collectionResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:         http.MethodPost,
		path:           collectionBasePath(database),
		expectedStatus: http.StatusOK,
	}, request, response)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// addRecords performs a create-only batch write.
func (cl *apiClient) addRecords(
	ctx context.Context,
	collection collectionRef,
	request addRecordsRequest,
) error {
	return cl.doJSON(ctx, requestSpec{
		method:         http.MethodPost,
		path:           recordsPath(collection, "add"),
		expectedStatus: http.StatusCreated,
	}, request, nil)
}

// updateRecords changes existing record fields without upsert semantics.
func (cl *apiClient) updateRecords(
	ctx context.Context,
	collection collectionRef,
	request updateRecordsRequest,
) error {
	return cl.doJSON(ctx, requestSpec{
		method:         http.MethodPost,
		path:           recordsPath(collection, "update"),
		expectedStatus: http.StatusOK,
	}, request, nil)
}

// getRecords fetches records and validates the returned columnar envelope.
func (cl *apiClient) getRecords(
	ctx context.Context,
	collection collectionRef,
	request getRecordsRequest,
) (*getRecordsResponse, error) {
	response := &getRecordsResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:         http.MethodPost,
		path:           recordsPath(collection, "get"),
		expectedStatus: http.StatusOK,
	}, request, response)
	if err != nil {
		return nil, err
	}
	if err := validateGetEnvelope(request, response); err != nil {
		return nil, err
	}
	return response, nil
}

// validateGetEnvelope ensures requested columns are present and have matching lengths.
func validateGetEnvelope(request getRecordsRequest, response *getRecordsResponse) error {
	if !response.IDs.present || response.IDs.null {
		return errors.New("get records response is missing ids")
	}
	if !response.Include.present || response.Include.null {
		return errors.New("get records response is missing include")
	}
	expected := []string(nil)
	if request.Include != nil {
		expected = *request.Include
	}
	if !slices.Equal(response.Include.value, expected) {
		return fmt.Errorf(
			"get records include mismatch: expected %v, got %v",
			expected,
			response.Include.value,
		)
	}
	return nil
}

// queryRecords performs dense vector retrieval and validates the query envelope.
func (cl *apiClient) queryRecords(
	ctx context.Context,
	collection collectionRef,
	request queryRecordsRequest,
) (*queryRecordsResponse, error) {
	response := &queryRecordsResponse{}
	err := cl.doJSON(ctx, requestSpec{
		method:         http.MethodPost,
		path:           recordsPath(collection, "query"),
		expectedStatus: http.StatusOK,
	}, request, response)
	if err != nil {
		return nil, err
	}
	if err := validateQueryEnvelope(request, response); err != nil {
		return nil, err
	}
	return response, nil
}

// validateQueryEnvelope checks batch dimensions and every requested result column.
func validateQueryEnvelope(request queryRecordsRequest, response *queryRecordsResponse) error {
	if !response.IDs.present || response.IDs.null {
		return errors.New("query records response is missing ids")
	}
	if !response.Include.present || response.Include.null {
		return errors.New("query records response is missing include")
	}
	if !slices.Equal(response.Include.value, request.Include) {
		return fmt.Errorf(
			"query records include mismatch: expected %v, got %v",
			request.Include,
			response.Include.value,
		)
	}
	return nil
}

// deleteRecords requires a selector and validates the server's deletion count.
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
		method:         http.MethodPost,
		path:           recordsPath(collection, "delete"),
		expectedStatus: http.StatusOK,
	}, request, response)
	if err != nil {
		return nil, err
	}
	if err := validateDeleteResponse(request, response); err != nil {
		return nil, err
	}
	return response, nil
}

// validateDeleteResponse rejects missing or impossible deletion counts.
func validateDeleteResponse(request deleteRecordsRequest, response *deleteRecordsResponse) error {
	if !response.Deleted.present || response.Deleted.null {
		return errors.New("delete records response is missing deleted")
	}
	if response.Deleted.value < 0 {
		return fmt.Errorf("delete records returned a negative count: %d", response.Deleted.value)
	}
	maxDeleted := len(request.IDs)
	if maxDeleted == 0 && request.Limit != nil {
		maxDeleted = *request.Limit
	}
	if maxDeleted > 0 && response.Deleted.value > maxDeleted {
		return fmt.Errorf(
			"delete records returned %d deletions for %d targets",
			response.Deleted.value,
			maxDeleted,
		)
	}
	return nil
}

// collectionBasePath returns the REST collection endpoint for one tenant and database.
func collectionBasePath(database databaseRef) string {
	return "/api/v2/tenants/" + url.PathEscape(database.tenant) +
		"/databases/" + url.PathEscape(database.database) + "/collections"
}

// recordsPath returns a collection record-operation endpoint.
func recordsPath(collection collectionRef, operation string) string {
	return collectionBasePath(collection.databaseRef) + "/" +
		url.PathEscape(collection.id) + "/" + operation
}
