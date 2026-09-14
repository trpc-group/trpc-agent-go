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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess"
	process "trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdprocess/spec/processconnect"
)

type sandboxProcessHandler struct {
	processconnect.UnimplementedProcessHandler
	started atomic.Int32
	stdin   chan struct{}
}

func (h *sandboxProcessHandler) Start(ctx context.Context, req *connect.Request[process.StartRequest], stream *connect.ServerStream[process.StartResponse]) error {
	h.started.Add(1)
	if err := stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
		Event: &process.ProcessEvent_Start{Start: &process.ProcessEvent_StartEvent{Pid: 42}},
	}}); err != nil {
		return err
	}
	if req.Msg.GetStdin() {
		select {
		case <-h.stdin:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return stream.Send(&process.StartResponse{Event: &process.ProcessEvent{
		Event: &process.ProcessEvent_End{End: &process.ProcessEvent_EndEvent{Exited: true}},
	}})
}

func (h *sandboxProcessHandler) SendInput(context.Context, *connect.Request[process.SendInputRequest]) (*connect.Response[process.SendInputResponse], error) {
	return connect.NewResponse(&process.SendInputResponse{}), nil
}

func (h *sandboxProcessHandler) CloseStdin(context.Context, *connect.Request[process.CloseStdinRequest]) (*connect.Response[process.CloseStdinResponse], error) {
	close(h.stdin)
	return connect.NewResponse(&process.CloseStdinResponse{}), nil
}

type processTestTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t *processTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Host = req.URL.Host
	req.URL.Scheme, req.URL.Host = t.target.Scheme, t.target.Host
	return t.base.RoundTrip(req)
}

