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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type cleanupFixture struct {
	name                           string
	groups, rows, oldRows, deleted int
}

func seedCleanupFixture(t testing.TB, ctx context.Context, coll *mongo.Collection,
	now time.Time, fixture cleanupFixture) {
	t.Helper()
	payload := make([]byte, 1024)
	total := fixture.deleted + fixture.groups*fixture.rows
	for start := 0; start < total; start += 1000 {
		var docs []any
		for i := start; i < start+1000 && i < total; i++ {
			row := bson.M{"app_name": "app", "user_id": "user", "event": payload}
			if i < fixture.deleted {
				row["session_id"] = "deleted"
				row["deleted_at"] = now
				row["updated_at"] = now.Add(-2 * time.Hour)
			} else {
				j := i - fixture.deleted
				row["session_id"] = fmt.Sprint(j / fixture.rows)
				row["updated_at"] = now
				if j%fixture.rows < fixture.oldRows {
					row["updated_at"] = now.Add(-2 * time.Hour)
				}
			}
			docs = append(docs, row)
		}
		_, err := coll.InsertMany(ctx, docs)
		require.NoError(t, err)
	}
}

// Capture the service's actual pipeline so measurements follow production code.
func cleanupPipelineForTest(t testing.TB, now time.Time) bson.A {
	t.Helper()
	var pipeline bson.A
	svc := &Service{client: &mockClient{
		aggregateFn: func(value any) (*mongo.Cursor, error) {
			pipeline = value.(bson.A)
			return emptyCursor()
		},
	}}
	require.NoError(t, svc.cleanupExpiredCollection(context.Background(), now, time.Hour, "history", "history"))
	return pipeline
}

// BenchmarkIntegrationCleanupIdentification compares both recent-heavy and
// deleted-heavy workloads without timing setup or deletes. Each timed iteration
// consumes the entire result cursor and validates the exact expired group set.
// The optional delay models one service-level Aggregate call, excluding getMore.
func BenchmarkIntegrationCleanupIdentification(b *testing.B) {
	for _, fixture := range []cleanupFixture{
		{name: "mostly_deleted", groups: 100, rows: 200, oldRows: 1, deleted: 200000},
		{name: "mostly_recent", groups: 100, rows: 400, oldRows: 5},
		{name: "short_active", groups: 3200, rows: 2, oldRows: 1},
		{name: "expired", groups: 3200, rows: 1, oldRows: 1},
		{name: "long_lived", groups: 2000, rows: 100, oldRows: 50},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			ctx, svc, db := newIntegrationService(b)
			coll := db.Collection(svc.collSessionTracks)
			now := time.Now().UTC().Truncate(time.Millisecond)
			seedCleanupFixture(b, ctx, coll, now, fixture)
			expected := make(map[string]bson.M)
			if fixture.oldRows == fixture.rows {
				for i := 0; i < fixture.groups; i++ {
					expected[fmt.Sprint(i)] = bson.M{"app_name": "app", "user_id": "user", "session_id": fmt.Sprint(i)}
				}
			}
			for _, strategy := range []struct {
				name     string
				pipeline bson.A
			}{
				{"original", originalCleanupPipeline(now.Add(-time.Hour))},
				{"indexed", cleanupPipelineForTest(b, now)},
			} {
				for _, delay := range []time.Duration{0, 10 * time.Millisecond} {
					b.Run(fmt.Sprintf("%s/delay=%s", strategy.name, delay), func(b *testing.B) {
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							if delay > 0 {
								time.Sleep(delay)
							}
							cur, err := coll.Aggregate(ctx, strategy.pipeline)
							require.NoError(b, err)
							var groups []struct {
								ID bson.M `bson:"_id"`
							}
							require.NoError(b, cur.All(ctx, &groups))
							b.StopTimer()
							actual := make(map[string]bson.M)
							for _, group := range groups {
								actual[group.ID["session_id"].(string)] = group.ID
							}
							require.Len(b, groups, len(expected))
							require.Equal(b, expected, actual)
							b.StartTimer()
						}
						b.ReportMetric(1, "aggregate_calls/op")
					})
				}
			}
		})
	}
}
