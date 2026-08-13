//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package eventstream

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithConsumer(t *testing.T) {
	type contextKey struct{}
	ctx, consumerDone := WithConsumer(
		context.WithValue(context.Background(), contextKey{}, "value"),
	)
	done := ConsumerDone(ctx)
	require.NotNil(t, done)
	assert.Equal(t, "value", ctx.Value(contextKey{}))
	select {
	case <-done:
		assert.Fail(t, "consumer lifetime ended early")
	default:
	}

	consumerDone()
	consumerDone()
	select {
	case <-done:
	default:
		assert.Fail(t, "consumer lifetime did not end")
	}
}

func TestConsumerDoneAbsent(t *testing.T) {
	assert.Nil(t, ConsumerDone(context.Background()))
	assert.Nil(t, ConsumerDone(nil))
}
