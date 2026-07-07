//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gorm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	gormio "gorm.io/gorm"
)

func TestRegisterGormInstance(t *testing.T) {
	instanceName := "test-instance"
	RegisterGormInstance(instanceName, WithDialector(sqlite.Open(":memory:")))

	opts, ok := GetGormInstance(instanceName)
	require.True(t, ok, "expected instance %s to be registered", instanceName)
	assert.NotEmpty(t, opts)
}

func TestRegisterGormInstance_Append(t *testing.T) {
	instanceName := "test-append"
	RegisterGormInstance(instanceName, WithDialector(sqlite.Open("file:one?mode=memory")))
	RegisterGormInstance(instanceName, WithInstanceName("one"))

	opts, ok := GetGormInstance(instanceName)
	require.True(t, ok)
	assert.Len(t, opts, 2)
}

func TestSetAndGetClientBuilder(t *testing.T) {
	originalBuilder := GetClientBuilder()
	defer SetClientBuilder(originalBuilder)

	customErr := errors.New("custom builder")
	SetClientBuilder(func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
		return nil, customErr
	})

	_, err := GetClientBuilder()(context.Background())
	assert.ErrorIs(t, err, customErr)
}

func TestDefaultClientBuilder_WithDB(t *testing.T) {
	ctx := context.Background()
	db, err := gormio.Open(sqlite.Open(":memory:"), &gormio.Config{})
	require.NoError(t, err)

	client, err := GetClientBuilder()(ctx, WithDB(db))
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Same(t, db, client.DB())

	require.NoError(t, client.Close())

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.PingContext(ctx), "injected db must remain open after client close")
}

func TestDefaultClientBuilder_WithDialector(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "storage_gorm_test.db")

	client, err := GetClientBuilder()(ctx, WithDialector(sqlite.Open(path)))
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, client.DB())

	require.NoError(t, client.Close())

	_, err = GetClientBuilder()(ctx, WithDialector(sqlite.Open(path)))
	require.NoError(t, err)
}

func TestDefaultClientBuilder_MissingDialector(t *testing.T) {
	_, err := GetClientBuilder()(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dialector is required")
}

func TestGetGormInstance_ReturnsCopy(t *testing.T) {
	instanceName := "test-copy"
	RegisterGormInstance(instanceName, WithDialector(sqlite.Open(":memory:")))

	opts, ok := GetGormInstance(instanceName)
	require.True(t, ok)
	require.Len(t, opts, 1)

	opts = append(opts, WithInstanceName("mutated"))

	stored, ok := GetGormInstance(instanceName)
	require.True(t, ok)
	assert.Len(t, stored, 1, "caller mutations must not affect registry state")
}

func TestRegisterGormInstance_Concurrent(t *testing.T) {
	const workers = 16
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			name := fmt.Sprintf("concurrent-%d", n%4)
			RegisterGormInstance(name, WithInstanceName(name))
			_, _ = GetGormInstance(name)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < workers; i++ {
		<-done
	}
}

func TestClientBuilderOpts_WithExtraOptions(t *testing.T) {
	opts := &ClientBuilderOpts{}
	WithExtraOptions("alpha", 42)(opts)
	assert.Equal(t, []any{"alpha", 42}, opts.ExtraOptions)
}
