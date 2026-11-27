//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package mongodb

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestIntegrationOrderedIndexUpgrade(t *testing.T) {
	ctx, svc, db := newIntegrationService(t, WithCollectionPrefix("upgrade"))
	coll := db.Collection(svc.collSessionEvents)
	const ordered = "idx_upgrade_session_events_ordered_lookup"
	const legacy = "idx_upgrade_session_events_lookup"
	// Start with the indexes created by the previous release.
	_, err := coll.Indexes().DropOne(ctx, ordered)
	require.NoError(t, err)

	// Uniqueness and active-document membership must not change on upgrade.
	base := time.Now().UTC().Truncate(time.Millisecond)
	docs := make([]any, 0, 5001)
	for i := 0; i < 5000; i++ {
		docs = append(docs, bson.M{"app_name": "app", "user_id": "user", "session_id": "session", "created_at": base, "event": []byte(`{}`)})
	}
	docs = append(docs, bson.M{"app_name": "app", "user_id": "user", "session_id": "session", "created_at": base, "deleted_at": base})
	_, err = coll.InsertMany(ctx, docs)
	require.NoError(t, err)
	require.NoError(t, svc.ensureIndexes(ctx))
	require.NoError(t, svc.ensureIndexes(ctx))
	assertIndexNames(t, ctx, db, svc.collSessionEvents, legacy, ordered)
	filter := bson.M{"app_name": "app", "user_id": "user", "session_id": bson.M{"$in": bson.A{"session"}}, "deleted_at": nil}
	for _, direction := range []int{1, -1} {
		sort := bson.D{{Key: "session_id", Value: direction}, {Key: "created_at", Value: direction}, {Key: "_id", Value: direction}}
		for _, index := range []string{legacy, ordered, ""} {
			cmd := bson.D{{Key: "explain", Value: bson.D{{Key: "find", Value: coll.Name()}, {Key: "filter", Value: filter}, {Key: "sort", Value: sort}, {Key: "limit", Value: 100}}}, {Key: "verbosity", Value: "executionStats"}}
			if index != "" {
				cmd[0].Value = append(cmd[0].Value.(bson.D), bson.E{Key: "hint", Value: index})
			}
			var result struct {
				QueryPlanner   bson.M `bson:"queryPlanner"`
				ExecutionStats struct {
					Returned int64 `bson:"nReturned"`
					Docs     int64 `bson:"totalDocsExamined"`
					Keys     int64 `bson:"totalKeysExamined"`
				} `bson:"executionStats"`
			}
			require.NoError(t, db.RunCommand(ctx, cmd).Decode(&result))
			plan, err := json.Marshal(result.QueryPlanner["winningPlan"])
			require.NoError(t, err)
			require.EqualValues(t, 100, result.ExecutionStats.Returned)
			if index == legacy {
				require.Contains(t, string(plan), `"stage":"SORT"`)
				require.EqualValues(t, 5000, result.ExecutionStats.Docs)
			} else {
				require.NotContains(t, string(plan), `"stage":"SORT"`)
				require.LessOrEqual(t, result.ExecutionStats.Docs, int64(100))
			}
			t.Logf("direction=%d index=%q docs=%d keys=%d returned=%d", direction, index, result.ExecutionStats.Docs, result.ExecutionStats.Keys, result.ExecutionStats.Returned)
		}
		cursor, err := coll.Find(ctx, filter, options.Find().SetSort(sort).SetHint(ordered))
		require.NoError(t, err)
		var rows []bson.M
		require.NoError(t, cursor.All(ctx, &rows))
		require.Len(t, rows, 5000)
	}
}
