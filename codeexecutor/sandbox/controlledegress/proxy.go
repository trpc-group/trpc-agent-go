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
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultProxyMaxConnections  = 128
	defaultProxyMaxHeaderBytes  = 64 << 10
	defaultProxyHeaderTimeout   = 10 * time.Second
	defaultProxyUpstreamTimeout = 10 * time.Second
)

// Proxy is a host-side HTTP forward/CONNECT proxy listening on a Unix socket.
type Proxy struct {
	UnixPath string

	policy          Policy
	testDialContext func(ctx context.Context, network, address string) (net.Conn, error)
	identity        RunIdentity
	ln              net.Listener
	socketInfo      os.FileInfo
	ctx             context.Context
	cancel          context.CancelFunc
	slots           chan struct{}
	wg              sync.WaitGroup

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

type proxyConfig struct {
	identity RunIdentity
}

// ProxyOption configures trusted host-side proxy behavior.
type ProxyOption func(*proxyConfig)

// WithRunIdentity attaches trusted host-side audit correlation metadata.
func WithRunIdentity(identity RunIdentity) ProxyOption {
	return func(config *proxyConfig) {
		config.identity = identity
	}
}

// StartProxy validates and snapshots policy, creates a caller-owned Unix socket
// with mode 0600, and starts serving until Close. Resolver, Authorizer, and
// Auditor implementations remain shared and must support concurrent calls.
func StartProxy(
	unixPath string,
	policy Policy,
	opts ...ProxyOption,
) (*Proxy, error) {
	config := proxyConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	return startProxy(unixPath, policy, nil, config.identity)
}

// StartTestProxy starts a proxy for tests. Its optional dial hook may redirect
// approved traffic to a local test server; production code must use StartProxy.
func StartTestProxy(
	unixPath string,
	policy Policy,
	dial ...func(context.Context, string, string) (net.Conn, error),
) (*Proxy, error) {
	var testDial func(context.Context, string, string) (net.Conn, error)
	if len(dial) > 0 {
		testDial = dial[0]
	}
	return startProxy(unixPath, policy, testDial, RunIdentity{})
}

func startProxy(
	unixPath string,
	policy Policy,
	testDial func(context.Context, string, string) (net.Conn, error),
	identity RunIdentity,
) (*Proxy, error) {
	policy = clonePolicy(policy)
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := prepareUnixSocketPath(unixPath); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", unixPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(unixPath, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	socketInfo, err := os.Lstat(unixPath)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Proxy{
		UnixPath:        unixPath,
		policy:          policy,
		testDialContext: testDial,
		identity:        identity,
		ln:              ln,
		socketInfo:      socketInfo,
		ctx:             ctx,
		cancel:          cancel,
		slots:           make(chan struct{}, defaultProxyMaxConnections),
		conns:           make(map[net.Conn]struct{}),
	}
	go p.serve()
	return p, nil
}

func clonePolicy(policy Policy) Policy {
	cloned := policy
	cloned.AllowedHosts = append([]string(nil), policy.AllowedHosts...)
	cloned.AllowedPorts = append([]int(nil), policy.AllowedPorts...)
	return cloned
}

func prepareUnixSocketPath(unixPath string) error {
	if unixPath == "" {
		return fmt.Errorf("unix socket path is empty")
	}
	st, err := os.Lstat(unixPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %q", unixPath)
	}
	conn, dialErr := net.DialTimeout("unix", unixPath, 100*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("unix socket path %q is already in use", unixPath)
	}
	return os.Remove(unixPath)
}

// Close stops the proxy listener.
func (p *Proxy) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	ln := p.ln
	if p.cancel != nil {
		p.cancel()
	}
	for conn := range p.conns {
		_ = conn.Close()
	}
	p.mu.Unlock()

	var closeErr error
	if ln != nil {
		closeErr = ln.Close()
	}
	p.wg.Wait()
	if p.socketInfo != nil {
		if current, err := os.Lstat(p.UnixPath); err == nil &&
			os.SameFile(p.socketInfo, current) {
			_ = os.Remove(p.UnixPath)
		}
	}
	return closeErr
}

func (p *Proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		select {
		case p.slots <- struct{}{}:
		default:
			writeProxyError(c, http.StatusServiceUnavailable, "proxy connection limit reached")
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			<-p.slots
			_ = c.Close()
			return
		}
		p.conns[c] = struct{}{}
		p.wg.Add(1)
		p.mu.Unlock()
		go func() {
			defer p.wg.Done()
			defer func() { <-p.slots }()
			defer func() {
				p.mu.Lock()
				delete(p.conns, c)
				p.mu.Unlock()
			}()
			p.handle(c)
		}()
	}
}

