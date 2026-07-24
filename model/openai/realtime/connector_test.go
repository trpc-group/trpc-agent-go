//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

func TestWebSocketConnector(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		requests <- conn.Request()
		var payload string
		require.NoError(t, websocket.Message.Receive(conn, &payload))
		require.NoError(t, websocket.Message.Send(conn, payload))
	}))
	defer server.Close()

	conn, err := NewConnector().Connect(context.Background(), Config{
		URL:    "ws" + strings.TrimPrefix(server.URL, "http"),
		APIKey: "test-key",
		Header: http.Header{"X-Test": []string{"value"}},
	})
	require.NoError(t, err)
	defer conn.Close()

	event, err := NewEvent("session.update", map[string]any{"session": map[string]any{"model": "test"}})
	require.NoError(t, err)
	require.NoError(t, conn.Send(context.Background(), event))
	got, err := conn.Receive(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "session.update", got.Type())
	assert.JSONEq(t, string(event.Bytes()), string(got.Bytes()))

	req := <-requests
	assert.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
	assert.Equal(t, "realtime=v1", req.Header.Get("OpenAI-Beta"))
	assert.Equal(t, "value", req.Header.Get("X-Test"))
}

func TestWebSocketConnReceiveCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	conn, err := NewConnector().Connect(context.Background(), Config{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"),
	})
	require.NoError(t, err)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = conn.Receive(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestConnectorConfigValidation(t *testing.T) {
	_, err := NewConnector().Connect(context.Background(), Config{})
	assert.ErrorContains(t, err, "URL is required")

	_, err = NewConnector().Connect(context.Background(), Config{URL: "https://example.com"})
	assert.ErrorContains(t, err, "unsupported websocket URL scheme")
}
