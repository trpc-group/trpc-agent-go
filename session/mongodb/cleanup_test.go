//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestCleanupCandidateRechecksRecentRows(t *testing.T) {
	now := time.Now()
	failure := errors.New("probe failed")
	for _, tc := range []struct {
		name     string
		probeErr error
		deletes  bool
	}{
		{"active", nil, false}, {"expired", mongo.ErrNoDocuments, true}, {"failure", failure, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := &mockClient{
				aggregateFn: func(pipeline any) (*mongo.Cursor, error) {
					stages := pipeline.(bson.A)
					require.Equal(t, bson.M{"$lte": now.Add(-time.Hour)}, stages[0].(bson.M)["$match"].(bson.M)["updated_at"])
					return docsCursor([]any{bson.M{"_id": bson.M{"app_name": "app", "user_id": "user", "session_id": "session"}}})
				},
				findOneFn: func(filter any) *mongo.SingleResult {
					require.Equal(t, bson.M{"app_name": "app", "user_id": "user", "session_id": "session", "deleted_at": nil, "updated_at": bson.M{"$gt": now.Add(-time.Hour)}}, filter)
					return mongo.NewSingleResultFromDocument(bson.M{"_id": 1}, tc.probeErr, nil)
				},
			}
			svc := newServiceForTest(t, mc, func(o *serviceOpts) { o.sessionTTL = time.Hour })
			err := svc.cleanupExpiredEvents(context.Background(), now)
			if tc.probeErr == failure {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			deletes := 0
			for _, op := range mc.recorded() {
				if op.name == "UpdateMany" || op.name == "DeleteMany" {
					deletes++
				}
			}
			if tc.deletes {
				require.Equal(t, 1, deletes)
			} else {
				require.Zero(t, deletes)
			}
		})
	}
}

func TestCleanupFlushesBeforeLaterProbeFailure(t *testing.T) {
	for _, soft := range []bool{true, false} {
		t.Run(fmt.Sprint(soft), func(t *testing.T) {
			failure := errors.New("later batch failed")
			docs := make([]any, cleanupBatchSize+1)
			for i := range docs {
				docs[i] = bson.M{"_id": bson.M{"app_name": "app", "user_id": "user", "session_id": fmt.Sprint(i)}}
			}
			mc := &mockClient{
				aggregateFn: func(any) (*mongo.Cursor, error) { return docsCursor(docs) },
				findOneFn: func(filter any) *mongo.SingleResult {
					var err error
					switch filter.(bson.M)["session_id"] {
					case "0":
						err = mongo.ErrNoDocuments
					case fmt.Sprint(cleanupBatchSize):
						err = failure
					}
					return mongo.NewSingleResultFromDocument(bson.M{}, err, nil)
				},
			}
			svc := newServiceForTest(t, mc, func(o *serviceOpts) {
				o.sessionTTL = time.Hour
				o.softDelete = soft
			})
			err := svc.cleanupExpiredEvents(context.Background(), time.Now())
			require.ErrorIs(t, err, failure)
			var deletions []mockOp
			for _, op := range mc.recorded() {
				if op.name == "UpdateMany" || op.name == "DeleteMany" {
					deletions = append(deletions, op)
				}
			}
			require.Len(t, deletions, 1, "confirmed expiration must be committed before probing the next batch")
			require.Equal(t, bson.A{bson.M{"app_name": "app", "user_id": "user", "session_id": "0"}},
				deletions[0].filter.(bson.M)["$or"])
		})
	}
}

type cleanupContextClient struct {
	mongoClient
	aggregate func(context.Context, string) (*mongo.Cursor, error)
	findOne   func(context.Context) *mongo.SingleResult
}

func (c *cleanupContextClient) Aggregate(ctx context.Context, db, coll string, pipeline any,
	opts ...*options.AggregateOptions) (*mongo.Cursor, error) {
	return c.aggregate(ctx, coll)
}

func (c *cleanupContextClient) FindOne(ctx context.Context, db, coll string, filter any,
	opts ...*options.FindOneOptions) *mongo.SingleResult {
	if c.findOne != nil {
		return c.findOne(ctx)
	}
	return c.mongoClient.FindOne(ctx, db, coll, filter, opts...)
}

func TestCleanupCollectionBudgets(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_parent=%t", cancelParent), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			mc := &mockClient{}
			svc := newServiceForTest(t, mc, func(o *serviceOpts) { o.sessionTTL = time.Hour })
			var eventsCtx context.Context
			sawTracks := false
			svc.client = &cleanupContextClient{mongoClient: mc,
				aggregate: func(callCtx context.Context, coll string) (*mongo.Cursor, error) {
					if coll == svc.collSessionEvents {
						eventsCtx = callCtx
						if cancelParent {
							cancel()
						}
						return nil, context.DeadlineExceeded
					}
					sawTracks = true
					require.ErrorIs(t, eventsCtx.Err(), context.Canceled)
					if cancelParent {
						require.ErrorIs(t, callCtx.Err(), context.Canceled)
						return nil, callCtx.Err()
					}
					require.NoError(t, callCtx.Err(), "event failure must not exhaust the track budget")
					deadline, ok := callCtx.Deadline()
					require.True(t, ok)
					require.InDelta(t, 30, time.Until(deadline).Seconds(), 1)
					return docsCursor([]any{bson.M{"_id": bson.M{"app_name": "app", "user_id": "user", "session_id": "expired"}}})
				},
			}
			svc.cleanupExpired(ctx)
			require.True(t, sawTracks)
			if cancelParent {
				require.Empty(t, mc.recorded())
			} else {
				ops := mc.recorded()
				require.Len(t, ops, 2)
				require.Equal(t, "UpdateMany", ops[1].name)
				require.Equal(t, svc.collSessionTracks, ops[1].coll)
			}
		})
	}
}

func TestCleanupProbeCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mc := &mockClient{}
	svc := newServiceForTest(t, mc)
	started := make(chan struct{}, cleanupProbeConcurrency+1)
	var running atomic.Int64
	svc.client = &cleanupContextClient{mongoClient: mc,
		findOne: func(callCtx context.Context) *mongo.SingleResult {
			running.Add(1)
			defer running.Add(-1)
			started <- struct{}{}
			<-callCtx.Done()
			return mongo.NewSingleResultFromDocument(bson.M{}, callCtx.Err(), nil)
		},
	}
	candidates := make(bson.A, cleanupProbeConcurrency+1)
	for i := range candidates {
		candidates[i] = bson.M{"app_name": "app", "user_id": "user", "session_id": fmt.Sprint(i)}
	}
	result := make(chan error, 1)
	go func() {
		_, err := svc.expiredCleanupCandidates(ctx, svc.collSessionEvents, time.Now(), candidates)
		result <- err
	}()
	for i := 0; i < cleanupProbeConcurrency; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("probes did not start concurrently")
		}
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not stop after cancellation")
	}
	require.Zero(t, running.Load(), "all probe workers must exit before returning")
	require.Empty(t, started, "no additional probe may start after cancellation")
}
