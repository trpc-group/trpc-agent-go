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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTripFunc adapts a function into an HTTP transport for client tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type trackingBody struct {
	reader io.Reader
	closed bool
}

func (b *trackingBody) Read(value []byte) (int, error) {
	return b.reader.Read(value)
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestClientRetriesTransientStatusAndClosesBodies(t *testing.T) {
	var calls int
	var bodies [][]byte
	var responseBodies []*trackingBody
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		bodies = append(bodies, body)
		status := http.StatusServiceUnavailable
		responseBody := []byte(`{"error":"unavailable"}`)
		if calls == 2 {
			status = http.StatusCreated
			responseBody = []byte(`{}`)
		}
		tracked := &trackingBody{reader: bytes.NewReader(responseBody)}
		responseBodies = append(responseBodies, tracked)
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       tracked,
		}, nil
	})
	client := &apiClient{
		baseURL:    "http://chroma.test",
		httpClient: &http.Client{Transport: transport},
		headers:    make(http.Header),
		timeout:    time.Second,
	}

	err := client.doJSON(context.Background(), requestSpec{
		method:         http.MethodPost,
		path:           "/add",
		expectedStatus: http.StatusCreated,
	}, map[string]any{"ids": []string{"id"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	require.Len(t, bodies, 2)
	assert.Equal(t, bodies[0], bodies[1])
	for _, body := range responseBodies {
		assert.True(t, body.closed)
	}
}

func TestClientSendsAuthenticationAndCustomHeaders(t *testing.T) {
	var authorization, apiKey, custom string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		apiKey = request.Header.Get("X-Chroma-Token")
		custom = request.Header.Get("X-Custom")
		writeTestJSON(writer, http.StatusOK, map[string]any{"max_batch_size": 10})
	}))
	defer server.Close()
	opts := defaultServiceOpts()
	opts.baseURL = server.URL
	opts.bearer = "bearer-secret"
	opts.headers = map[string]string{"X-Custom": "custom-value"}
	client := newAPIClient(opts)

	_, err := client.preflightChecks(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer bearer-secret", authorization)
	assert.Empty(t, apiKey)
	assert.Equal(t, "custom-value", custom)
}

func TestClientAPIErrorIncludesTraceID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("chroma-trace-id", "trace-123")
		writeTestJSON(writer, http.StatusBadRequest, map[string]any{
			"error": "InvalidArgument", "message": "bad request",
		})
	}))
	defer server.Close()
	client := &apiClient{
		baseURL: server.URL, httpClient: server.Client(),
		headers: make(http.Header), timeout: time.Second,
	}

	err := client.do(context.Background(), requestSpec{
		method: http.MethodGet, path: "/error", expectedStatus: http.StatusOK,
	}, nil)

	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.statusCode)
	assert.Equal(t, "trace-123", apiErr.traceID)
	assert.Contains(t, err.Error(), "bad request")
	assert.Contains(t, err.Error(), "trace-123")
}

func TestClientRedactsSecretsFromErrorResponse(t *testing.T) {
	const (
		apiKey       = "api-key-secret"
		customSecret = "custom-header-secret"
	)
	body := apiKey + customSecret + strings.Repeat("x", maxErrorBodyPreview)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("chroma-trace-id", apiKey)
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(body))
	}))
	defer server.Close()
	opts := defaultServiceOpts()
	opts.baseURL = server.URL
	opts.apiKey = apiKey
	opts.headers = map[string]string{"X-Custom": customSecret}
	client := newAPIClient(opts)

	err := client.do(context.Background(), requestSpec{
		method: http.MethodGet, path: "/error", expectedStatus: http.StatusOK,
	}, nil)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), apiKey)
	assert.NotContains(t, err.Error(), customSecret)
	assert.Contains(t, err.Error(), "[redacted]")
	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.LessOrEqual(t, len(apiErr.message), maxErrorBodyPreview+len("…"))
}

