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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestCleanupFlushesBeforeLaterDecodeFailure(t *testing.T) {
	for _, soft := range []bool{true, false} {
		t.Run(fmt.Sprint(soft), func(t *testing.T) {
			docs := make([]any, cleanupBatchSize+1)
			for i := 0; i < cleanupBatchSize; i++ {
				docs[i] = bson.M{"_id": bson.M{"app_name": "app", "user_id": "user", "session_id": fmt.Sprint(i)}}
			}
			docs[cleanupBatchSize] = bson.M{"_id": "invalid group"}
			mc := &mockClient{aggregateFn: func(any) (*mongo.Cursor, error) { return docsCursor(docs) }}
			svc := newServiceForTest(t, mc, func(o *serviceOpts) {
				o.sessionTTL = time.Hour
				o.softDelete = soft
			})
			err := svc.cleanupExpiredEvents(context.Background(), time.Now())
			require.ErrorContains(t, err, "decode aggregate row")
			ops := mc.recorded()
			require.Len(t, ops, 2, "completed deletion batch must survive a later failure")
			require.Len(t, ops[1].filter.(bson.M)["$or"], cleanupBatchSize)
			if soft {
				require.Equal(t, "UpdateMany", ops[1].name)
			} else {
				require.Equal(t, "DeleteMany", ops[1].name)
			}
		})
	}
}

type cleanupContextClient struct {
	mongoClient
	aggregate func(context.Context, string) (*mongo.Cursor, error)
}

func (c *cleanupContextClient) Aggregate(ctx context.Context, db, coll string, pipeline any,
	opts ...*options.AggregateOptions) (*mongo.Cursor, error) {
	return c.aggregate(ctx, coll)
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
				require.Len(t, ops, 1)
				require.Equal(t, "UpdateMany", ops[0].name)
				require.Equal(t, svc.collSessionTracks, ops[0].coll)
			}
		})
	}
}
