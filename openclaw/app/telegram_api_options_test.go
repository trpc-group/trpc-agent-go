//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMakeTelegramHTTPClient_NoOverrides(t *testing.T) {
	client, err := makeTelegramHTTPClient("", 0)
	require.NoError(t, err)
	require.Nil(t, client)
}

func TestMakeTelegramHTTPClient_InvalidProxy(t *testing.T) {
	_, err := makeTelegramHTTPClient("://bad", 0)
	require.Error(t, err)
}

func TestMakeTelegramHTTPClient_TimeoutAndProxy(t *testing.T) {
	client, err := makeTelegramHTTPClient("http://127.0.0.1:8080", time.Second)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.Equal(t, time.Second, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.Proxy)
}

func TestMakeTelegramAPIOptions_PropagatesProxyError(t *testing.T) {
	_, err := makeTelegramAPIOptions("://bad", 0, 1)
	require.Error(t, err)
}

func TestMakeTelegramAPIOptions_OptionsCount(t *testing.T) {
	t.Setenv(telegramBaseURLEnvName, "")

	opts, err := makeTelegramAPIOptions("", 0, 3)
	require.NoError(t, err)
	require.Len(t, opts, 1)

	t.Setenv(telegramBaseURLEnvName, "http://127.0.0.1:1")
	opts, err = makeTelegramAPIOptions("", 0, 3)
	require.NoError(t, err)
	require.Len(t, opts, 2)

	opts, err = makeTelegramAPIOptions("http://127.0.0.1:8080", time.Second, 3)
	require.NoError(t, err)
	require.Len(t, opts, 3)
}
