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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

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

func TestOriginBoundHTTPClientPreservesSuppliedConfiguration(t *testing.T) {
	httpsOrigin := &http.Request{URL: mustParseURL(
		t, "https://envd.example/process.Process/Start",
	)}
	callerErr := errors.New("caller redirect policy")
	policyCalls := 0
	suppliedClient := &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   time.Second,
		CheckRedirect: func(
			*http.Request,
			[]*http.Request,
		) error {
			policyCalls++
			return callerErr
		},
	}
	boundClient := newOriginBoundHTTPClient(
		suppliedClient,
		mustParseURL(t, "https://envd.example"),
	)
	require.NotSame(t, suppliedClient, boundClient)
	assert.Equal(t, suppliedClient.Timeout, boundClient.Timeout)
	assert.Same(t, http.DefaultTransport, suppliedClient.Transport)
	boundTransport, ok := boundClient.Transport.(*originBoundRoundTripper)
	require.True(t, ok)
	assert.Same(t, http.DefaultTransport, boundTransport.base)

	err := boundClient.CheckRedirect(&http.Request{URL: mustParseURL(
		t, "https://envd.example/process.Process/Start/",
	)}, []*http.Request{httpsOrigin})
	require.ErrorIs(t, err, callerErr)
	assert.Equal(t, 1, policyCalls)
}

func TestOriginBoundHTTPClientAllowsSameOriginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/finish", http.StatusTemporaryRedirect)
			return
		}
		assert.Equal(t, "/finish", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	client := newOriginBoundHTTPClient(
		server.Client(), mustParseURL(t, server.URL),
	)
	resp, err := client.Get(server.URL + "/start")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, resp.Body.Close()) })
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestNewClientRejectsCrossOriginRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		targetRequests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	origin := httptest.NewTLSServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(origin.Close)

	client, err := NewClient(
		origin.URL,
		origin.Client(),
		http.Header{"X-Access-Token": {"secret"}},
	)
	require.NoError(t, err)

	_, err = client.List(context.Background())
	require.ErrorContains(t, err, "outside configured origin")
	assert.Zero(t, targetRequests.Load())
}

func TestOriginBoundRoundTripperRejectsSchemeAndPortChanges(t *testing.T) {
	transportCalls := 0
	transport := roundTripperFunc(func(
		*http.Request,
	) (*http.Response, error) {
		transportCalls++
		return &http.Response{StatusCode: http.StatusNoContent}, nil
	})
	boundTransport := &originBoundRoundTripper{
		base: transport,
		origin: originFromURL(mustParseURL(
			t, "https://envd.example",
		)),
	}

	for _, tt := range []struct {
		name      string
		targetURL string
	}{
		{
			name:      "HTTPSDowngrade",
			targetURL: "http://envd.example:443/process.Process/Start",
		},
		{
			name:      "DifferentPort",
			targetURL: "https://envd.example:49983/process.Process/Start",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := boundTransport.RoundTrip(&http.Request{
				URL: mustParseURL(t, tt.targetURL),
			})
			require.ErrorContains(t, err, "outside configured origin")
		})
	}
	assert.Zero(t, transportCalls)
}

func TestOriginBoundHTTPClientRetainsDefaultRedirectPolicy(t *testing.T) {
	client := newOriginBoundHTTPClient(
		&http.Client{}, mustParseURL(t, "https://envd.example"),
	)
	assert.Nil(t, client.CheckRedirect)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(
	req *http.Request,
) (*http.Response, error) {
	return f(req)
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u
}
