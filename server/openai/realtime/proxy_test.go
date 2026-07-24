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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	modelrealtime "trpc.group/trpc-go/trpc-agent-go/model/openai/realtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

func TestProxyForwardsEventsBidirectionally(t *testing.T) {
	upstream := newFakeConn()
	connector := &fakeConnector{conn: upstream}
	proxy, err := New(
		WithUpstream(modelrealtime.Config{
			URL:    "wss://api.openai.example/v1/realtime?model=test",
			APIKey: "secret",
			Header: http.Header{"X-Upstream": []string{"value"}},
		}),
		WithConnector(connector),
	)
	require.NoError(t, err)

	server := httptest.NewServer(proxy.Handler())
	defer server.Close()
	downstream, err := websocket.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+proxy.Path(),
		"",
		"http://localhost",
	)
	require.NoError(t, err)

	require.NoError(t, websocket.Message.Send(
		downstream,
		`{"type":"input_audio_buffer.append","audio":"AAEC"}`,
	))
	select {
	case event := <-upstream.sent:
		assert.Equal(t, "input_audio_buffer.append", event.Type())
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream event")
	}

	serverEvent, err := modelrealtime.ParseEvent(
		[]byte(`{"type":"response.audio.delta","delta":"BAUG"}`),
	)
	require.NoError(t, err)
	upstream.received <- serverEvent

	var payload string
	require.NoError(t, websocket.Message.Receive(downstream, &payload))
	assert.JSONEq(t, `{"type":"response.audio.delta","delta":"BAUG"}`, payload)

	connector.mu.Lock()
	gotConfig := connector.config
	connector.mu.Unlock()
	assert.Equal(t, "secret", gotConfig.APIKey)
	assert.Equal(t, "value", gotConfig.Header.Get("X-Upstream"))

	require.NoError(t, downstream.Close())
	select {
	case <-upstream.closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for upstream cleanup")
	}
}

func TestProxyReportsUpstreamConnectionError(t *testing.T) {
	proxy, err := New(
		WithUpstream(modelrealtime.Config{URL: "wss://api.openai.example/v1/realtime"}),
		WithConnector(&fakeConnector{err: errors.New("dial failed")}),
	)
	require.NoError(t, err)

	server := httptest.NewServer(proxy.Handler())
	defer server.Close()
	conn, err := websocket.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http")+proxy.Path(),
		"",
		"http://localhost",
	)
	require.NoError(t, err)
	defer conn.Close()

	var payload string
	require.NoError(t, websocket.Message.Receive(conn, &payload))
	assert.JSONEq(t, `{
		"type":"error",
		"error":{
			"type":"connection_error",
			"message":"upstream connection failed"
		}
	}`, payload)
}

func TestProxyRejectsNonWebSocketMethod(t *testing.T) {
	proxy, err := New(
		WithUpstream(modelrealtime.Config{URL: "wss://api.openai.example/v1/realtime"}),
		WithConnector(&fakeConnector{conn: newFakeConn()}),
	)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, proxy.Path(), nil)
	recorder := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Equal(t, http.MethodGet, recorder.Header().Get("Allow"))
}

func TestProxyValidation(t *testing.T) {
	_, err := New()
	assert.ErrorContains(t, err, "upstream URL is required")

	_, err = New(
		WithPath("relative"),
		WithUpstream(modelrealtime.Config{URL: "wss://example.com"}),
	)
	assert.ErrorContains(t, err, "path must start with")

	_, err = New(
		WithUpstream(modelrealtime.Config{URL: "wss://example.com"}),
		WithConnector(nil),
	)
	assert.ErrorContains(t, err, "connector is required")
}

type fakeConnector struct {
	mu     sync.Mutex
	config modelrealtime.Config
	conn   modelrealtime.Conn
	err    error
}

func (c *fakeConnector) Connect(
	_ context.Context,
	config modelrealtime.Config,
) (modelrealtime.Conn, error) {
	c.mu.Lock()
	c.config = config
	c.mu.Unlock()
	return c.conn, c.err
}

type fakeConn struct {
	received chan modelrealtime.Event
	sent     chan modelrealtime.Event
	closed   chan struct{}
	once     sync.Once
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		received: make(chan modelrealtime.Event, 1),
		sent:     make(chan modelrealtime.Event, 1),
		closed:   make(chan struct{}),
	}
}

func (c *fakeConn) Send(
	ctx context.Context,
	event modelrealtime.Event,
) error {
	select {
	case c.sent <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return errors.New("closed")
	}
}

func (c *fakeConn) Receive(ctx context.Context) (modelrealtime.Event, error) {
	select {
	case event := <-c.received:
		return event, nil
	case <-ctx.Done():
		return modelrealtime.Event{}, ctx.Err()
	case <-c.closed:
		return modelrealtime.Event{}, errors.New("closed")
	}
}

func (c *fakeConn) Close() error {
	c.once.Do(func() {
		close(c.closed)
	})
	return nil
}
