//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// integrationURI returns the MongoDB URI to use for integration tests, or
// skips the calling test when the environment variable is unset.
func integrationURI(t *testing.T) string {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		t.Skip("MONGO_URI not set; skipping integration test")
	}
	return uri
}

// integrationDB returns a unique database name for the current test run, so
// concurrent runs and leftover state from previous runs do not collide.
func integrationDB(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("trpc_agent_go_storage_mongodb_test_%d", time.Now().UnixNano())
}

// newIntegrationClient connects to the MongoDB deployment described by
// MONGO_URI and registers cleanup for both the client and the test database.
func newIntegrationClient(t *testing.T) (*defaultClient, string) {
	t.Helper()

	uri := integrationURI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	baseClient, err := defaultClientBuilder(ctx, WithClientBuilderDSN(uri))
	require.NoError(t, err, "connect to MongoDB")
	client, ok := baseClient.(*defaultClient)
	require.True(t, ok, "defaultClientBuilder must return *defaultClient")

	dbName := integrationDB(t)
	t.Cleanup(func() {
		// Best-effort cleanup; ignore errors so tests still report the
		// underlying assertion failure rather than a teardown failure.
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = client.client.Database(dbName).Drop(dropCtx)
		_ = client.Disconnect(dropCtx)
	})
	return client, dbName
}

func TestIntegration_CRUD(t *testing.T) {
	client, db := newIntegrationClient(t)
	ctx := context.Background()
	const coll = "items"

	// Seed three documents via InsertOne (the only insert primitive on Client).
	for _, doc := range []bson.M{
		{"_id": "a", "v": 1},
		{"_id": "b", "v": 2},
		{"_id": "c", "v": 3},
	} {
		_, err := client.InsertOne(ctx, db, coll, doc)
		require.NoError(t, err)
	}

	// FindOne round-trip.
	var doc struct {
		ID string `bson:"_id"`
		V  int    `bson:"v"`
	}
	require.NoError(t, client.FindOne(ctx, db, coll, bson.M{"_id": "a"}).Decode(&doc))
	assert.Equal(t, "a", doc.ID)
	assert.Equal(t, 1, doc.V)

	// UpdateOne.
	updRes, err := client.UpdateOne(ctx, db, coll,
		bson.M{"_id": "a"}, bson.M{"$set": bson.M{"v": 10}})
	require.NoError(t, err)
	assert.Equal(t, int64(1), updRes.ModifiedCount)

	// UpdateMany.
	updMany, err := client.UpdateMany(ctx, db, coll,
		bson.M{"v": bson.M{"$gte": 2}}, bson.M{"$set": bson.M{"kind": "bulk"}})
	require.NoError(t, err)
	assert.Equal(t, int64(3), updMany.ModifiedCount)

	// Find + cursor iteration returns all three documents in id order.
	cursor, err := client.Find(ctx, db, coll, bson.M{},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	require.NoError(t, err)
	defer cursor.Close(ctx)

	var all []bson.M
	require.NoError(t, cursor.All(ctx, &all))
	require.Len(t, all, 3)
	assert.Equal(t, "a", all[0]["_id"])
	assert.EqualValues(t, 10, all[0]["v"])

	// DeleteOne removes a single matching document.
	delRes, err := client.DeleteOne(ctx, db, coll, bson.M{"_id": "a"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), delRes.DeletedCount)

	// DeleteMany removes the rest.
	delMany, err := client.DeleteMany(ctx, db, coll, bson.M{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), delMany.DeletedCount)
}

func TestIntegration_CreateMany(t *testing.T) {
	client, db := newIntegrationClient(t)
	ctx := context.Background()
	const coll = "indexed"

	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "app_name", Value: 1}, {Key: "user_id", Value: 1}},
			Options: options.Index().SetName("by_app_user"),
		},
		{
			Keys:    bson.D{{Key: "expires_at", Value: 1}},
			Options: options.Index().SetName("ttl_expires_at").SetExpireAfterSeconds(0),
		},
	}

	// First call creates the indexes.
	names, err := client.CreateMany(ctx, db, coll, models)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"by_app_user", "ttl_expires_at"}, names)

	// Second call is idempotent: same models returned, no error.
	namesAgain, err := client.CreateMany(ctx, db, coll, models)
	require.NoError(t, err)
	assert.ElementsMatch(t, names, namesAgain)
}

func TestIntegration_Transaction_CommitAndAbort(t *testing.T) {
	client, db := newIntegrationClient(t)
	ctx := context.Background()
	const coll = "tx"

	t.Run("commit on success", func(t *testing.T) {
		err := client.Transaction(ctx, func(sc mongo.SessionContext) error {
			_, err := client.InsertOne(sc, db, coll, bson.M{"_id": "ok", "v": 1})
			return err
		}, nil)
		// Transactions require a replica set / sharded cluster. If the
		// configured deployment is standalone, the driver returns a clear
		// error and we skip the rest of this subtest.
		if err != nil && isUnsupportedTransactionError(err) {
			t.Skipf("MongoDB deployment does not support transactions: %v", err)
		}
		require.NoError(t, err)

		var got bson.M
		require.NoError(t, client.FindOne(ctx, db, coll, bson.M{"_id": "ok"}).Decode(&got))
	})

	t.Run("rollback on error", func(t *testing.T) {
		boom := errors.New("rollback please")
		err := client.Transaction(ctx, func(sc mongo.SessionContext) error {
			if _, err := client.InsertOne(sc, db, coll, bson.M{"_id": "abort", "v": 1}); err != nil {
				return err
			}
			return boom
		}, nil)
		if err != nil && isUnsupportedTransactionError(err) {
			t.Skipf("MongoDB deployment does not support transactions: %v", err)
		}
		require.ErrorIs(t, err, boom)

		// The aborted insert must not be visible.
		err = client.FindOne(ctx, db, coll, bson.M{"_id": "abort"}).Err()
		assert.ErrorIs(t, err, mongo.ErrNoDocuments)
	})
}

// isUnsupportedTransactionError reports whether err looks like
// "transactions are not supported by this deployment". The MongoDB driver
// does not export a sentinel for this, so we match on the well-known
// message fragments.
func isUnsupportedTransactionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, frag := range []string{
		"Transaction numbers are only allowed on a replica set",
		"Transactions are not supported",
		"transactions are not supported",
	} {
		if containsFold(msg, frag) {
			return true
		}
	}
	return false
}

// containsFold is a tiny case-insensitive substring check; we avoid pulling
// in strings.EqualFold-based helpers for one site.
func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFoldASCII(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func TestIntegration_Close(t *testing.T) {
	uri := integrationURI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := defaultClientBuilder(ctx, WithClientBuilderDSN(uri))
	require.NoError(t, err)

	require.NoError(t, client.Disconnect(ctx))
}
