//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/hook"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestServiceOpts_Defaults(t *testing.T) {
	o := defaultOptions
	assert.True(t, o.softDelete)
	assert.Equal(t, defaultAsyncPersisterNum, o.asyncPersisterNum)
	assert.False(t, o.enableAsyncPersist)
	assert.Equal(t, defaultAsyncSummaryNum, o.asyncSummaryNum)
	assert.Equal(t, defaultSummaryQueueSize, o.summaryQueueSize)
	assert.Equal(t, defaultSummaryJobTimeout, o.summaryJobTimeout)
}

func TestWithMongoClientURI(t *testing.T) {
	o := defaultOptions
	WithMongoClientURI("mongodb://h:27017")(&o)
	assert.Equal(t, "mongodb://h:27017", o.uri)
}

func TestWithMongoInstance(t *testing.T) {
	o := defaultOptions
	WithMongoInstance("inst")(&o)
	assert.Equal(t, "inst", o.instanceName)
}

func TestWithDatabase(t *testing.T) {
	o := defaultOptions
	WithDatabase("mydb")(&o)
	assert.Equal(t, "mydb", o.database)
}

func TestWithSessionEventLimit(t *testing.T) {
	o := defaultOptions
	WithSessionEventLimit(12)(&o)
	assert.Equal(t, 12, o.sessionEventLimit)
}

func TestWithExtraOptions_Appends(t *testing.T) {
	o := defaultOptions
	WithExtraOptions("a")(&o)
	WithExtraOptions("b", "c")(&o)
	assert.Equal(t, []any{"a", "b", "c"}, o.extraOptions)
}

func TestTTLOptions(t *testing.T) {
	o := defaultOptions
	WithSessionTTL(time.Hour)(&o)
	WithAppStateTTL(2 * time.Hour)(&o)
	WithUserStateTTL(3 * time.Hour)(&o)
	assert.Equal(t, time.Hour, o.sessionTTL)
	assert.Equal(t, 2*time.Hour, o.appStateTTL)
	assert.Equal(t, 3*time.Hour, o.userStateTTL)
}

func TestWithEnableAsyncPersist(t *testing.T) {
	o := defaultOptions
	WithEnableAsyncPersist(true)(&o)
	assert.True(t, o.enableAsyncPersist)
}

func TestWithAsyncPersisterNum(t *testing.T) {
	o := defaultOptions
	WithAsyncPersisterNum(3)(&o)
	assert.Equal(t, 3, o.asyncPersisterNum)

	WithAsyncPersisterNum(0)(&o)
	assert.Equal(t, defaultAsyncPersisterNum, o.asyncPersisterNum)

	WithAsyncPersisterNum(-1)(&o)
	assert.Equal(t, defaultAsyncPersisterNum, o.asyncPersisterNum)
}

func TestWithCleanupInterval(t *testing.T) {
	o := defaultOptions
	WithCleanupInterval(time.Minute)(&o)
	assert.Equal(t, time.Minute, o.cleanupInterval)
}

func TestWithAsyncSummaryNum(t *testing.T) {
	o := defaultOptions
	WithAsyncSummaryNum(2)(&o)
	assert.Equal(t, 2, o.asyncSummaryNum)

	WithAsyncSummaryNum(0)(&o)
	assert.Equal(t, defaultAsyncSummaryNum, o.asyncSummaryNum)
}

func TestWithSummaryQueueSize(t *testing.T) {
	o := defaultOptions
	WithSummaryQueueSize(8)(&o)
	assert.Equal(t, 8, o.summaryQueueSize)

	WithSummaryQueueSize(0)(&o)
	assert.Equal(t, defaultSummaryQueueSize, o.summaryQueueSize)
}

func TestWithSummaryJobTimeout(t *testing.T) {
	o := defaultOptions
	WithSummaryJobTimeout(2 * time.Second)(&o)
	assert.Equal(t, 2*time.Second, o.summaryJobTimeout)

	WithSummaryJobTimeout(0)(&o)
	assert.Equal(t, 2*time.Second, o.summaryJobTimeout)
}

func TestWithSoftDelete(t *testing.T) {
	o := defaultOptions
	WithSoftDelete(false)(&o)
	assert.False(t, o.softDelete)
}

func TestWithSkipDBInit(t *testing.T) {
	o := defaultOptions
	WithSkipDBInit(true)(&o)
	assert.True(t, o.skipDBInit)
}

func TestWithCollectionPrefix_AddsTrailingUnderscore(t *testing.T) {
	o := defaultOptions
	WithCollectionPrefix("trpc")(&o)
	assert.Equal(t, "trpc_", o.collectionPrefix)
}

func TestWithCollectionPrefix_KeepsTrailingUnderscore(t *testing.T) {
	o := defaultOptions
	WithCollectionPrefix("trpc_")(&o)
	assert.Equal(t, "trpc_", o.collectionPrefix)
}

func TestWithCollectionPrefix_EmptyKeepsEmpty(t *testing.T) {
	o := defaultOptions
	WithCollectionPrefix("")(&o)
	assert.Equal(t, "", o.collectionPrefix)
}

func TestWithCollectionPrefix_RejectsInvalid(t *testing.T) {
	defer func() {
		require.NotNil(t, recover(), "invalid prefix should panic")
	}()
	o := defaultOptions
	WithCollectionPrefix("bad-prefix!")(&o)
}

func TestWithGetSessionHook_AppendsInOrder(t *testing.T) {
	o := defaultOptions
	var calls []string
	h1 := session.GetSessionHook(func(_ *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		calls = append(calls, "first")
		return next()
	})
	h2 := session.GetSessionHook(func(_ *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		calls = append(calls, "second")
		return next()
	})
	WithGetSessionHook(h1, h2)(&o)
	assert.Len(t, o.getSessionHooks, 2)

	_, err := hook.RunGetSessionHooks(o.getSessionHooks,
		&session.GetSessionContext{Context: context.Background()},
		func(_ *session.GetSessionContext, _ func() (*session.Session, error)) (*session.Session, error) {
			calls = append(calls, "final")
			return &session.Session{}, nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second", "final"}, calls)
}

func TestWithAppendEventHook_AppendsInOrder(t *testing.T) {
	o := defaultOptions
	var calls []string
	h1 := session.AppendEventHook(func(_ *session.AppendEventContext, next func() error) error {
		calls = append(calls, "first")
		return next()
	})
	h2 := session.AppendEventHook(func(_ *session.AppendEventContext, next func() error) error {
		calls = append(calls, "second")
		return next()
	})
	WithAppendEventHook(h1, h2)(&o)
	assert.Len(t, o.appendEventHooks, 2)

	err := hook.RunAppendEventHooks(o.appendEventHooks,
		&session.AppendEventContext{Context: context.Background()},
		func(_ *session.AppendEventContext, _ func() error) error {
			calls = append(calls, "final")
			return nil
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second", "final"}, calls)
}

func TestWithSummaryFilterAllowlist(t *testing.T) {
	o := defaultOptions
	WithSummaryFilterAllowlist("a", "b")(&o)
	assert.Equal(t, []string{"a", "b"}, o.summaryFilterAllowlist)
}

func TestWithCascadeFullSessionSummary(t *testing.T) {
	o := defaultOptions
	assert.True(t, o.shouldCascadeFullSessionSummary())

	WithCascadeFullSessionSummary(false)(&o)
	require.NotNil(t, o.cascadeFullSessionSummary)
	assert.False(t, o.shouldCascadeFullSessionSummary())
}