func (p *Proxy) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(defaultProxyHeaderTimeout))
	headerReader := &proxyHeaderLimitReader{
		reader:    c,
		remaining: defaultProxyMaxHeaderBytes,
	}
	br := bufio.NewReader(headerReader)
	req, err := http.ReadRequest(br)
	if err != nil {
		if headerReader.exceeded {
			writeProxyError(c, http.StatusRequestHeaderFieldsTooLarge, "proxy request headers too large")
		}
		return
	}
	headerReader.unlimited = true
	_ = c.SetReadDeadline(time.Time{})
	ctx := withRunIdentity(p.ctx, p.identity)
	if req.Method == http.MethodConnect {
		p.handleCONNECT(ctx, c, br, req)
		return
	}
	p.handleHTTP(ctx, c, req)
}

type proxyHeaderLimitReader struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
	unlimited bool
}

func (r *proxyHeaderLimitReader) Read(p []byte) (int, error) {
	if r.unlimited {
		return r.reader.Read(p)
	}
	if r.remaining <= 0 {
		r.exceeded = true
		return 0, fmt.Errorf("proxy request headers exceed %d bytes", defaultProxyMaxHeaderBytes)
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func (p *Proxy) handleCONNECT(ctx context.Context, client net.Conn, br *bufio.Reader, req *http.Request) {
	target, err := ParseCONNECTTarget(req.Host)
	if err != nil {
		writeProxyError(client, http.StatusBadRequest, err.Error())
		return
	}
	dec := p.policy.Decide(ctx, target)
	if !dec.Allow {
		writeProxyError(client, http.StatusForbidden, dec.Reason)
		return
	}
	upstream, err := p.dialUpstream(ctx, dec, target)
	if err != nil {
		writeProxyError(client, http.StatusBadGateway, err.Error())
		return
	}
	defer upstream.Close()
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if net.ParseIP(target.Host) == nil {
		_ = client.SetReadDeadline(time.Now().Add(defaultProxyHeaderTimeout))
		sni, clientHello, err := readTLSClientHelloSNI(br)
		_ = client.SetReadDeadline(time.Time{})
		if err != nil || !strings.EqualFold(sni, target.Host) {
			reason := "TLS SNI does not match CONNECT target"
			if err != nil {
				reason = "invalid TLS ClientHello after CONNECT"
			}
			p.policy.audit(ctx, Decision{Reason: reason, Target: target})
			return
		}
		if _, err := upstream.Write(clientHello); err != nil {
			return
		}
	} else if buffered := br.Buffered(); buffered > 0 {
		peek, _ := br.Peek(buffered)
		if _, err := upstream.Write(peek); err != nil {
			return
		}
		_, _ = br.Discard(buffered)
	}
	relayBidirectional(client, upstream)
}

func (p *Proxy) handleHTTP(ctx context.Context, client net.Conn, req *http.Request) {
	rawURL := req.URL.String()
	if req.URL.Scheme == "" || req.URL.Host == "" {
		// Some clients send origin-form with Host header; treat as http.
		if req.Host == "" {
			writeProxyError(client, http.StatusBadRequest, "missing host")
			return
		}
		rawURL = "http://" + req.Host + req.URL.RequestURI()
	}
	target, err := ParseHTTPAbsoluteForm(rawURL)
	if err != nil {
		writeProxyError(client, http.StatusBadRequest, err.Error())
		return
	}
	if req.Host != "" {
		target.HostHeader = req.Host
	}
	dec := p.policy.Decide(ctx, target)
	if !dec.Allow {
		writeProxyError(client, http.StatusForbidden, dec.Reason)
		return
	}
	upstream, err := p.dialUpstream(ctx, dec, target)
	if err != nil {
		writeProxyError(client, http.StatusBadGateway, err.Error())
		return
	}
	defer upstream.Close()

	outReq := req.Clone(ctx)
	outReq.RequestURI = ""
	outReq.URL.Scheme = target.Scheme
	outReq.URL.Host = target.Authority()
	outReq.Header.Del("Proxy-Connection")
	if err := outReq.Write(upstream); err != nil {
		writeProxyError(client, http.StatusBadGateway, err.Error())
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(upstream), outReq)
	if err != nil {
		writeProxyError(client, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if err := resp.Write(client); err != nil {
		return
	}
}

func (p *Proxy) dialUpstream(ctx context.Context, dec Decision, target Target) (net.Conn, error) {
	addr := target.Authority()
	if p.testDialContext != nil {
		return p.testDialContext(ctx, "tcp", addr)
	}
	return dialFirst(ctx, dec.DialIPs, target.Port)
}

func dialFirst(ctx context.Context, ips []net.IP, port int) (net.Conn, error) {
	var last error
	dialer := &net.Dialer{Timeout: defaultProxyUpstreamTimeout}
	for _, ip := range ips {
		if err := ValidateDialIP(ip); err != nil {
			last = err
			continue
		}
		addr := net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port))
		c, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			return c, nil
		}
		last = err
	}
	if last == nil {
		return nil, fmt.Errorf("no dial addresses")
	}
	return nil, last
}

func writeProxyError(w net.Conn, code int, msg string) {
	msg = strings.ReplaceAll(msg, "\n", " ")
	body := msg + "\n"
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}

func relayBidirectional(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyClose := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		type closeWriter interface{ CloseWrite() error }
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
	}
	go copyClose(a, b)
	go copyClose(b, a)
	wg.Wait()
}
