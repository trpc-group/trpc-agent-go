//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdprocess

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientRejectsInvalidBaseURL(t *testing.T) {
	_, err := NewClient("not-a-url", nil, nil)
	require.Error(t, err)
}

func TestNewClientUsesDefaultHTTPClient(t *testing.T) {
	client, err := NewClient("https://envd.example", nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, client.processClient)
}

func TestNewClientRejectsInsecureRemoteBaseURL(t *testing.T) {
	_, err := NewClient("http://envd.example", nil, nil)
	require.ErrorContains(t, err, "remote base URL must use HTTPS")
}

func TestNewClientRejectsBaseURLCredentials(t *testing.T) {
	_, err := NewClient("https://user:secret@envd.example", nil, nil)
	require.ErrorContains(t, err, "must not contain user credentials")
}

func TestNewClientAllowsCredentiallessLoopbackHTTP(t *testing.T) {
	for _, baseURL := range []string{
		"http://localhost:49983",
		"http://127.0.0.1:49983",
		"http://[::1]:49983",
	} {
		t.Run(baseURL, func(t *testing.T) {
			client, err := NewClient(baseURL, nil, nil)
			require.NoError(t, err)
			assert.False(t, client.credentialsAllowed)
		})
	}
}

func TestNewClientRejectsHeadersOverLoopbackHTTP(t *testing.T) {
	_, err := NewClient(
		"http://127.0.0.1:49983",
		nil,
		http.Header{"X-Access-Token": {"secret"}},
	)
	require.ErrorContains(t, err, "configured headers require HTTPS")
}

func TestStartRejectsProcessUserOverLoopbackHTTP(t *testing.T) {
	client, err := NewClient("http://127.0.0.1:49983", nil, nil)
	require.NoError(t, err)

	_, err = client.Start(context.Background(), Request{
		Cmd:  "true",
		User: "sandbox-user",
	})
	require.ErrorContains(t, err, "process user requires HTTPS")
}

func TestDefaultHTTPClientRedirectPolicy(t *testing.T) {
	httpsOrigin := &http.Request{URL: mustParseURL(
		t, "https://envd.example/process.Process/Start",
	)}
	tests := []struct {
		name    string
		target  string
		wantErr string
	}{
		{
			name:   "SameHostHTTPS",
			target: "https://envd.example/process.Process/Start/",
		},
		{
			name:    "CrossHost",
			target:  "https://redirect.example/process.Process/Start",
			wantErr: "cross-host redirect",
		},
		{
			name:    "HTTPSDowngrade",
			target:  "http://envd.example/process.Process/Start",
			wantErr: "HTTPS downgrade redirect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &http.Request{URL: mustParseURL(t, tt.target)}
			err := newDefaultHTTPClient().CheckRedirect(
				target, []*http.Request{httpsOrigin},
			)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u
}
