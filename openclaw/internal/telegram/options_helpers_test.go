//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildHTTPClient_NoOverrides(t *testing.T) {
	client, err := BuildHTTPClient("", 0)
	require.NoError(t, err)
	require.Nil(t, client)
}

func TestBuildHTTPClient_InvalidProxy(t *testing.T) {
	_, err := BuildHTTPClient("://bad", 0)
	require.Error(t, err)
}

func TestBuildHTTPClient_TimeoutAndProxy(t *testing.T) {
	client, err := BuildHTTPClient("http://127.0.0.1:8080", time.Second)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, time.Second, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.Proxy)
}

func TestBuildClientOptionsFromEnv_PropagatesProxyError(t *testing.T) {
	_, err := BuildClientOptionsFromEnv(ClientNetOptions{
		ProxyURL:   "://bad",
		MaxRetries: 1,
	})
	require.Error(t, err)
}

func TestBuildClientOptionsFromEnv_OptionsCount(t *testing.T) {
	t.Setenv(telegramBaseURLEnvName, "")

	opts, err := BuildClientOptionsFromEnv(ClientNetOptions{
		MaxRetries: 3,
	})
	require.NoError(t, err)
	require.Len(t, opts, 1)

	t.Setenv(telegramBaseURLEnvName, "http://127.0.0.1:1")
	opts, err = BuildClientOptionsFromEnv(ClientNetOptions{
		MaxRetries: 3,
	})
	require.NoError(t, err)
	require.Len(t, opts, 2)

	opts, err = BuildClientOptionsFromEnv(ClientNetOptions{
		ProxyURL:   "http://127.0.0.1:8080",
		Timeout:    time.Second,
		MaxRetries: 3,
	})
	require.NoError(t, err)
	require.Len(t, opts, 3)
}

func TestBuildHTTPClient_DefaultTransportType(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	http.DefaultTransport = roundTripperFunc(func(
		_ *http.Request,
	) (*http.Response, error) {
		return nil, nil
	})

	_, err := BuildHTTPClient("", time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), errDefaultTransportType)
}
