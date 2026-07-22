//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rreplaytest "trpc.group/trpc-go/trpc-agent-go/session/replaytest/redis"
)

// TestNewTargetInvalidURL covers the session-service construction error
// path of NewTarget: an unparsable Redis URL fails Reset.
func TestNewTargetInvalidURL(t *testing.T) {
	tgt, err := rreplaytest.NewTarget("bad", "://not-a-redis-url")
	require.Error(t, err)
	assert.Nil(t, tgt)
	assert.ErrorContains(t, err, "create redis session service")
}

// TestTargetResetAndAccessors covers repeated Reset (prefix rotation) and
// the plain accessors against a miniredis server.
func TestTargetResetAndAccessors(t *testing.T) {
	mr := miniredis.RunT(t)
	tgt, err := rreplaytest.NewTarget("redis", "redis://"+mr.Addr())
	require.NoError(t, err)
	defer tgt.Close()

	assert.Equal(t, "redis", tgt.Name())
	assert.NotNil(t, tgt.SessionService())
	assert.NotNil(t, tgt.MemoryService())

	// A second Reset closes and recreates both services under a new prefix.
	require.NoError(t, tgt.Reset(context.Background()))
	assert.NotNil(t, tgt.SessionService())
	assert.NotNil(t, tgt.MemoryService())
}
