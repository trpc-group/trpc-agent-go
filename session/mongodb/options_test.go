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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestServiceOpts_Defaults(t *testing.T) {
	o := defaultOptions
	assert.True(t, o.softDelete)
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
	h := session.GetSessionHook(func(_ *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
		return next()
	})
	WithGetSessionHook(h, h)(&o)
	assert.Len(t, o.getSessionHooks, 2)
}
