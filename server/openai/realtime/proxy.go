//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package realtime provides an OpenAI Realtime-compatible WebSocket proxy.
package realtime

import (
	"context"
	"errors"
	"net/http"
	"sync"

	modelrealtime "trpc.group/trpc-go/trpc-agent-go/model/openai/realtime"

	"golang.org/x/net/websocket"
)

const defaultPath = "/v1/realtime"

// Proxy forwards OpenAI Realtime events between clients and an upstream
// Realtime WebSocket endpoint. It does not authenticate downstream clients;
// applications are responsible for access control around Handler.
type Proxy struct {
	path      string
	config    modelrealtime.Config
	connector modelrealtime.Connector
	handler   http.Handler
	ctx       context.Context
	cancel    context.CancelFunc
	sessionMu sync.Mutex
	sessionWG sync.WaitGroup
	closed    bool
	closeOnce sync.Once
	done      chan struct{}
}

// Option configures a Proxy.
type Option func(*proxyOptions)

type proxyOptions struct {
	path      string
	config    modelrealtime.Config
	connector modelrealtime.Connector
}

// New creates a Realtime WebSocket proxy.
func New(opts ...Option) (*Proxy, error) {
	options := proxyOptions{
		path:      defaultPath,
		connector: modelrealtime.NewConnector(),
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.path == "" || options.path[0] != '/' {
		return nil, errors.New("openai realtime proxy: path must start with /")
	}
	if options.connector == nil {
		return nil, errors.New("openai realtime proxy: connector is required")
	}
	if options.config.URL == "" {
		return nil, errors.New("openai realtime proxy: upstream URL is required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	proxy := &Proxy{
		path:      options.path,
		config:    cloneConfig(options.config),
		connector: options.connector,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	proxy.setupHandler()
	return proxy, nil
}

// WithPath sets the downstream WebSocket endpoint path.
func WithPath(path string) Option {
	return func(options *proxyOptions) {
		options.path = path
	}
}

// WithUpstream configures the upstream Realtime connection.
func WithUpstream(config modelrealtime.Config) Option {
	return func(options *proxyOptions) {
		options.config = cloneConfig(config)
	}
}

// WithConnector replaces the upstream connector. Custom implementations must
// satisfy the concurrency and cancellation contracts on modelrealtime.Connector
// and modelrealtime.Conn.
func WithConnector(connector modelrealtime.Connector) Option {
	return func(options *proxyOptions) {
		options.connector = connector
	}
}

func cloneConfig(config modelrealtime.Config) modelrealtime.Config {
	config.Header = config.Header.Clone()
	return config
}

// Handler returns the proxy's HTTP handler.
//
// The handler performs no downstream authentication or authorization and
// relays accepted connections with the configured upstream credentials.
// Callers must add access-control middleware before exposing it publicly.
func (p *Proxy) Handler() http.Handler {
	return p.handler
}

// Path returns the downstream WebSocket endpoint path.
func (p *Proxy) Path() string {
	return p.path
}

// Close stops accepting proxy sessions, closes both peers of every active
// session, and waits for their relay goroutines to exit. Close is idempotent.
func (p *Proxy) Close() error {
	p.closeOnce.Do(func() {
		p.sessionMu.Lock()
		p.closed = true
		p.cancel()
		p.sessionMu.Unlock()
		p.sessionWG.Wait()
		close(p.done)
	})
	<-p.done
	return nil
}

func (p *Proxy) setupHandler() {
	websocketHandler := websocket.Server{
		Handler: websocket.Handler(p.handleWebSocket),
		Handshake: func(*websocket.Config, *http.Request) error {
			return nil
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(p.path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if p.isClosed() {
			http.Error(w, "proxy is closed", http.StatusServiceUnavailable)
			return
		}
		websocketHandler.ServeHTTP(w, r)
	})
	p.handler = mux
}

func (p *Proxy) handleWebSocket(downstreamWS *websocket.Conn) {
	ctx, cancel, finish, ok := p.beginSession(downstreamWS.Request().Context())
	if !ok {
		_ = downstreamWS.Close()
		return
	}
	defer finish()

	upstream, err := p.connector.Connect(ctx, cloneConfig(p.config))
	if err != nil || upstream == nil {
		p.sendConnectionError(ctx, downstreamWS)
		return
	}
	downstream := modelrealtime.NewWebSocketConn(
		downstreamWS,
		p.config.MaxPayloadBytes,
	)
	defer downstream.Close()
	defer upstream.Close()

	errCh := make(chan error, 2)
	var relayWG sync.WaitGroup
	relayWG.Add(2)
	go p.relay(ctx, &relayWG, errCh, downstream, upstream)
	go p.relay(ctx, &relayWG, errCh, upstream, downstream)

	<-errCh
	cancel()
	_ = downstream.Close()
	_ = upstream.Close()
	relayWG.Wait()
}

func (p *Proxy) isClosed() bool {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	return p.closed
}

func (p *Proxy) beginSession(
	parent context.Context,
) (context.Context, context.CancelFunc, func(), bool) {
	p.sessionMu.Lock()
	if p.closed {
		p.sessionMu.Unlock()
		return nil, nil, nil, false
	}
	p.sessionWG.Add(1)
	p.sessionMu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	stopOwnerCancellation := context.AfterFunc(p.ctx, cancel)
	return ctx, cancel, func() {
		stopOwnerCancellation()
		cancel()
		p.sessionWG.Done()
	}, true
}

func (*Proxy) relay(
	ctx context.Context,
	wg *sync.WaitGroup,
	errCh chan<- error,
	source modelrealtime.Conn,
	target modelrealtime.Conn,
) {
	defer wg.Done()
	for {
		event, err := source.Receive(ctx)
		if err != nil {
			errCh <- err
			return
		}
		if err := target.Send(ctx, event); err != nil {
			errCh <- err
			return
		}
	}
}

func (*Proxy) sendConnectionError(
	ctx context.Context,
	conn *websocket.Conn,
) {
	event, err := modelrealtime.NewEvent("error", map[string]any{
		"error": map[string]any{
			"type":    "connection_error",
			"message": "upstream connection failed",
		},
	})
	if err != nil {
		return
	}
	downstream := modelrealtime.NewWebSocketConn(conn, 0)
	_ = downstream.Send(ctx, event)
	_ = downstream.Close()
}