func TestClientRejectsUnsafeRedirectBeforeSendingCredentials(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		targetCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Redirect(writer, &http.Request{}, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	opts := defaultServiceOpts()
	opts.baseURL = source.URL
	opts.bearer = "redirect-secret"
	opts.httpClient = source.Client()
	client := newAPIClient(opts)

	err := client.do(context.Background(), requestSpec{
		method: http.MethodGet, path: "/", expectedStatus: http.StatusOK,
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "same https origin")
	assert.Zero(t, targetCalls.Load())
}

func TestClientAllowsSameOriginHTTPSRedirect(t *testing.T) {
	var finalAuthorization string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/start" {
			http.Redirect(writer, request, "/final", http.StatusTemporaryRedirect)
			return
		}
		finalAuthorization = request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	opts := defaultServiceOpts()
	opts.baseURL = server.URL
	opts.bearer = "redirect-secret"
	opts.httpClient = server.Client()
	client := newAPIClient(opts)

	err := client.do(context.Background(), requestSpec{
		method: http.MethodGet, path: "/start", expectedStatus: http.StatusOK,
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "Bearer redirect-secret", finalAuthorization)
}

func TestClientRejectsHTTPRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/start" {
			http.Redirect(writer, request, "/target", http.StatusTemporaryRedirect)
			return
		}
		targetCalls.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := &apiClient{
		baseURL: server.URL,
		httpClient: &http.Client{
			Transport:     server.Client().Transport,
			CheckRedirect: checkSecureRedirect,
		},
		headers: make(http.Header),
		timeout: time.Second,
	}

	err := client.do(context.Background(), requestSpec{
		method: http.MethodGet, path: "/start", expectedStatus: http.StatusOK,
	}, nil)

	require.Error(t, err)
	assert.Zero(t, targetCalls.Load())
}

func TestSecureRedirectAllowsAtMostTenRedirects(t *testing.T) {
	target, err := http.NewRequest(http.MethodGet, "https://chroma.test/target", nil)
	require.NoError(t, err)
	original, err := http.NewRequest(http.MethodGet, "https://chroma.test/start", nil)
	require.NoError(t, err)
	via := make([]*http.Request, maxHTTPRedirects)
	for i := range via {
		via[i] = original
	}

	assert.NoError(t, checkSecureRedirect(target, via))
	assert.Error(t, checkSecureRedirect(target, append(via, original)))
}

func TestClientContextCancellationStopsRetry(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		once.Do(func() { close(started) })
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client := &apiClient{
		baseURL:    "http://chroma.test",
		httpClient: &http.Client{Transport: transport},
		headers:    make(http.Header),
		timeout:    time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	errorCh := make(chan error, 1)
	go func() {
		errorCh <- client.do(ctx, requestSpec{
			method: http.MethodGet, path: "/get", expectedStatus: http.StatusOK,
		}, nil)
	}()
	<-started
	cancel()

	err := <-errorCh
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClientDoesNotRetryDeterministicErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "bad request", status: http.StatusBadRequest},
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "internal server error", status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{
					StatusCode: tt.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":"failed"}`)),
				}, nil
			})
			client := &apiClient{
				baseURL: "http://chroma.test",
				httpClient: &http.Client{
					Transport: transport,
				},
				headers: make(http.Header),
				timeout: time.Second,
			}

			err := client.do(context.Background(), requestSpec{
				method: http.MethodGet, path: "/", expectedStatus: http.StatusOK,
			}, nil)

			require.Error(t, err)
			assert.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestShouldRetryTransientNetworkErrors(t *testing.T) {
	transient := &transportError{err: &net.DNSError{IsTimeout: true}}
	assert.True(t, shouldRetry(transient, 0))
	assert.False(t, shouldRetry(transient, maxHTTPAttempts-1))
	assert.True(t, shouldRetry(&transportError{err: syscall.ECONNRESET}, 0))
	assert.False(t, shouldRetry(&transportError{err: errors.New("permanent")}, 0))
	assert.False(t, shouldRetry(context.Canceled, 0))
}

