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
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
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

func TestWebSocketConnectorDialCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := NewConnector().Connect(ctx, Config{
			URL: "ws" + strings.TrimPrefix(server.URL, "http"),
		})
		result <- err
	}()
	<-started
	assert.ErrorIs(t, <-result, context.DeadlineExceeded)
}

func TestWebSocketConnCanceledReceiveDoesNotWaitForEarlierReceive(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(websocket.Handler(func(*websocket.Conn) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	conn, err := NewConnector().Connect(context.Background(), Config{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"),
	})
	require.NoError(t, err)
	wrapped := conn.(*webSocketConn)
	firstResult := make(chan error, 1)
	go func() {
		_, receiveErr := conn.Receive(context.Background())
		firstResult <- receiveErr
	}()
	require.Eventually(t, func() bool {
		return len(wrapped.receiveGate) == 1
	}, time.Second, time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = conn.Receive(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case err := <-firstResult:
		t.Fatalf("first receive unexpectedly returned: %v", err)
	default:
	}
	require.NoError(t, conn.Close())
	<-firstResult
}

func TestWebSocketConnCanceledSendDoesNotWaitForAdmission(t *testing.T) {
	conn := &webSocketConn{sendGate: make(chan struct{}, 1)}
	conn.sendGate <- struct{}{}
	event, err := NewEvent("session.update", nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = conn.Send(ctx, event)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWebSocketConnReassemblesFragmentedEvent(t *testing.T) {
	server := newFragmentedEventServer(t, []frame{
		{opcode: websocket.TextFrame, payload: `{"type":"response.`},
		{opcode: websocket.ContinuationFrame, payload: `audio.delta","delta":`},
		{fin: true, opcode: websocket.ContinuationFrame, payload: `"AQID"}`},
	})
	defer server.Close()

	conn, err := NewConnector().Connect(context.Background(), Config{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"),
	})
	require.NoError(t, err)
	defer conn.Close()
	event, err := conn.Receive(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "response.audio.delta", event.Type())
	assert.JSONEq(t, `{"type":"response.audio.delta","delta":"AQID"}`, string(event.Bytes()))
}

func TestWebSocketConnLimitsReassembledEvent(t *testing.T) {
	server := newFragmentedEventServer(t, []frame{
		{opcode: websocket.TextFrame, payload: `{"type":"`},
		{fin: true, opcode: websocket.ContinuationFrame, payload: `response.done"}`},
	})
	defer server.Close()

	conn, err := NewConnector().Connect(context.Background(), Config{
		URL:             "ws" + strings.TrimPrefix(server.URL, "http"),
		MaxPayloadBytes: 16,
	})
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Receive(context.Background())
	assert.ErrorIs(t, err, websocket.ErrFrameTooLarge)
}

func TestConnectorConfigValidation(t *testing.T) {
	_, err := NewConnector().Connect(context.Background(), Config{})
	assert.ErrorContains(t, err, "URL is required")

	_, err = NewConnector().Connect(context.Background(), Config{URL: "https://example.com"})
	assert.ErrorContains(t, err, "unsupported websocket URL scheme")
}

type frame struct {
	fin     bool
	opcode  byte
	payload string
}

func newFragmentedEventServer(t *testing.T, frames []frame) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		require.True(t, ok)
		conn, buf, err := hijacker.Hijack()
		require.NoError(t, err)
		defer conn.Close()

		sum := sha1.Sum([]byte(
			r.Header.Get("Sec-WebSocket-Key") +
				"258EAFA5-E914-47DA-95CA-C5AB0DC85B11",
		))
		_, err = fmt.Fprintf(
			buf,
			"HTTP/1.1 101 Switching Protocols\r\n"+
				"Upgrade: websocket\r\n"+
				"Connection: Upgrade\r\n"+
				"Sec-WebSocket-Accept: %s\r\n\r\n",
			base64.StdEncoding.EncodeToString(sum[:]),
		)
		require.NoError(t, err)
		require.NoError(t, buf.Flush())
		for _, frame := range frames {
			require.NoError(t, writeFrame(buf, frame))
		}
		require.NoError(t, buf.Flush())
	}))
}

func writeFrame(w io.Writer, frame frame) error {
	header := frame.opcode
	if frame.fin {
		header |= 0x80
	}
	payload := []byte(frame.payload)
	if len(payload) > 125 {
		return fmt.Errorf("test frame payload is too large: %d", len(payload))
	}
	if _, err := w.Write([]byte{header, byte(len(payload))}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
