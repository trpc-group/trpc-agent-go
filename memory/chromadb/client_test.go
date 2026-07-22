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
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestClient_RetriesTransientStatusAndClosesBodies(t *testing.T) {
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
		method:           http.MethodPost,
		path:             "/add",
		expectedStatuses: statusSet(http.StatusCreated),
	}, map[string]any{"ids": []string{"id"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	require.Len(t, bodies, 2)
	assert.Equal(t, bodies[0], bodies[1])
	for _, body := range responseBodies {
		assert.True(t, body.closed)
	}
}

func TestClient_SendsAuthenticationAndCustomHeaders(t *testing.T) {
	var authorization, apiKey, custom string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		apiKey = request.Header.Get("X-Chroma-Token")
		custom = request.Header.Get("X-Custom")
		writeTestJSON(writer, http.StatusOK, map[string]any{"max_batch_size": 10})
	}))
	defer server.Close()
	opts := defaultOptions.clone()
	opts.baseURL = server.URL
	opts.bearer = "bearer-secret"
	opts.headers = map[string]string{"X-Custom": "custom-value"}
	client, err := newAPIClient(opts)
	require.NoError(t, err)

	_, err = client.checklist(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer bearer-secret", authorization)
	assert.Empty(t, apiKey)
	assert.Equal(t, "custom-value", custom)
}

func TestClient_APIErrorIncludesTraceID(t *testing.T) {
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
		method: http.MethodGet, path: "/error", expectedStatuses: statusSet(http.StatusOK),
	}, nil)

	var apiErr *apiError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.statusCode)
	assert.Equal(t, "trace-123", apiErr.traceID)
	assert.Contains(t, err.Error(), "bad request")
	assert.Contains(t, err.Error(), "trace-123")
}

func TestClient_ContextCancellationStopsRetry(t *testing.T) {
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
			method: http.MethodGet, path: "/get", expectedStatuses: statusSet(http.StatusOK),
		}, nil)
	}()
	<-started
	cancel()

	err := <-errorCh
	assert.ErrorIs(t, err, context.Canceled)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, 2*time.Second, parseRetryAfter("2", now))
	assert.Equal(t, 3*time.Second, parseRetryAfter(now.Add(3*time.Second).Format(http.TimeFormat), now))
	assert.Zero(t, parseRetryAfter("invalid", now))
	assert.Zero(t, parseRetryAfter("0", now))
}

func TestClient_DeleteRequiresSelector(t *testing.T) {
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
