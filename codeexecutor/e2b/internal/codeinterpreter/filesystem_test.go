//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeinterpreter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	filesystem "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdfs/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdfs/spec/filesystemconnect"
)

type sandboxFilesystemHandler struct {
	filesystemconnect.UnimplementedFilesystemHandler
}

func (*sandboxFilesystemHandler) Stat(context.Context, *connect.Request[filesystem.StatRequest]) (*connect.Response[filesystem.StatResponse], error) {
	return connect.NewResponse(&filesystem.StatResponse{Entry: &filesystem.EntryInfo{Size: 5}}), nil
}
func TestFilesystemSandboxConnection(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, tc := range []struct{ name, version, auth, user string }{
			{name: "modern", version: "0.5.2", user: "root"},
			{name: "legacy", version: "0.2.10", user: "root"},
			{name: "unknown", user: "root"},
			{name: "custom", version: "0.5.2", auth: "Basic YWxpY2U6", user: "alice"},
		} {
			t.Run(fmt.Sprintf("connect=%t/%s", existing, tc.name), func(t *testing.T) {
				t.Setenv("E2B_DEBUG", "false")
				t.Setenv("E2B_ACCESS_TOKEN", "")
				_, rpc := filesystemconnect.NewFilesystemHandler(&sandboxFilesystemHandler{})
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/files" || strings.HasPrefix(r.URL.Path, "/filesystem.Filesystem/") {
						assert.Equal(t, "49984-sandbox.actual.test", r.Host)
						assert.Equal(t, "envd-token", r.Header.Get("X-Access-Token"))
						assert.Equal(t, "traffic-token", r.Header.Get("E2B-Traffic-Access-Token"))
						assert.Equal(t, "custom", r.Header.Get("X-Custom"))
						assert.Empty(t, r.Header.Get("X-API-Key"))
						user, _, ok := r.BasicAuth()
						assert.True(t, ok)
						assert.Equal(t, tc.user, user)
						if r.URL.Path == "/files" {
							assert.Equal(t, tc.user, r.URL.Query().Get("username"))
							_, _ = w.Write([]byte("hello"))
						} else {
							rpc.ServeHTTP(w, r)
						}
						return
					}
					assert.NotEqual(t, "/execute", r.URL.Path)
					assert.Contains(t, r.URL.Path, "/sandboxes")
					assert.Equal(t, "api-key", r.Header.Get("X-API-Key"))
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"sandboxID":"sandbox","clientID":"worker","domain":"actual.test","envdVersion":%q,"envdPort":49984,"envdAccessToken":"envd-token","trafficAccessToken":"traffic-token"}`, tc.version)
				}))
				defer srv.Close()
				target, err := url.Parse(srv.URL)
				require.NoError(t, err)
				client := srv.Client()
				client.Transport = &processTestTransport{target: target, base: client.Transport}
				headers := map[string]string{"X-Custom": "custom", "Content-Type": "wrong"}
				if tc.auth != "" {
					headers["authorization"] = tc.auth
				}
				opts := &SandboxOpts{APIKey: "api-key", APIURL: srv.URL, Domain: "configured.test", HTTPClient: client, Headers: headers}
				var sandbox *Sandbox
				if existing {
					sandbox, err = Connect(context.Background(), "sandbox", opts)
				} else {
					sandbox, err = Create(context.Background(), opts)
				}
				require.NoError(t, err)
				fs, err := NewFilesystemClient(context.Background(), sandbox)
				require.NoError(t, err)
				got, err := fs.Read(context.Background(), "/file", 3)
				require.NoError(t, err)
				assert.Equal(t, "hel", string(got.Content))
				assert.EqualValues(t, 5, got.Size)
				_, err = fs.Stat(context.Background(), "/file")
				require.NoError(t, err)
				assert.Zero(t, client.Timeout, "borrowed HTTP client must not be mutated")
			})
		}
	}
}
func TestFilesystemDebugConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.URL.Query().Get("username"))
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(target.Port(), &port)
	require.NoError(t, err)
	s := &Sandbox{envdPort: port, connection: &ConnectionConfig{Domain: target.Hostname(), Debug: true, HTTPClient: srv.Client()}}
	c, err := NewFilesystemClient(context.Background(), s)
	require.NoError(t, err)
	_, err = c.Read(context.Background(), "/file", 1)
	require.NoError(t, err)
	s.connection.Headers = map[string]string{"Authorization": "Basic YWxpY2U6"}
	_, err = NewFilesystemClient(context.Background(), s)
	require.ErrorContains(t, err, "HTTPS")
}
func TestFilesystemConnectionValidation(t *testing.T) {
	_, err := NewFilesystemClient(nil, nil)
	require.ErrorContains(t, err, "nil context")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = NewFilesystemClient(ctx, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = NewFilesystemClient(context.Background(), nil)
	require.ErrorContains(t, err, "not initialized")
	s := &Sandbox{envdPort: -1, connection: &ConnectionConfig{Domain: "example.test"}}
	_, err = NewFilesystemClient(context.Background(), s)
	require.ErrorContains(t, err, "invalid envd port")
}
func TestFilesystemConnectionTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(target.Port(), &port)
	require.NoError(t, err)
	client := srv.Client()
	s := &Sandbox{envdPort: port, connection: &ConnectionConfig{Domain: target.Hostname(), Debug: true, HTTPClient: client, RequestTimeout: 20 * time.Millisecond}}
	fs, err := NewFilesystemClient(context.Background(), s)
	require.NoError(t, err)
	_, err = fs.Read(context.Background(), "/file", 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, client.Timeout)
}
