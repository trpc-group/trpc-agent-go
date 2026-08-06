//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInMemoryFactoryCreatesServices(t *testing.T) {
	sessionSvc, memorySvc, profile, err := InMemoryFactory()()
	require.NoError(t, err)
	require.NotNil(t, sessionSvc)
	require.NotNil(t, memorySvc)
	require.Equal(t, "inmemory", profile.Name)
	require.NoError(t, sessionSvc.Close())
	require.NoError(t, memorySvc.Close())
}

func TestOptionalBackendFactoriesRequireConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		factory BackendFactory
	}{
		{name: "postgres", envKey: "POSTGRES_DSN", factory: PostgresFactory()},
		{name: "mysql", envKey: "MYSQL_DSN", factory: MySQLFactory()},
		{name: "clickhouse", envKey: "CLICKHOUSE_DSN", factory: ClickHouseFactory()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, "")
			sessionSvc, memorySvc, profile, err := tc.factory()
			require.ErrorIs(t, err, ErrBackendNotConfigured)
			require.Nil(t, sessionSvc)
			require.Nil(t, memorySvc)
			require.Equal(t, tc.name, profile.Name)

			t.Setenv(tc.envKey, "configured")
			sessionSvc, memorySvc, profile, err = tc.factory()
			require.ErrorIs(t, err, ErrBackendNotConfigured)
			require.Nil(t, sessionSvc)
			require.Nil(t, memorySvc)
			require.Equal(t, tc.name, profile.Name)
		})
	}
}

func TestRedisFactoryReportsAdapterLinkage(t *testing.T) {
	factory := RedisFactory()
	t.Setenv("REDIS_ADDR", "")
	sessionSvc, memorySvc, profile, err := factory()
	require.ErrorIs(t, err, ErrBackendNotConfigured)
	require.Nil(t, sessionSvc)
	require.Nil(t, memorySvc)
	require.Equal(t, "redis", profile.Name)

	t.Setenv("REDIS_ADDR", "redis://127.0.0.1:6379")
	sessionSvc, memorySvc, profile, err = factory()
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrBackendNotConfigured))
	require.Contains(t, err.Error(), "session/replaytest/redis")
	require.Contains(t, err.Error(), "REDIS_ADDR")
	require.Nil(t, sessionSvc)
	require.Nil(t, memorySvc)
	require.Equal(t, "redis", profile.Name)
}