func TestRunProcessSandboxConnection(t *testing.T) {
	for _, connectExisting := range []bool{false, true} {
		for _, tc := range []struct {
			name, version, refreshedVersion, stdin, authorization, wantAuthorization, wantError string
			port                                                                                int
		}{
			{name: "modern", version: "0.5.2", stdin: "data", port: 49984},
			{name: "legacy", version: "0.2.10", wantAuthorization: "Basic dXNlcjo="},
			{name: "custom user", version: "0.2.10", authorization: "Basic cm9vdDo=", wantAuthorization: "Basic cm9vdDo="},
			{name: "unsupported stdin", version: "0.2.10", stdin: "data", wantError: "finite stdin requires envd >= 0.5.2"},
			{name: "refresh version", refreshedVersion: "0.5.2", stdin: "data"},
			{name: "unknown version", stdin: "data", wantError: "known envd version"},
			{name: "unknown without stdin"},
			{name: "invalid version", version: "bad", wantError: "invalid envd version"},
		} {
			t.Run(fmt.Sprintf("connect=%t/%s", connectExisting, tc.name), func(t *testing.T) {
				t.Setenv("E2B_DEBUG", "false")
				t.Setenv("E2B_ACCESS_TOKEN", "")
				h := &sandboxProcessHandler{stdin: make(chan struct{})}
				_, rpc := processconnect.NewProcessHandler(h)
				var metadataReads atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/process.Process/") {
						port := tc.port
						if port == 0 {
							port = 49983
						}
						assert.Equal(t, fmt.Sprintf("%d-sandbox.actual.test", port), r.Host)
						assert.Equal(t, "envd-token", r.Header.Get("X-Access-Token"))
						assert.Equal(t, "traffic-token", r.Header.Get("E2B-Traffic-Access-Token"))
						assert.Equal(t, "custom", r.Header.Get("X-Custom"))
						assert.Empty(t, r.Header.Get("X-API-Key"))
						if strings.HasSuffix(r.URL.Path, "/Start") {
							assert.Equal(t, tc.wantAuthorization, r.Header.Get("Authorization"))
							assert.Contains(t, r.Header.Get("Content-Type"), "application/connect+")
						}
						rpc.ServeHTTP(w, r)
						return
					}
					assert.Equal(t, "api-key", r.Header.Get("X-API-Key"))
					version := tc.version
					if r.Method == http.MethodGet {
						metadataReads.Add(1)
						version = tc.refreshedVersion
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"sandboxID":"sandbox","clientID":"worker","domain":"actual.test","envdVersion":%q,"envdPort":%d,"envdAccessToken":"envd-token","trafficAccessToken":"traffic-token"}`, version, tc.port)
				}))
				defer server.Close()
				target, err := url.Parse(server.URL)
				require.NoError(t, err)
				client := server.Client()
				client.Transport = &processTestTransport{target: target, base: client.Transport}
				headers := map[string]string{"X-Custom": "custom", "Content-Type": "application/json"}
				if tc.authorization != "" {
					headers["Authorization"] = tc.authorization
				}
				opts := &SandboxOpts{APIKey: "api-key", APIURL: server.URL, Domain: "configured.test", HTTPClient: client, Headers: headers}
				var sandbox *Sandbox
				if connectExisting {
					sandbox, err = Connect(context.Background(), "sandbox", opts)
				} else {
					sandbox, err = Create(context.Background(), opts)
				}
				require.NoError(t, err)
				_, err = RunProcess(context.Background(), sandbox, envdprocess.Request{Cmd: "echo", Stdin: tc.stdin})
				if tc.wantError != "" {
					require.ErrorContains(t, err, tc.wantError)
					assert.Zero(t, h.started.Load())
				} else {
					require.NoError(t, err)
					assert.EqualValues(t, 1, h.started.Load())
				}
				wantReads := int32(0)
				if tc.version == "" && tc.stdin != "" {
					wantReads = 1
				}
				assert.Equal(t, wantReads, metadataReads.Load())
				assert.Zero(t, client.Timeout, "process configuration must not modify the HTTP client")
			})
		}
	}
}

func TestRunProcessRejectsInvalidConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunProcess(ctx, nil, envdprocess.Request{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = RunProcess(nil, nil, envdprocess.Request{})
	require.ErrorContains(t, err, "nil context")
	_, err = RunProcess(context.Background(), nil, envdprocess.Request{})
	require.ErrorContains(t, err, "sandbox not initialized")
	s := &Sandbox{envdVersion: "0.5.2", connection: &ConnectionConfig{Domain: "remote.test", Debug: true}}
	_, err = RunProcess(context.Background(), s, envdprocess.Request{Cmd: "true"})
	require.ErrorContains(t, err, "HTTPS")
	s.envdPort = -1
	_, err = RunProcess(context.Background(), s, envdprocess.Request{Cmd: "true"})
	require.ErrorContains(t, err, "invalid envd port")
}

func TestRunProcessMetadataRefreshHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	s := &Sandbox{connection: &ConnectionConfig{APIURL: server.URL, HTTPClient: server.Client(), RequestTimeout: time.Minute}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := RunProcess(ctx, s, envdprocess.Request{Cmd: "cat", Stdin: "data"})
	require.ErrorContains(t, err, "discover envd stdin capability")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRunProcessLoopbackDebug(t *testing.T) {
	h := &sandboxProcessHandler{stdin: make(chan struct{})}
	_, rpc := processconnect.NewProcessHandler(h)
	server := httptest.NewServer(rpc)
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	s := &Sandbox{envdPort: port, envdVersion: "0.5.2", connection: &ConnectionConfig{
		Domain: u.Hostname(), Debug: true, HTTPClient: server.Client(),
	}}
	_, err = RunProcess(context.Background(), s, envdprocess.Request{Cmd: "true"})
	require.NoError(t, err)
	s.connection.AccessToken = "must-not-send"
	_, err = RunProcess(context.Background(), s, envdprocess.Request{Cmd: "true"})
	require.ErrorContains(t, err, "headers require HTTPS")
	assert.EqualValues(t, 1, h.started.Load())
}

func TestLegacyEnvdUser(t *testing.T) {
	for version, want := range map[string]bool{
		"0.2.10": true, "v0.3.9+build": true, "0.4.0-rc.1": true,
		"0.4.0": false, "0.5.2": false, "1.0.0": false, "": false,
	} {
		assert.Equal(t, want, legacyEnvdUser(version), version)
	}
}
