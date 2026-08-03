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
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareUnixSocketPathSafety(t *testing.T) {
	if err := prepareUnixSocketPath(""); err == nil {
		t.Fatal("empty socket path was accepted")
	}

	regular := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(regular, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareUnixSocketPath(regular); err == nil {
		t.Fatal("regular file was accepted as replaceable socket")
	}

	activePath := filepath.Join(t.TempDir(), "active.sock")
	active, err := net.Listen("unix", activePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareUnixSocketPath(activePath); err == nil {
		t.Fatal("active socket was accepted as stale")
	}
	_ = active.Close()

	stalePath := filepath.Join(t.TempDir(), "stale.sock")
	stale, err := net.Listen("unix", stalePath)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if err := prepareUnixSocketPath(stalePath); err != nil {
		t.Fatalf("stale socket cleanup: %v", err)
	}
	if _, err := os.Lstat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale socket still exists: %v", err)
	}
}

func TestProxyReturnsProtocolAndDialErrors(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	proxy, err := StartTestProxy(
		sock,
		policy,
		func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("forced dial failure")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	tests := []struct {
		name string
		raw  string
		want int
	}{
		{
			name: "connect denied",
			raw:  "CONNECT evil.example:443 HTTP/1.1\r\nHost: evil.example:443\r\n\r\n",
			want: http.StatusForbidden,
		},
		{
			name: "connect dial failure",
			raw:  "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n",
			want: http.StatusBadGateway,
		},
		{
			name: "unsupported HTTP scheme",
			raw:  "GET ftp://example.com/file HTTP/1.1\r\nHost: example.com\r\n\r\n",
			want: http.StatusBadRequest,
		},
		{
			name: "HTTP dial failure",
			raw:  "GET http://example.com/file HTTP/1.1\r\nHost: example.com\r\n\r\n",
			want: http.StatusBadGateway,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := net.Dial("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := io.WriteString(conn, tt.raw); err != nil {
				t.Fatal(err)
			}
			resp, err := http.ReadResponse(
				bufio.NewReader(conn),
				&http.Request{Method: strings.Fields(tt.raw)[0]},
			)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}

	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.handleHTTP(
			context.Background(),
			server,
			&http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Path: "/"},
				Header: make(http.Header),
			},
		)
		_ = server.Close()
	}()
	resp, err := http.ReadResponse(
		bufio.NewReader(client),
		&http.Request{Method: http.MethodGet},
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = client.Close()
	<-done
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("origin-form missing host status = %d, want 400", resp.StatusCode)
	}
}

func TestDialFirstFailurePaths(t *testing.T) {
	if _, err := dialFirst(context.Background(), nil, 443); err == nil {
		t.Fatal("empty dial address list was accepted")
	}
	if _, err := dialFirst(
		context.Background(),
		[]net.IP{net.ParseIP("127.0.0.1")},
		443,
	); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("private dial error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dialFirst(
		ctx,
		[]net.IP{net.ParseIP("1.1.1.1")},
		443,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled public dial error = %v, want context canceled", err)
	}
}

func TestRelayPropagatesUpstreamCloseToTCPClient(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "relay.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		_ = conn.Close()
	}()

	relay, err := StartRelay(0, sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })
	client, err := net.Dial("tcp", relay.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	if _, err := client.Read(buf[:]); err != io.EOF {
		t.Fatalf("read error = %v, want EOF", err)
	}
	<-serverDone
}

func TestRelayCloseClosesActiveConnections(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "relay.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		close(accepted)
		_, _ = io.Copy(io.Discard, conn)
		_ = conn.Close()
	}()

	relay, err := StartRelay(0, sock)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", relay.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("relay did not connect to upstream")
	}
	closed := make(chan error, 1)
	go func() { closed <- relay.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("relay close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Relay.Close blocked with an active connection")
	}
	<-serverDone
}

func TestProxyDenyAndRelayHTTP(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "proxy.sock")

	auditor := &memoryAuditor{}
	policy := StaticAllowlist("example.com")
	policy.Auditor = auditor
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}

	proxy, err := StartTestProxy(sock, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	relay, err := StartRelay(port, sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: relay.Addr()}),
		},
	}
	resp, err := client.Get("http://evil.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if len(auditor.Events) == 0 {
		t.Fatal("expected audit events")
	}
}

