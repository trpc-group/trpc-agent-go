//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package controlledegress

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
)

// DefaultRelayPort is the guest loopback port used for HTTP_PROXY injection.
const DefaultRelayPort = 17923

const defaultRelayMaxConnections = 128

// Exit codes for the egress-relay helper process.
const (
	// ExitUsageError is returned for invalid helper arguments.
	ExitUsageError = 2
	// ExitSetupFailed is returned when the loopback→UDS relay cannot start.
	ExitSetupFailed = 75
	// SetupErrorPrefix begins authenticated failures emitted before the user
	// command starts. The relay appends a host-generated token.
	SetupErrorPrefix = "egress-relay: setup:"
	// RunErrorPrefix marks failures emitted while starting the user command.
	RunErrorPrefix = "egress-relay: run:"
)

// Relay bridges TCP connections on 127.0.0.1:ListenPort to a Unix socket.
// This is the in-sandbox equivalent of Claude Code's socat hop.
type Relay struct {
	ListenHost string
	ListenPort int
	UnixPath   string
	dial       func(context.Context) (net.Conn, error)

	ln net.Listener
	wg sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	slots  chan struct{}
	closed bool
}

// StartRelay listens on 127.0.0.1:port and forwards each connection to unixPath.
// Controlled egress starts this trusted relay before applying seccomp to the
// user workload.
func StartRelay(listenPort int, unixPath string) (*Relay, error) {
	dialer := &net.Dialer{}
	return startRelayWithDialer(listenPort, unixPath, func(ctx context.Context) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", unixPath)
	})
}

func startRelayWithDialer(
	listenPort int,
	name string,
	dial func(context.Context) (net.Conn, error),
) (*Relay, error) {
	if listenPort <= 0 {
		listenPort = DefaultRelayPort
	}
	if dial == nil {
		return nil, fmt.Errorf("controlled egress relay missing dialer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Relay{
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		UnixPath:   name,
		dial:       dial,
		ctx:        ctx,
		cancel:     cancel,
		conns:      make(map[net.Conn]struct{}),
		slots:      make(chan struct{}, defaultRelayMaxConnections),
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", r.ListenHost, r.ListenPort))
	if err != nil {
		cancel()
		return nil, err
	}
	r.ln = ln
	go r.serve()
	return r, nil
}

// Addr returns the HTTP proxy URL host:port (without scheme).
func (r *Relay) Addr() string {
	return fmt.Sprintf("%s:%d", r.ListenHost, r.ListenPort)
}

// ProxyURL returns http://127.0.0.1:port for HTTP_PROXY injection.
func (r *Relay) ProxyURL() string {
	return "http://" + r.Addr()
}

// Close stops the relay.
func (r *Relay) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	ln := r.ln
	conns := make([]net.Conn, 0, len(r.conns))
	for conn := range r.conns {
		conns = append(conns, conn)
	}
	r.mu.Unlock()

	var closeErr error
	if ln != nil {
		closeErr = ln.Close()
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	r.wg.Wait()
	return closeErr
}

func (r *Relay) serve() {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			r.mu.Lock()
			closed := r.closed
			r.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		select {
		case r.slots <- struct{}{}:
		default:
			_ = c.Close()
			continue
		}
		r.mu.Lock()
		if r.closed {
			r.mu.Unlock()
			<-r.slots
			_ = c.Close()
			return
		}
		r.conns[c] = struct{}{}
		r.wg.Add(1)
		r.mu.Unlock()
		go func(conn net.Conn) {
			defer r.wg.Done()
			defer func() { <-r.slots }()
			r.handle(conn)
		}(c)
	}
}

func (r *Relay) handle(client net.Conn) {
	defer r.releaseConn(client)
	upstream, err := r.dial(r.ctx)
	if err != nil {
		return
	}
	if !r.trackConn(upstream) {
		return
	}
	defer r.releaseConn(upstream)
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		type closeWriter interface{ CloseWrite() error }
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite()
			return
		}
		_ = dst.Close()
	}
	go pipe(upstream, client)
	go pipe(client, upstream)
	wg.Wait()
}

func (r *Relay) trackConn(conn net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		_ = conn.Close()
		return false
	}
	r.conns[conn] = struct{}{}
	return true
}

func (r *Relay) releaseConn(conn net.Conn) {
	_ = conn.Close()
	r.mu.Lock()
	delete(r.conns, conn)
	r.mu.Unlock()
}
