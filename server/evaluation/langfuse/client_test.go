//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestClientGetDatasetEscapesDatasetName(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestedPath = request.URL.EscapedPath()
		writeJSON(writer, request, http.StatusOK, &dataset{ID: "dataset-1", Name: "demo/dataset"})
	}))
	defer server.Close()
	client := newClient(server.URL, "pk", "sk", server.Client())
	dataset, err := client.getDataset(context.Background(), "demo/dataset")
	require.NoError(t, err)
	require.NotNil(t, dataset)
	assert.Equal(t, "/api/public/datasets/demo%2Fdataset", requestedPath)
	assert.Equal(t, "dataset-1", dataset.ID)
}

func TestClientDoJSONReturnsStructuredAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, request, http.StatusBadRequest, errorResponse{Message: "bad request"})
	}))
	defer server.Close()
	client := newClient(server.URL, "pk", "sk", server.Client())
	err := client.createTrace(context.Background(), traceCreateRequest{ID: "trace-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad request")
}

func TestClientGetDatasetReturnsAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, request, http.StatusNotFound, errorResponse{Message: "dataset not found"})
	}))
	defer server.Close()
	client := newClient(server.URL, "pk", "sk", server.Client())
	_, err := client.getDataset(context.Background(), "missing-dataset")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dataset not found")
}

func TestClientDoJSONReturnsPlainTextAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "plain failure", http.StatusBadGateway)
	}))
	defer server.Close()
	client := newClient(server.URL, "pk", "sk", server.Client())
	err := client.createScore(context.Background(), scoreCreateRequest{Name: "score"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plain failure")
}

func TestClientDoJSONReturnsDecodeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, err := writer.Write([]byte("{"))
		require.NoError(t, err)
	}))
	defer server.Close()
	client := newClient(server.URL, "pk", "sk", server.Client())
	_, err := client.createDatasetRunItem(context.Background(), datasetRunItemCreateRequest{
		RunName:       "nightly-run",
		DatasetItemID: "item-1",
		TraceID:       "trace-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode response")
}

func TestClientDoJSONReturnsMarshalErrors(t *testing.T) {
	client := newClient("http://example.com", "pk", "sk", http.DefaultClient)
	err := client.doJSON(context.Background(), http.MethodPost, "/api/public/traces", map[string]any{
		"unsupported": make(chan int),
	}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal request body")
}

func TestWithRunOptionsAppendsOptions(t *testing.T) {
	runOptionA := agentRunOptionStub("a")
	runOptionB := agentRunOptionStub("b")
	opts := newOptions(
		WithRunOptions(runOptionA),
		WithRunOptions(runOptionB),
	)
	require.Len(t, opts.runOptions, 2)
}

type failingResponseWriter struct {
	header   http.Header
	writeErr error
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingResponseWriter) WriteHeader(statusCode int) {}

func (w *failingResponseWriter) Write(data []byte) (int, error) {
	if w.writeErr == nil {
		return len(data), nil
	}
	return 0, w.writeErr
}

func TestWriteJSONHandlesEncodeAndWriteFailures(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/langfuse/remote-experiment", nil)
	writer := &failingResponseWriter{writeErr: errors.New("write failed")}
	writeJSON(writer, request, http.StatusOK, map[string]any{"unsupported": make(chan int)})
	writeJSON(writer, request, http.StatusOK, map[string]string{"status": "ok"})
}

func agentRunOptionStub(label string) func(*agent.RunOptions) {
	_ = label
	return func(config *agent.RunOptions) {}
}
