//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessionstate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestContextWithService(t *testing.T) {
	service := inmemory.NewSessionService()
	ctx := ContextWithService(context.Background(), service)

	got, ok := ServiceFromContext(ctx)
	require.True(t, ok)
	assert.Same(t, service, got)
}

func TestContextWithServiceMissing(t *testing.T) {
	_, ok := ServiceFromContext(nil)
	assert.False(t, ok)

	ctx := ContextWithService(context.Background(), nil)
	_, ok = ServiceFromContext(ctx)
	assert.False(t, ok)
}
