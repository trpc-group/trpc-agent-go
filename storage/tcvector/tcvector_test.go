//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package tcvector provides the tcvectordb instance info management.
package tcvector

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

// fakeClient implements ClientInterface for tests.
type fakeClient struct {
	tcvectordb.DatabaseInterface
	tcvectordb.FlatInterface
}

func TestSetGetClientBuilder(t *testing.T) {
	oldRegistry := tcvectorRegistry
	tcvectorRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { tcvectorRegistry = oldRegistry }()

	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	invoked := false
	custom := func(opts ...ClientBuilderOpt) (ClientInterface, error) {
		invoked = true
		return &fakeClient{}, nil
	}

	SetClientBuilder(custom)
	b := GetClientBuilder()
	cli, err := b(
		WithClientBuilderHTTPURL("http://localhost"),
		WithClientBuilderUserName("user"),
		WithClientBuilderKey("key"),
	)
	require.NoError(t, err)
	require.NotNil(t, cli)
	require.True(t, invoked, "custom builder not invoked")
}

func TestDefaultClientBuilder_ValidatesRequired(t *testing.T) {
	_, err := defaultClientBuilder()
	require.Error(t, err)
	require.Equal(t, "HTTPURL is required", err.Error())

	_, err = defaultClientBuilder(WithClientBuilderHTTPURL("http://localhost"))
	require.Error(t, err)
	require.Equal(t, "UserName is required", err.Error())

	_, err = defaultClientBuilder(
		WithClientBuilderHTTPURL("http://localhost"),
		WithClientBuilderUserName("user"),
	)
	require.Error(t, err)
	require.Equal(t, "Key is required", err.Error())
}

func TestDefaultClientBuilder_Success(t *testing.T) {
	cli, err := defaultClientBuilder(
		WithClientBuilderHTTPURL("http://localhost"),
		WithClientBuilderUserName("user"),
		WithClientBuilderKey("key"),
	)
	require.NoError(t, err)
	require.NotNil(t, cli)
}

func TestRegisterAndGetTcVectorInstance(t *testing.T) {
	oldRegistry := tcvectorRegistry
	tcvectorRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { tcvectorRegistry = oldRegistry }()

	const name = "test-tcvector"
	RegisterTcVectorInstance(name,
		WithClientBuilderHTTPURL("http://localhost"),
		WithClientBuilderUserName("user"),
		WithClientBuilderKey("key"),
		WithExtraOptions("opt1", 2),
	)

	opts, ok := GetTcVectorInstance(name)
	require.True(t, ok, "expected instance to exist")
	require.GreaterOrEqual(t, len(opts), 4, "expected at least 4 options (extra opts included)")

	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, "http://localhost", cfg.HTTPURL)
	require.Equal(t, "user", cfg.UserName)
	require.Equal(t, "key", cfg.Key)
	require.Len(t, cfg.ExtraOptions, 2)
	require.Equal(t, "opt1", cfg.ExtraOptions[0])
	require.Equal(t, 2, cfg.ExtraOptions[1])

	// default builder should still succeed and ignore ExtraOptions safely
	cli, err := defaultClientBuilder(
		WithClientBuilderHTTPURL(cfg.HTTPURL),
		WithClientBuilderUserName(cfg.UserName),
		WithClientBuilderKey(cfg.Key),
	)
	require.NoError(t, err)
	require.NotNil(t, cli)
}

// Ensure DefaultClientBuilder propagates invalid URL error from SDK.
func TestDefaultClientBuilder_InvalidURL(t *testing.T) {
	const (
		badURL = "ftp://example.com"
		user   = "user"
		key    = "key"
	)
	_, err := defaultClientBuilder(
		WithClientBuilderHTTPURL(badURL),
		WithClientBuilderUserName(user),
		WithClientBuilderKey(key),
	)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "invalid url param with:"))
}

// RegisterTcVectorInstance overwrites previous options for the same name.
func TestRegisterTcVectorInstance_Overwrite(t *testing.T) {
	oldRegistry := tcvectorRegistry
	tcvectorRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { tcvectorRegistry = oldRegistry }()

	const name = "overwrite-tcvector"
	RegisterTcVectorInstance(name,
		WithClientBuilderHTTPURL("http://a"),
		WithClientBuilderUserName("ua"),
		WithClientBuilderKey("ka"),
		WithExtraOptions("optA"),
	)
	RegisterTcVectorInstance(name,
		WithClientBuilderHTTPURL("http://b"),
		WithClientBuilderUserName("ub"),
		WithClientBuilderKey("kb"),
		WithExtraOptions("optB", 99),
	)

	opts, ok := GetTcVectorInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 4)

	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, "http://b", cfg.HTTPURL)
	require.Equal(t, "ub", cfg.UserName)
	require.Equal(t, "kb", cfg.Key)
	require.Len(t, cfg.ExtraOptions, 2)
	require.Equal(t, "optB", cfg.ExtraOptions[0])
	require.Equal(t, 99, cfg.ExtraOptions[1])
}

// GetTcVectorInstance should return false for unknown names.
func TestGetTcVectorInstance_NotFound(t *testing.T) {
	oldRegistry := tcvectorRegistry
	tcvectorRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { tcvectorRegistry = oldRegistry }()

	opts, ok := GetTcVectorInstance("not-exist")
	require.False(t, ok)
	require.Nil(t, opts)
}
