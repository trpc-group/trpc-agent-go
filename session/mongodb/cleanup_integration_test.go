//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package mongodb

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func originalCleanupPipeline(cutoff time.Time) bson.A {
	return bson.A{
		bson.M{"$match": bson.M{"deleted_at": nil}},
		bson.M{"$group": bson.M{"_id": bson.M{"app_name": "$app_name", "user_id": "$user_id", "session_id": "$session_id"}, "max_updated_at": bson.M{"$max": "$updated_at"}}},
		bson.M{"$match": bson.M{"max_updated_at": bson.M{"$lte": cutoff}}},
	}
}

func TestIntegrationCleanupCandidatesPreserveGroups(t *testing.T) {
	for _, soft := range []bool{true, false} {
		for _, indexed := range []bool{true, false} {
			t.Run(fmt.Sprintf("soft=%t/indexed=%t", soft, indexed), func(t *testing.T) {
				ctx, svc, db := newIntegrationService(t, WithSoftDelete(soft))
				now := time.Now().UTC().Truncate(time.Millisecond)
				cutoff := now.Add(-time.Hour)
				for _, name := range []string{svc.collSessionEvents, svc.collSessionTracks} {
					coll := db.Collection(name)
					index := "idx_" + name + "_cleanup_candidates"
					_, err := coll.Indexes().DropOne(ctx, index)
					require.NoError(t, err)
					specs := []struct {
						app, user, id string
						age           time.Duration
						deleted       bool
					}{
						{"app", "u", "expired", 2 * time.Hour, false}, {"app", "u", "expired", 3 * time.Hour, false},
						{"app", "u", "boundary", time.Hour, false},
						{"app", "u", "active", 2 * time.Hour, false}, {"app", "u", "active", 0, false},
						{"app", "u", "only-recent", 0, false},
						{"app", "u", "deleted-recent", 2 * time.Hour, false}, {"app", "u", "deleted-recent", 0, true},
						{"app", "u", "deleted-old", 2 * time.Hour, true},
						{"other-app", "u", "expired", 0, false}, {"app", "other-user", "expired", 0, false},
					}
					var docs []any
					for i, sp := range specs {
						row := bson.M{"app_name": sp.app, "user_id": sp.user, "session_id": sp.id, "updated_at": now.Add(-sp.age), "track": fmt.Sprint(i % 2)}
						if i%2 == 0 {
							row["deleted_at"] = nil
						}
						if sp.deleted {
							row["deleted_at"] = now
						}
						docs = append(docs, row)
					}
					_, err = coll.InsertMany(ctx, docs)
					require.NoError(t, err)
					if indexed {
						require.NoError(t, svc.ensureIndexes(ctx))
						require.NoError(t, svc.ensureIndexes(ctx))
					}
					cursor, err := coll.Aggregate(ctx, originalCleanupPipeline(cutoff))
					require.NoError(t, err)
					var expired []struct {
						ID bson.M `bson:"_id"`
					}
					require.NoError(t, cursor.All(ctx, &expired))
					require.Len(t, expired, 3)
					require.NoError(t, svc.cleanupExpiredCollection(ctx, now, time.Hour, name, "history"))
					for _, sp := range specs {
						filter := bson.M{"app_name": sp.app, "user_id": sp.user, "session_id": sp.id}
						expiredGroup := false
						for _, group := range expired {
							if group.ID["app_name"] == sp.app && group.ID["user_id"] == sp.user && group.ID["session_id"] == sp.id {
								expiredGroup = true
							}
						}
						filter["deleted_at"] = nil
						count, err := coll.CountDocuments(ctx, filter)
						require.NoError(t, err)
						var activeBefore, totalBefore int64
						for _, original := range specs {
							if original.app == sp.app && original.user == sp.user && original.id == sp.id {
								totalBefore++
								if !original.deleted {
									activeBefore++
								}
							}
						}
						wantActive, wantTotal := activeBefore, totalBefore
						if expiredGroup {
							wantActive = 0
							if !soft {
								wantTotal -= activeBefore
							}
						}
						require.Equal(t, wantActive, count, "group must retain all or none of its active rows")
						delete(filter, "deleted_at")
						count, err = coll.CountDocuments(ctx, filter)
						require.NoError(t, err)
						require.Equal(t, wantTotal, count, "soft deletion must retain documents")
					}
					// Previously deleted rows are left alone in either deletion mode.
					count, err := coll.CountDocuments(ctx, bson.M{"session_id": "deleted-old"})
					require.NoError(t, err)
					require.EqualValues(t, 1, count)
				}
			})
		}
	}
}

