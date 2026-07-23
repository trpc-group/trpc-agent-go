//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gorm_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	gormio "gorm.io/gorm"

	storagegorm "trpc.group/trpc-go/trpc-agent-go/storage/gorm"
)

func TestWithOwnsConnection_ObservableFromExternalPackage(t *testing.T) {
	opts := storagegorm.ApplyClientBuilderOpts(storagegorm.WithOwnsConnection(false))

	assert.True(t, opts.OwnsConnectionSet)
	assert.False(t, opts.OwnsConnection)
	assert.False(t, opts.EffectiveOwnsConnection())
}

func TestCustomBuilder_RespectsWithOwnsConnectionFalse(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	original := storagegorm.GetClientBuilder()
	defer storagegorm.SetClientBuilder(original)

	storagegorm.SetClientBuilder(func(ctx context.Context, builderOpts ...storagegorm.ClientBuilderOpt) (storagegorm.Client, error) {
		opts := storagegorm.ApplyClientBuilderOpts(builderOpts...)
		require.True(t, opts.OwnsConnectionSet)
		require.False(t, opts.EffectiveOwnsConnection())

		db, err := gormio.Open(sqlite.Dialector{Conn: sqlDB}, &gormio.Config{})
		if err != nil {
			return nil, err
		}
		return storagegorm.NewClient(db, opts.EffectiveOwnsConnection()), nil
	})

	client, err := storagegorm.GetClientBuilder()(ctx,
		storagegorm.WithDialector(sqlite.Dialector{Conn: sqlDB}),
		storagegorm.WithOwnsConnection(false),
	)
	require.NoError(t, err)

	require.NoError(t, client.Close())
	require.NoError(t, sqlDB.PingContext(ctx), "borrowed pool must remain open")
}