func TestProxyAllowViaDialHook(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "UPSTREAM_OK")
	}))
	t.Cleanup(upstream.Close)

	dir := t.TempDir()
	sock := filepath.Join(dir, "proxy.sock")
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}

	proxy, err := StartTestProxy(sock, policy, func(ctx context.Context, network, address string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", sock)
			},
		},
	}
	resp, err := client.Get("http://example.com/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "UPSTREAM_OK" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}

func TestStartProxySnapshotsPolicySlices(t *testing.T) {
	hosts := []string{"example.com"}
	ports := []int{443}
	policy := Policy{
		AllowedHosts: hosts,
		AllowedPorts: ports,
	}
	proxy, err := StartTestProxy(
		filepath.Join(t.TempDir(), "proxy.sock"),
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	hosts[0] = "evil.example"
	ports[0] = 22
	if got := proxy.policy.AllowedHosts[0]; got != "example.com" {
		t.Fatalf("proxy host policy changed to %q", got)
	}
	if got := proxy.policy.AllowedPorts[0]; got != 443 {
		t.Fatalf("proxy port policy changed to %d", got)
	}
}

func TestProxyCONNECTRequiresMatchingSNI(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "TLS_OK")
	}))
	t.Cleanup(upstream.Close)

	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	proxy, err := StartTestProxy(sock, policy, func(ctx context.Context, network, address string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: "proxy.invalid"}),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return net.Dial("unix", sock)
		},
		TLSClientConfig: &tls.Config{ // #nosec G402 -- local test TLS server.
			InsecureSkipVerify: true,
		},
	}}
	resp, err := client.Get("https://example.com/")
	if err != nil {
		t.Fatalf("matching SNI request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "TLS_OK" {
		t.Fatalf("body = %q, want TLS_OK", body)
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprint(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	connectResp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	_ = connectResp.Body.Close()
	if connectResp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d", connectResp.StatusCode)
	}
	tlsConn := tls.Client(conn, &tls.Config{ // #nosec G402 -- local mismatch probe.
		ServerName:         "evil.example",
		InsecureSkipVerify: true,
	})
	if err := tlsConn.Handshake(); err == nil {
		t.Fatal("mismatched SNI handshake succeeded")
	}
}

func TestStartProxySecuresSocketAndRedactsAudit(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	auditor := &memoryAuditor{}
	policy := StaticAllowlist("example.com")
	policy.Auditor = auditor
	identity := RunIdentity{
		Principal: "principal",
		Session:   "session",
		Request:   "request",
	}
	proxy, err := StartProxy(sock, policy, WithRunIdentity(identity))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprint(conn, "GET http://user:secret@evil.example/path?token=secret HTTP/1.1\r\nHost: evil.example\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = conn.Close()
	if len(auditor.Events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(auditor.Events))
	}
	event := auditor.Events[0]
	if event.Principal != identity.Principal ||
		event.Session != identity.Session ||
		event.Request != identity.Request {
		t.Fatalf("audit identity = %#v, want %#v", event, identity)
	}
	if event.Target.Path != "" || event.Target.Original != "" ||
		event.Target.HostHeader != "" {
		t.Fatalf("audit target leaked request data: %#v", event.Target)
	}
}

func TestProxyRejectsOversizedHeaders(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	proxy, err := StartProxy(sock, StaticAllowlist("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := "GET http://example.com/ HTTP/1.1\r\nX-Large: " +
		strings.Repeat("a", defaultProxyMaxHeaderBytes) + "\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status = %d, want 431", resp.StatusCode)
	}
}

func TestRelayForwardsToUnix(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "echo.sock")
	ul, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ul.Close() })
	go func() {
		c, err := ul.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		_, _ = c.Write([]byte("ECHO:" + string(buf[:n])))
	}()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	relay, err := StartRelay(port, sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })

	c, err := net.Dial("tcp", relay.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("ping"))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "ECHO:ping" {
		t.Fatalf("got %q", got)
	}
}

func TestSSRFBlocksLoopbackLiteralHost(t *testing.T) {
	p := StaticAllowlist("127.0.0.1")
	d := p.Decide(context.Background(), Target{Host: "127.0.0.1", Port: 80})
	if d.Allow {
		t.Fatal("loopback literal must be denied")
	}
	if !strings.Contains(d.Reason, "blocked") {
		t.Fatalf("reason = %q", d.Reason)
	}
}
