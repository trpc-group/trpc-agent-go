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
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func originalCleanupPipeline(cutoff time.Time) bson.A {
	return bson.A{
		bson.M{"$match": bson.M{"deleted_at": nil}},
		bson.M{"$group": bson.M{"_id": bson.M{"app_name": "$app_name", "user_id": "$user_id", "session_id": "$session_id"}, "max_updated_at": bson.M{"$max": "$updated_at"}}},
		bson.M{"$match": bson.M{"max_updated_at": bson.M{"$lte": cutoff}}},
	}
}

func TestIntegrationCleanupPreservesGroups(t *testing.T) {
	for _, soft := range []bool{true, false} {
		for _, indexed := range []bool{true, false} {
			t.Run(fmt.Sprintf("soft=%t/indexed=%t", soft, indexed), func(t *testing.T) {
				ctx, svc, db := newIntegrationService(t, WithSoftDelete(soft))
				now := time.Now().UTC().Truncate(time.Millisecond)
				cutoff := now.Add(-time.Hour)
				for _, name := range []string{svc.collSessionEvents, svc.collSessionTracks} {
					coll := db.Collection(name)
					index := "idx_" + name + "_cleanup"
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
						{"", "u", "legacy-empty-app", 2 * time.Hour, false},
						{"中文", "u", "unicode-app", 2 * time.Hour, false},
						{" ", "u", "space-app", 2 * time.Hour, false},
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
					require.Len(t, expired, 6)
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

func TestIntegrationCleanupSkipsSoftDeletedHistory(t *testing.T) {
	ctx, svc, db := newIntegrationService(t, WithSoftDelete(false))
	coll := db.Collection(svc.collSessionTracks)
	now := time.Now().UTC().Truncate(time.Millisecond)
	fixture := cleanupFixture{name: "mostly_deleted", groups: 100, rows: 200, oldRows: 1, deleted: 200000}
	seedCleanupFixture(t, ctx, coll, now, fixture)
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
	require.EqualValues(t, fixture.groups*fixture.rows, stats[0].Keys)
	require.EqualValues(t, 1, stats[0].Operations, "identify groups with a single aggregation")
	t.Logf("original docs=%d keys=%d duration=%s; indexed aggregation docs=%d keys=%d operations=%d duration=%s", before.Docs, before.Keys, beforeTime, stats[0].Docs, stats[0].Keys, stats[0].Operations, afterTime)
	count, err := coll.CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	require.EqualValues(t, fixture.deleted+fixture.groups*fixture.rows, count)
}

// delayedCleanupClient adds a cancellable round-trip delay to cleanup calls.
type delayedCleanupClient struct {
	mongoClient
	delay time.Duration
	calls atomic.Int64
}

func (c *delayedCleanupClient) wait(ctx context.Context) error {
	c.calls.Add(1)
	timer := time.NewTimer(c.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *delayedCleanupClient) Aggregate(ctx context.Context, db, coll string, pipeline any,
	opts ...*options.AggregateOptions) (*mongo.Cursor, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.mongoClient.Aggregate(ctx, db, coll, pipeline, opts...)
}

func (c *delayedCleanupClient) FindOne(ctx context.Context, db, coll string, filter any,
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	if err := c.wait(ctx); err != nil {
		return mongo.NewSingleResultFromDocument(bson.M{}, err, nil)
	}
	return c.mongoClient.FindOne(ctx, db, coll, filter, opts...)
}

func (c *delayedCleanupClient) DeleteMany(ctx context.Context, db, coll string, filter any,
	opts ...*options.DeleteOptions) (*mongo.DeleteResult, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.mongoClient.DeleteMany(ctx, db, coll, filter, opts...)
}

func TestIntegrationCleanupProgressWithManyActiveCandidates(t *testing.T) {
	ctx, svc, db := newIntegrationService(t, WithSoftDelete(false))
	// Enable TTL after initialization to avoid starting a background worker.
	svc.opts.sessionTTL = time.Hour
	now := time.Now().UTC().Truncate(time.Millisecond)
	const activeGroups = 3200
	docs := make([]any, 0, 2*activeGroups+1)
	for i := 0; i < activeGroups; i++ {
		for _, updated := range []time.Time{now.Add(-2 * time.Hour), now} {
			docs = append(docs, bson.M{"app_name": "app", "user_id": "user",
				"session_id": fmt.Sprint(i), "updated_at": updated})
		}
	}
	expired := bson.M{"app_name": "app", "user_id": "user",
		"session_id": "expired", "updated_at": now.Add(-2 * time.Hour)}
	docs = append(docs, expired)
	_, err := db.Collection(svc.collSessionEvents).InsertMany(ctx, docs)
	require.NoError(t, err)
	_, err = db.Collection(svc.collSessionTracks).InsertOne(ctx, expired)
	require.NoError(t, err)
	delayed := &delayedCleanupClient{mongoClient: svc.client, delay: 10 * time.Millisecond}
	svc.client = delayed
	started := time.Now()
	svc.cleanupExpired(ctx)
	t.Logf("cleanup of %d active candidates completed in %s with %d database calls",
		activeGroups, time.Since(started), delayed.calls.Load())
	for _, name := range []string{svc.collSessionEvents, svc.collSessionTracks} {
		count, err := db.Collection(name).CountDocuments(ctx, bson.M{"session_id": "expired"})
		require.NoError(t, err)
		require.Zero(t, count, "expired group must be cleaned from %s", name)
	}
	count, err := db.Collection(svc.collSessionEvents).CountDocuments(ctx, bson.M{})
	require.NoError(t, err)
	require.EqualValues(t, 2*activeGroups, count, "active histories must remain intact")
	require.EqualValues(t, 4, delayed.calls.Load(), "each collection needs one aggregate and one delete, independent of active session count")
}
