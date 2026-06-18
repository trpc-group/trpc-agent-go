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

// captureIndexes runs ensureIndexes against a recording mockClient and
// returns the index models keyed by collection name, preserving the call
// order on the side. It is the single setup helper used by every init test.
func captureIndexes(t *testing.T) (order []string, models map[string][]mongo.IndexModel) {
	t.Helper()
	models = map[string][]mongo.IndexModel{}

	mc := &mockClient{}
	mc.ensureIndexesFn = func(in []mongo.IndexModel) ([]string, error) {
		// The most recent recorded op is this very EnsureIndexes call, so
		// its coll is correct.
		ops := mc.recorded()
		coll := ops[len(ops)-1].coll
		order = append(order, coll)
		cp := make([]mongo.IndexModel, len(in))
		copy(cp, in)
		models[coll] = cp
		return make([]string, len(in)), nil
	}

	s := newServiceForTest(t, mc)
	require.NoError(t, s.ensureIndexes(context.Background()))
	return order, models
}

func TestEnsureIndexes_CoversAllFiveCollectionsInOrder(t *testing.T) {
	order, _ := captureIndexes(t)
	assert.Equal(t, []string{
		"session_states",
		"session_events",
		"session_summaries",
		"app_states",
		"user_states",
	}, order)
}

func TestEnsureIndexes_AllIndexesFilterOnDeletedAtAbsent(t *testing.T) {
	_, models := captureIndexes(t)
	for coll, ms := range models {
		require.NotEmpty(t, ms, "no indexes on %s", coll)
		for _, m := range ms {
			require.NotNil(t, m.Options, "%s: nil Options", coll)
			require.NotNil(t, m.Options.PartialFilterExpression, "%s: nil PartialFilterExpression", coll)
			expr, ok := m.Options.PartialFilterExpression.(bson.M)
			require.True(t, ok, "%s: PartialFilterExpression not a bson.M", coll)
			inner, ok := expr["deleted_at"].(bson.M)
			require.True(t, ok, "%s: missing deleted_at clause", coll)
			assert.Equal(t, false, inner["$exists"], "%s: $exists value", coll)
		}
	}
}

func TestEnsureIndexes_UniqueOnlyOnPrimaryKeys(t *testing.T) {
	// session_events is a lookup index (multiple rows per session); the
	// other four collections each have a unique key.
	uniqueByColl := map[string]bool{
		"session_states":    true,
		"session_events":    false,
		"session_summaries": true,
		"app_states":        true,
		"user_states":       true,
	}
	_, models := captureIndexes(t)
	for coll, want := range uniqueByColl {
		ms, ok := models[coll]
		require.True(t, ok, "missing models for %s", coll)
		require.NotEmpty(t, ms)
		got := ms[0].Options.Unique
		if want {
			require.NotNil(t, got, "expected unique on %s", coll)
			assert.True(t, *got, "expected unique on %s", coll)
		} else if got != nil {
			assert.False(t, *got, "did not expect unique on %s", coll)
		}
	}
}

func TestEnsureIndexes_SessionEventsIsLookupOnCreatedAt(t *testing.T) {
	_, models := captureIndexes(t)
	ms := models["session_events"]
	require.Len(t, ms, 1)
	keys := ms[0].Keys.(bson.D)
	require.Len(t, keys, 4)
	assert.Equal(t, "app_name", keys[0].Key)
	assert.Equal(t, "user_id", keys[1].Key)
	assert.Equal(t, "session_id", keys[2].Key)
	assert.Equal(t, "created_at", keys[3].Key)
}

func TestEnsureIndexes_SessionSummariesUniqueOnFilterKey(t *testing.T) {
	_, models := captureIndexes(t)
	ms := models["session_summaries"]
	require.Len(t, ms, 1)
	keys := ms[0].Keys.(bson.D)
	require.Len(t, keys, 4)
	assert.Equal(t, "app_name", keys[0].Key)
	assert.Equal(t, "user_id", keys[1].Key)
	assert.Equal(t, "session_id", keys[2].Key)
	assert.Equal(t, "filter_key", keys[3].Key)
}