func TestIntegrationCleanupCandidateScanCost(t *testing.T) {
	ctx, svc, db := newIntegrationService(t, WithSoftDelete(false))
	coll := db.Collection(svc.collSessionTracks)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for batch := 0; batch < 40; batch++ {
		docs := make([]any, 0, 1000)
		for j := 0; j < 1000; j++ {
			i := batch*1000 + j
			updated := now
			if i < 500 {
				updated = now.Add(-2 * time.Hour)
			}
			docs = append(docs, bson.M{"app_name": "app", "user_id": "user", "session_id": fmt.Sprint(i % 100), "updated_at": updated, "event": make([]byte, 1024)})
		}
		_, err := coll.InsertMany(ctx, docs)
		require.NoError(t, err)
	}
	require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "profile", Value: 2}}).Err())
	defer db.RunCommand(ctx, bson.D{{Key: "profile", Value: 0}})
	started := time.Now()
	cur, err := coll.Aggregate(ctx, originalCleanupPipeline(now.Add(-time.Hour)))
	require.NoError(t, err)
	var original []bson.M
	require.NoError(t, cur.All(ctx, &original))
	require.Empty(t, original)
	beforeTime := time.Since(started)
	var before struct {
		Docs int64 `bson:"docsExamined"`
		Keys int64 `bson:"keysExamined"`
	}
	require.NoError(t, db.Collection("system.profile").FindOne(ctx, bson.M{"ns": db.Name() + "." + coll.Name(), "command.aggregate": coll.Name()}, options.FindOne().SetSort(bson.D{{Key: "ts", Value: -1}})).Decode(&before))
	require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "profile", Value: 0}}).Err())
	require.NoError(t, db.Collection("system.profile").Drop(ctx))
	require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "profile", Value: 2}}).Err())
	started = time.Now()
	require.NoError(t, svc.cleanupExpiredCollection(ctx, now, time.Hour, svc.collSessionTracks, "tracks"))
	afterTime := time.Since(started)
	cursor, err := db.Collection("system.profile").Aggregate(ctx, bson.A{
		bson.M{"$match": bson.M{"ns": db.Name() + "." + coll.Name(), "op": bson.M{"$in": bson.A{"query", "command", "getmore"}}}},
		bson.M{"$group": bson.M{"_id": nil, "docs": bson.M{"$sum": "$docsExamined"}, "keys": bson.M{"$sum": "$keysExamined"}, "operations": bson.M{"$sum": 1}}},
	})
	require.NoError(t, err)
	var stats []struct {
		Docs       int64 `bson:"docs"`
		Keys       int64 `bson:"keys"`
		Operations int64 `bson:"operations"`
	}
	require.NoError(t, cursor.All(ctx, &stats))
	require.Len(t, stats, 1)
	require.Less(t, stats[0].Docs, before.Docs)
	require.LessOrEqual(t, stats[0].Keys, int64(1000))
	t.Logf("original docs=%d keys=%d duration=%s; candidates docs=%d keys=%d operations=%d duration=%s", before.Docs, before.Keys, beforeTime, stats[0].Docs, stats[0].Keys, stats[0].Operations, afterTime)
	count, err := coll.CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	require.EqualValues(t, 40000, count)
}
