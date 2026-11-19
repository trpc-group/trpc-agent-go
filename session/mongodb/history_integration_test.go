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
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestIntegrationRecentHistoryMatchesFullFiltering(t *testing.T) {
	ctx, svc, db := newIntegrationService(t)
	base := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Minute)
	for _, userAt := range [][]int{{0, 4, 7}, {0}, {4}, {}} {
		key := session.Key{AppName: "history", UserID: "user", SessionID: fmt.Sprint(userAt)}
		_, err := svc.CreateSession(ctx, key, nil)
		require.NoError(t, err)
		var docs []any
		// Payload timestamps intentionally disagree with creation order. Include
		// tied creation timestamps and an empty payload, which must not use a slot.
		offsets := []int{10, 1, 8, 3, 4, 12, 2, 9}
		for i, offset := range offsets {
			role := model.RoleAssistant
			for _, j := range userAt {
				if i == j {
					role = model.RoleUser
				}
			}
			evt := integrationEvent(fmt.Sprint(i), role, "message", base.Add(time.Duration(offset)*time.Second))
			payload, err := json.Marshal(evt)
			require.NoError(t, err)
			docs = append(docs, sessionEventDoc{AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID, Event: payload, CreatedAt: base.Add(time.Duration(i/2) * time.Second)})
		}
		docs = append(docs, sessionEventDoc{AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID, CreatedAt: base.Add(20 * time.Second)})
		payload, err := json.Marshal(integrationEvent("deleted", model.RoleUser, "deleted", base))
		require.NoError(t, err)
		docs = append(docs, sessionEventDoc{AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID, Event: payload, CreatedAt: base.Add(21 * time.Second), DeletedAt: &base})
		_, err = db.Collection(svc.collSessionEvents).InsertMany(ctx, docs)
		require.NoError(t, err)
		for _, limit := range []int{1, 2, 3, 8, 9, 1000} {
			for _, cutoff := range []time.Time{{}, base, base.Add(4 * time.Second), base.Add(9 * time.Second), base.Add(11 * time.Second), base.Add(time.Hour)} {
				// Multiple keys retain the original forward-reading implementation.
				want, err := svc.getEventsList(ctx, []session.Key{key, key}, limit, cutoff, nil)
				require.NoError(t, err)
				got, err := svc.GetSession(ctx, key, session.WithEventNum(limit), session.WithEventTime(cutoff))
				require.NoError(t, err)
				require.Equal(t, want[0], got.Events, "users=%v limit=%d cutoff=%v", userAt, limit, cutoff)
			}
		}
		svc.opts.sessionEventLimit = 0
		want, err := svc.getEventsList(ctx, []session.Key{key, key}, 0, time.Time{}, nil)
		require.NoError(t, err)
		got, err := svc.GetSession(ctx, key)
		require.NoError(t, err)
		require.Equal(t, want[0], got.Events)
		svc.opts.sessionEventLimit = defaultSessionEventLimit
	}
	// Skip-initialization deployments remain correct without the new index.
	_, err := db.Collection(svc.collSessionEvents).Indexes().DropOne(ctx, "idx_session_events_ordered_lookup")
	require.NoError(t, err)
	key := session.Key{AppName: "history", UserID: "user", SessionID: "[0]"}
	want, err := svc.getEventsList(ctx, []session.Key{key, key}, 2, time.Time{}, nil)
	require.NoError(t, err)
	got, err := svc.GetSession(ctx, key, session.WithEventNum(2))
	require.NoError(t, err)
	require.Equal(t, want[0], got.Events)
}

func TestIntegrationRecentHistoryScanCost(t *testing.T) {
	ctx, svc, db := newIntegrationService(t)
	key := session.Key{AppName: "scan", UserID: "user", SessionID: "long"}
	_, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	base := time.Now().UTC().Truncate(time.Millisecond)
	for batch := 0; batch < 40; batch++ {
		docs := make([]any, 0, 1000)
		for j := 0; j < 1000; j++ {
			i := batch*1000 + j
			role := model.RoleAssistant
			if i%2 == 0 {
				role = model.RoleUser
			}
			evt := integrationEvent(fmt.Sprint(i), role, "synthetic history", base)
			payload, err := json.Marshal(evt)
			require.NoError(t, err)
			docs = append(docs, sessionEventDoc{AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID, Event: payload, CreatedAt: base})
		}
		_, err = db.Collection(svc.collSessionEvents).InsertMany(ctx, docs)
		require.NoError(t, err)
	}
	// Profiling captures both initial find and getMore commands, so returned
	// counts describe the complete consumed cursor rather than its first batch.
	require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "profile", Value: 2}}).Err())
	defer db.RunCommand(ctx, bson.D{{Key: "profile", Value: 0}})
	started := time.Now()
	want, err := svc.getEventsList(ctx, []session.Key{key, key}, 100, time.Time{}, nil)
	require.NoError(t, err)
	beforeDuration := time.Since(started)
	before := historyProfileTotals(t, db, svc.collSessionEvents)
	started = time.Now()
	got, err := svc.GetSession(ctx, key, session.WithEventNum(100))
	require.NoError(t, err)
	afterDuration := time.Since(started)
	require.Equal(t, want[0], got.Events)
	after := historyProfileTotals(t, db, svc.collSessionEvents)
	require.EqualValues(t, 40000, before.Returned)
	require.GreaterOrEqual(t, after.Returned-before.Returned, int64(100))
	require.LessOrEqual(t, after.Returned-before.Returned, int64(128))
	require.LessOrEqual(t, after.Docs-before.Docs, int64(128))
	t.Logf("full: docs=%d keys=%d returned=%d duration=%s; recent: docs=%d keys=%d returned=%d duration=%s", before.Docs, before.Keys, before.Returned, beforeDuration, after.Docs-before.Docs, after.Keys-before.Keys, after.Returned-before.Returned, afterDuration)
}

