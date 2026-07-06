//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gormmemory

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapDBErr(t *testing.T) {
	assert.NoError(t, wrapDBErr("noop", nil))

	err := wrapDBErr("list memories", errors.New("connection reset"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gorm memory service list memories failed")
	assert.Contains(t, err.Error(), "connection reset")
}

func TestService_memoryTable(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc, err := NewService(db)
	require.NoError(t, err)
	defer svc.Close()
	assert.NotNil(t, svc.memoryTable(ctx))
}

func TestService_initDB_failure(t *testing.T) {
	db := testDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = NewService(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init database failed")
}
