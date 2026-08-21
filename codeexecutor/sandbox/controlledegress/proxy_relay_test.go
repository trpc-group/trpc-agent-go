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
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
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
	"sync"
	"sync/atomic"
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
	proxy, err := startTestProxy(
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

func TestRelayCapsActiveConnections(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	var dialed atomic.Int32
	serverEnds := make(chan net.Conn, defaultRelayMaxConnections)
	relay, err := startRelayWithDialer(
		port,
		"test",
		func(context.Context) (net.Conn, error) {
			clientEnd, serverEnd := net.Pipe()
			serverEnds <- serverEnd
			dialed.Add(1)
			return clientEnd, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var clients []net.Conn
	for i := 0; i < defaultRelayMaxConnections+12; i++ {
		conn, dialErr := net.Dial("tcp", relay.Addr())
		if dialErr == nil {
			clients = append(clients, conn)
		}
	}
	deadline := time.Now().Add(time.Second)
	for dialed.Load() < defaultRelayMaxConnections &&
		time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := dialed.Load(); got != defaultRelayMaxConnections {
		t.Fatalf("active upstreams = %d, want %d", got, defaultRelayMaxConnections)
	}
	for _, conn := range clients {
		_ = conn.Close()
	}
	for i := int32(0); i < dialed.Load(); i++ {
		_ = (<-serverEnds).Close()
	}
	if err := relay.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProxyCloseClosesActiveUpstream(t *testing.T) {
	proxyEnd, serverEnd := net.Pipe()
	t.Cleanup(func() { _ = serverEnd.Close() })
	requestRead := make(chan struct{})
	go func() {
		_, _ = http.ReadRequest(bufio.NewReader(serverEnd))
		close(requestRead)
	}()
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{
		addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}},
	}
	proxy, err := startTestProxy(
		filepath.Join(t.TempDir(), "proxy.sock"),
		policy,
		func(context.Context, string, string) (net.Conn, error) {
			return proxyEnd, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("unix", proxy.UnixPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_, _ = io.WriteString(
		client,
		"GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n",
	)
	<-requestRead
	closed := make(chan error, 1)
	go func() { closed <- proxy.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Proxy.Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Proxy.Close blocked on an active upstream")
	}
}

func TestProxyDenyAndRelayHTTP(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "proxy.sock")

	auditor := &memoryAuditor{}
	policy := StaticAllowlist("example.com")
	policy.Auditor = auditor
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}

	proxy, err := startTestProxy(sock, policy)
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

func TestProxyHTTPRejectsMismatchedHost(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	var dialed atomic.Bool
	proxy, err := startTestProxy(
		sock,
		policy,
		func(context.Context, string, string) (net.Conn, error) {
			dialed.Store(true)
			return nil, errors.New("should not dial mismatched Host")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		proxy.handleHTTP(
			context.Background(),
			server,
			&http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "http", Host: "example.com", Path: "/path"},
				Host:   "other.example",
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
	defer resp.Body.Close()
	_ = client.Close()
	<-done
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if dialed.Load() {
		t.Fatal("mismatched Host reached upstream dial")
	}
}

func TestProxyHTTPCanonicalizesMatchingHost(t *testing.T) {
	var gotHost string
	var gotProxyAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotProxyAuthorization = r.Header.Get("Proxy-Authorization")
		_, _ = io.WriteString(w, "OK")
	}))
	t.Cleanup(upstream.Close)

	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	proxy, err := startTestProxy(sock, policy, func(ctx context.Context, network, address string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	raw := "GET http://example.com/path HTTP/1.1\r\n" +
		"Host: EXAMPLE.COM:80\r\n" +
		"Proxy-Authorization: Basic secret\r\n\r\n"
	if _, err := io.WriteString(conn, raw); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotHost != "example.com" {
		t.Fatalf("upstream Host = %q, want example.com", gotHost)
	}
	if gotProxyAuthorization != "" {
		t.Fatalf("upstream received Proxy-Authorization %q", gotProxyAuthorization)
	}
}

func TestProxyCONNECTIPRejectsNonTLS(t *testing.T) {
	upstream, peer := net.Pipe()
	t.Cleanup(func() {
		_ = upstream.Close()
		_ = peer.Close()
	})
	proxy, err := startTestProxy(
		filepath.Join(t.TempDir(), "proxy.sock"),
		StaticAllowlist("1.1.1.1"),
		func(context.Context, string, string) (net.Conn, error) {
			return upstream, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	conn, err := net.Dial("unix", proxy.UnixPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(
		conn,
		"CONNECT 1.1.1.1:443 HTTP/1.1\r\nHost: 1.1.1.1:443\r\n\r\n",
	)
	resp, err := http.ReadResponse(
		bufio.NewReader(conn),
		&http.Request{Method: http.MethodConnect},
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}
	_, _ = conn.Write([]byte("PLAIN"))
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var buf [5]byte
	if n, _ := peer.Read(buf[:]); n != 0 {
		t.Fatalf("non-TLS payload reached upstream: %q", buf[:n])
	}
}

func TestProxyCONNECTFlushesBufferedBytesAfterSNI(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	t.Cleanup(func() {
		_ = clientEnd.Close()
		_ = serverEnd.Close()
	})
	received := make(chan []byte, 1)
	go func() {
		defer close(received)
		_ = serverEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		var got []byte
		buf := make([]byte, 1024)
		for {
			n, err := serverEnd.Read(buf)
			if n > 0 {
				got = append(got, buf[:n]...)
				if bytes.Contains(got, []byte("TRAILING")) {
					received <- got
					_ = serverEnd.Close()
					return
				}
			}
			if err != nil {
				received <- got
				return
			}
		}
	}()

	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := StaticAllowlist("example.com")
	policy.Resolver = stubResolver{addrs: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}}
	proxy, err := startTestProxy(sock, policy, func(context.Context, string, string) (net.Conn, error) {
		return clientEnd, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	hello := tlsClientHelloRecord("example.com")
	payload := append(append([]byte(nil), hello...), []byte("TRAILING")...)
	if _, err := fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d", resp.StatusCode)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	got := <-received
	if !bytes.Contains(got, hello) {
		t.Fatalf("upstream missing ClientHello: %q", got)
	}
	if !bytes.Contains(got, []byte("TRAILING")) {
		t.Fatalf("upstream missing buffered trailing bytes: %q", got)
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

	proxy, err := startTestProxy(sock, policy, func(ctx context.Context, network, address string) (net.Conn, error) {
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
	proxy, err := startTestProxy(
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
	proxy, err := startTestProxy(sock, policy, func(ctx context.Context, network, address string) (net.Conn, error) {
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

func tlsClientHelloRecord(sni string) []byte {
	nameEntry := []byte{0}
	var nameLen [2]byte
	binary.BigEndian.PutUint16(nameLen[:], uint16(len(sni)))
	nameEntry = append(nameEntry, nameLen[:]...)
	nameEntry = append(nameEntry, sni...)
	var listLen [2]byte
	binary.BigEndian.PutUint16(listLen[:], uint16(len(nameEntry)))
	list := append(listLen[:], nameEntry...)
	ext := []byte{0, 0}
	var extLen [2]byte
	binary.BigEndian.PutUint16(extLen[:], uint16(len(list)))
	ext = append(ext, extLen[:]...)
	ext = append(ext, list...)
	body := minimalClientHelloBody(ext)
	handshake := []byte{
		1,
		byte(len(body) >> 16),
		byte(len(body) >> 8),
		byte(len(body)),
	}
	handshake = append(handshake, body...)
	record := []byte{
		22, 3, 3,
		byte(len(handshake) >> 8),
		byte(len(handshake)),
	}
	return append(record, handshake...)
}

func TestRelayCloseCancelsUpstreamDial(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	dialStarted := make(chan struct{})
	relay, err := startRelayWithDialer(
		port,
		"blocking-dial",
		func(ctx context.Context) (net.Conn, error) {
			close(dialStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", relay.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("relay did not enter upstream dial")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- relay.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Relay.Close did not cancel the upstream dial")
	}
}

type contextBlockingAuditor struct {
	started     chan struct{}
	startedOnce sync.Once
}

func (a *contextBlockingAuditor) Record(ctx context.Context, _ AuditEvent) {
	a.startedOnce.Do(func() { close(a.started) })
	<-ctx.Done()
}

func TestProxyCloseCancelsAuditor(t *testing.T) {
	auditor := &contextBlockingAuditor{started: make(chan struct{})}
	policy := StaticAllowlist("allowed.example")
	policy.Auditor = auditor
	proxy, err := StartProxy(
		filepath.Join(t.TempDir(), "proxy.sock"),
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("unix", proxy.UnixPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := fmt.Fprint(
		client,
		"GET http://denied.example/ HTTP/1.1\r\nHost: denied.example\r\n\r\n",
	); err != nil {
		t.Fatal(err)
	}
	select {
	case <-auditor.started:
	case <-time.After(time.Second):
		t.Fatal("proxy did not enter auditor callback")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- proxy.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Proxy.Close did not cancel the auditor callback")
	}
	_, _ = io.Copy(io.Discard, client)
}

func TestProxyRemovesResponseHopByHopHeaders(t *testing.T) {
	upstream, peer := net.Pipe()
	defer peer.Close()
	go func() {
		defer peer.Close()
		_, _ = http.ReadRequest(bufio.NewReader(peer))
		_, _ = fmt.Fprint(
			peer,
			"HTTP/1.1 200 OK\r\n"+
				"Content-Length: 0\r\n"+
				"Connection: X-Test-Hop\r\n"+
				"X-Test-Hop: leaked\r\n\r\n",
		)
	}()
	proxy, err := startTestProxy(
		filepath.Join(t.TempDir(), "proxy.sock"),
		StaticAllowlist("1.1.1.1"),
		func(context.Context, string, string) (net.Conn, error) {
			return upstream, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	client, err := net.Dial("unix", proxy.UnixPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := fmt.Fprint(
		client,
		"GET http://1.1.1.1/ HTTP/1.1\r\nHost: 1.1.1.1\r\n\r\n",
	); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(
		bufio.NewReader(client),
		&http.Request{Method: http.MethodGet},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("X-Test-Hop"); got != "" {
		t.Fatalf("hop-by-hop response header leaked: %q", got)
	}
}