func TestIntegrationRecentHistorySmallWindowRoundTrips(t *testing.T) {
	const historySize = 2000
	for _, tc := range []struct {
		name          string
		userAt        int
		returned      int64
		maxOperations int64
	}{
		{name: "distant_user", userAt: 0, returned: historySize, maxOperations: 32},
		{name: "no_user", userAt: -1, returned: historySize, maxOperations: 32},
		{name: "user_in_window", userAt: historySize - 1, returned: 1, maxOperations: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, svc, db := newIntegrationService(t)
			key := session.Key{AppName: "roundtrips", UserID: "user", SessionID: tc.name}
			_, err := svc.CreateSession(ctx, key, nil)
			require.NoError(t, err)
			base := time.Now().UTC().Truncate(time.Millisecond)
			docs := make([]any, 0, historySize)
			for i := 0; i < historySize; i++ {
				role := model.RoleAssistant
				if i == tc.userAt {
					role = model.RoleUser
				}
				payload, err := json.Marshal(integrationEvent(fmt.Sprint(i), role, "message", base))
				require.NoError(t, err)
				docs = append(docs, sessionEventDoc{AppName: key.AppName, UserID: key.UserID, SessionID: key.SessionID, Event: payload, CreatedAt: base})
			}
			_, err = db.Collection(svc.collSessionEvents).InsertMany(ctx, docs)
			require.NoError(t, err)
			// Keep every operation even if a regression fetches history one row at a time.
			require.NoError(t, db.CreateCollection(ctx, "system.profile",
				options.CreateCollection().SetCapped(true).SetSizeInBytes(16<<20)))
			require.NoError(t, db.RunCommand(ctx, bson.D{{Key: "profile", Value: 2}}).Err())
			defer db.RunCommand(ctx, bson.D{{Key: "profile", Value: 0}})
			want, err := svc.getEventsList(ctx, []session.Key{key, key}, 1, time.Time{}, nil)
			require.NoError(t, err)
			before := historyProfileTotals(t, db, svc.collSessionEvents)
			got, err := svc.GetSession(ctx, key, session.WithEventNum(1))
			require.NoError(t, err)
			require.Equal(t, want[0], got.Events)
			after := historyProfileTotals(t, db, svc.collSessionEvents)
			operations := after.Operations - before.Operations
			returned := after.Returned - before.Returned
			t.Logf("full operations=%d; recent operations=%d returned=%d", before.Operations, operations, returned)
			require.Equal(t, tc.returned, returned)
			require.LessOrEqual(t, operations, tc.maxOperations)
		})
	}
}

type historyProfile struct {
	Operations int64 `bson:"operations"`
	Docs       int64 `bson:"docs"`
	Keys       int64 `bson:"keys"`
	Returned   int64 `bson:"returned"`
}

func historyProfileTotals(t *testing.T, db *mongo.Database, coll string) historyProfile {
	t.Helper()
	ctx := context.Background()
	cursor, err := db.Collection("system.profile").Aggregate(ctx, bson.A{
		bson.M{"$match": bson.M{"ns": db.Name() + "." + coll, "op": bson.M{"$in": bson.A{"query", "getmore"}}}},
		bson.M{"$group": bson.M{"_id": nil, "operations": bson.M{"$sum": 1}, "docs": bson.M{"$sum": "$docsExamined"}, "keys": bson.M{"$sum": "$keysExamined"}, "returned": bson.M{"$sum": "$nreturned"}}},
	})
	require.NoError(t, err)
	defer cursor.Close(ctx)
	var total historyProfile
	if cursor.Next(ctx) {
		require.NoError(t, cursor.Decode(&total))
	}
	require.NoError(t, cursor.Err())
	return total
}
