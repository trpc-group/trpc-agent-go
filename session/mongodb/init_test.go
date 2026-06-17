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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestEnsureIndexes_CreatesPartialUniqueIndexesOnAllThreeCollections(t *testing.T) {
	type indexCall struct {
		coll   string
		models []mongo.IndexModel
	}
	var calls []indexCall

	mc := &mockClient{
		ensureIndexesFn: func(models []mongo.IndexModel) ([]string, error) {
			// mockClient records the coll via mockOp; we re-collect by length
			// here to cross-check downstream.
			return make([]string, len(models)), nil
		},
	}
	s := newServiceForTest(t, mc)
	require.NoError(t, s.ensureIndexes(context.Background()))

	for _, op := range mc.recorded() {
		if op.name == "EnsureIndexes" {
			calls = append(calls, indexCall{coll: op.coll})
		}
	}
	require.Len(t, calls, 3)
	assert.Equal(t, "session_states", calls[0].coll)
	assert.Equal(t, "app_states", calls[1].coll)
	assert.Equal(t, "user_states", calls[2].coll)
}

func TestEnsureIndexes_PartialFilterIsDeletedAtAbsent(t *testing.T) {
	var capturedModels [][]mongo.IndexModel
	mc := &mockClient{
		ensureIndexesFn: func(models []mongo.IndexModel) ([]string, error) {
			cp := make([]mongo.IndexModel, len(models))
			copy(cp, models)
			capturedModels = append(capturedModels, cp)
			return make([]string, len(models)), nil
		},
	}
	s := newServiceForTest(t, mc)
	require.NoError(t, s.ensureIndexes(context.Background()))
	require.Len(t, capturedModels, 3)

	for _, ms := range capturedModels {
		require.Len(t, ms, 1)
		opts := ms[0].Options
		require.NotNil(t, opts)
		require.NotNil(t, opts.Unique)
		assert.True(t, *opts.Unique)
		require.NotNil(t, opts.PartialFilterExpression)
		// Partial filter expression should be `{ deleted_at: { $exists: false } }`.
		expr, ok := opts.PartialFilterExpression.(bson.M)
		require.True(t, ok)
		inner, ok := expr["deleted_at"].(bson.M)
		require.True(t, ok)
		assert.Equal(t, false, inner["$exists"])
	}
}
