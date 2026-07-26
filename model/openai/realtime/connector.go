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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const (
	defaultOrigin          = "http://localhost"
	defaultMaxPayloadBytes = 16 << 20
)

// Config configures an OpenAI Realtime WebSocket connection.
type Config struct {
	// URL is the upstream Realtime WebSocket endpoint. It must use ws or wss.
	URL string
	// Origin is sent in the WebSocket handshake. It defaults to
	// http://localhost when empty.
	Origin string
	// APIKey is sent as a Bearer token unless Header already contains an
	// Authorization value.
	APIKey string
	// Header contains additional handshake headers. Connect clones it before
	// adding defaults and never mutates the caller's map.
	Header http.Header
	// MaxPayloadBytes limits complete received event payloads. Values less than
	// one use the 16 MiB default.
	MaxPayloadBytes int
}

// Connector opens Realtime WebSocket connections. Connect may be called
// concurrently and must honor ctx promptly. A successful call must return a
// non-nil Conn.
type Connector interface {
	Connect(ctx context.Context, config Config) (Conn, error)
}

// Conn sends and receives Realtime events. One Send and one Receive may run
// concurrently, and Close may run concurrently with both. Implementations must
// honor operation contexts promptly. Close must be idempotent and unblock
// in-flight Send and Receive calls.
type Conn interface {
	Send(ctx context.Context, event Event) error
	Receive(ctx context.Context) (Event, error)
	Close() error
}

// WebSocketConnector is the default Connector implementation. The underlying
// x/net WebSocket transport automatically replies to incoming ping frames with
// pong frames.
type WebSocketConnector struct{}

// NewConnector creates the default OpenAI Realtime connector.
func NewConnector() *WebSocketConnector {
	return &WebSocketConnector{}
}

// Connect opens an upstream WebSocket connection.
func (*WebSocketConnector) Connect(ctx context.Context, config Config) (Conn, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	origin := config.Origin
	if origin == "" {
		origin = defaultOrigin
	}
	wsConfig, err := websocket.NewConfig(config.URL, origin)
	if err != nil {
		return nil, fmt.Errorf("realtime: create websocket config: %w", err)
	}
	wsConfig.Header = config.Header.Clone()
	if wsConfig.Header == nil {
		wsConfig.Header = make(http.Header)
	}
	if config.APIKey != "" && wsConfig.Header.Get("Authorization") == "" {
		wsConfig.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	if wsConfig.Header.Get("OpenAI-Beta") == "" {
		wsConfig.Header.Set("OpenAI-Beta", "realtime=v1")
	}
	ws, err := wsConfig.DialContext(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("realtime: connect websocket: %w", err)
	}
	return NewWebSocketConn(ws, config.MaxPayloadBytes), nil
}

func validateConfig(config Config) error {
	if config.URL == "" {
		return errors.New("realtime: websocket URL is required")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil {
		return fmt.Errorf("realtime: parse websocket URL: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return fmt.Errorf("realtime: unsupported websocket URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("realtime: websocket URL host is required")
	}
	return nil
}

type webSocketConn struct {
	conn            *websocket.Conn
	reader          *bufio.Reader
	maxPayloadBytes int
	sendGate        chan struct{}
	receiveGate     chan struct{}
	closeOnce       sync.Once
	closeErr        error
}

// NewWebSocketConn adapts an x/net WebSocket connection to Conn.
func NewWebSocketConn(conn *websocket.Conn, maxPayloadBytes int) Conn {
	if maxPayloadBytes <= 0 {
		maxPayloadBytes = defaultMaxPayloadBytes
	}
	conn.MaxPayloadBytes = maxPayloadBytes
	return &webSocketConn{
		conn:            conn,
		reader:          bufio.NewReader(conn),
		maxPayloadBytes: maxPayloadBytes,
		sendGate:        make(chan struct{}, 1),
		receiveGate:     make(chan struct{}, 1),
	}
}

func (c *webSocketConn) Send(ctx context.Context, event Event) error {
	if err := acquire(ctx, c.sendGate); err != nil {
		return err
	}
	defer release(c.sendGate)

	raw, err := event.MarshalJSON()
	if err != nil {
		return err
	}
	stop := interruptOnCancel(ctx, c.conn.SetWriteDeadline)
	defer stop()
	if err := websocket.Message.Send(c.conn, string(raw)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("realtime: send event: %w", err)
	}
	return nil
}

func (c *webSocketConn) Receive(ctx context.Context) (Event, error) {
	if err := acquire(ctx, c.receiveGate); err != nil {
		return Event{}, err
	}
	defer release(c.receiveGate)

	stop := interruptOnCancel(ctx, c.conn.SetReadDeadline)
	defer stop()
	payload, err := readEventPayload(c.reader, c.maxPayloadBytes)
	if err != nil {
		if ctx.Err() != nil {
			return Event{}, ctx.Err()
		}
		return Event{}, fmt.Errorf("realtime: receive event: %w", err)
	}
	return ParseEvent(payload)
}

func acquire(ctx context.Context, gate chan struct{}) error {
	select {
	case gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(gate chan struct{}) {
	<-gate
}

func readEventPayload(reader *bufio.Reader, maxPayloadBytes int) ([]byte, error) {
	payload := make([]byte, 0, 512)
	depth := 0
	inString := false
	escaped := false
	started := false
	for {
		b, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && len(payload) != 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		payload = append(payload, b)
		if len(payload) > maxPayloadBytes {
			return nil, websocket.ErrFrameTooLarge
		}
		if !started {
			switch b {
			case ' ', '\t', '\r', '\n':
				continue
			case '{':
				started = true
				depth = 1
				continue
			default:
				return nil, errors.New("event payload must be a JSON object")
			}
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return payload, nil
			}
		}
	}
}

func interruptOnCancel(
	ctx context.Context,
	setDeadline func(time.Time) error,
) func() {
	finished := make(chan struct{})
	stopAfterFunc := context.AfterFunc(ctx, func() {
		_ = setDeadline(time.Now())
		close(finished)
	})
	return func() {
		if !stopAfterFunc() {
			<-finished
		}
		_ = setDeadline(time.Time{})
	}
}

func (c *webSocketConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close()
	})
	return c.closeErr
}