func TestFullJitterStaysWithinBounds(t *testing.T) {
	for i := 0; i < 100; i++ {
		delay := fullJitter(100 * time.Millisecond)
		assert.GreaterOrEqual(t, delay, time.Duration(0))
		assert.Less(t, delay, 100*time.Millisecond)
	}
	assert.Zero(t, fullJitter(0))
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 2*time.Second, parseRetryAfter("2", now))
	assert.Equal(t, 3*time.Second, parseRetryAfter(now.Add(3*time.Second).Format(http.TimeFormat), now))
	assert.Zero(t, parseRetryAfter("invalid", now))
	assert.Zero(t, parseRetryAfter("0", now))
}

func TestWaitBeforeRetryHonorsRetryAfter(t *testing.T) {
	start := time.Now()

	err := waitBeforeRetry(context.Background(), &apiError{
		retryAfter: "1",
	}, 0)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), 950*time.Millisecond)
}

func TestClientStreamsJSONWithUseNumberAndRejectsTrailer(t *testing.T) {
	const nanoseconds = "1730500123456789012"
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "single value", body: `{"timestamp":` + nanoseconds + `}`},
		{name: "second value", body: `{"timestamp":1} {}`, wantErr: "multiple JSON values"},
		{name: "invalid trailer", body: `{"timestamp":1} x`, wantErr: "trailer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := &apiClient{
				baseURL: server.URL, httpClient: server.Client(),
				headers: make(http.Header), timeout: time.Second,
			}
			var response map[string]any

			err := client.do(context.Background(), requestSpec{
				method: http.MethodGet, path: "/", expectedStatus: http.StatusOK,
			}, &response)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			number, ok := response["timestamp"].(json.Number)
			require.True(t, ok)
			assert.Equal(t, nanoseconds, number.String())
		})
	}
}

func TestResponseEnvelopeValidation(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		include := []string{"documents"}
		request := getRecordsRequest{Include: &include}
		valid := &getRecordsResponse{
			IDs: responseField[[]string]{
				value: []string{}, present: true,
			},
			Include: responseField[[]string]{
				value: include, present: true,
			},
		}
		assert.NoError(t, validateGetEnvelope(request, valid))
		valid.Include.present = false
		assert.Error(t, validateGetEnvelope(request, valid))
	})
	t.Run("query", func(t *testing.T) {
		include := []string{"distances"}
		request := queryRecordsRequest{Include: include}
		valid := &queryRecordsResponse{
			IDs: responseField[[][]string]{
				value: [][]string{{}}, present: true,
			},
			Include: responseField[[]string]{
				value: include, present: true,
			},
		}
		assert.NoError(t, validateQueryEnvelope(request, valid))
		valid.Include.value = []string{"documents"}
		assert.Error(t, validateQueryEnvelope(request, valid))
	})
}

func TestDeleteResponseValidation(t *testing.T) {
	request := deleteRecordsRequest{IDs: []string{"one"}}
	tests := []struct {
		name     string
		response *deleteRecordsResponse
		wantErr  string
	}{
		{name: "zero", response: deleteResponse(0)},
		{name: "one", response: deleteResponse(1)},
		{name: "missing", response: &deleteRecordsResponse{}, wantErr: "missing deleted"},
		{
			name: "null",
			response: &deleteRecordsResponse{
				Deleted: responseField[int]{present: true, null: true},
			},
			wantErr: "missing deleted",
		},
		{name: "negative", response: deleteResponse(-1), wantErr: "negative"},
		{name: "too many", response: deleteResponse(2), wantErr: "2 deletions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDeleteResponse(request, tt.response)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestClientDeleteRequiresSelector(t *testing.T) {
	client := &apiClient{}

	response, err := client.deleteRecords(
		context.Background(),
		collectionRef{},
		deleteRecordsRequest{},
	)

	assert.Nil(t, response)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires")
}

func deleteResponse(value int) *deleteRecordsResponse {
	return &deleteRecordsResponse{
		Deleted: responseField[int]{value: value, present: true},
	}
}
