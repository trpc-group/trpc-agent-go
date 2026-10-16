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

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ensureIndexes creates the indexes required by the memory backend.
//
// The unique index filters on active documents so soft-deleted tombstones do
// not occupy a unique slot, mirroring the partial unique index used by the
// PostgreSQL backend. MongoDB partial indexes do not support `$exists:
// false`, so both the index and active-document queries use `deleted_at:
// nil`, which also matches documents where the field is absent (hard-delete
// mode). The lookup index is intentionally not partial so that queries
// without a deleted_at predicate can still use it.
func (s *Service) ensureIndexes(ctx context.Context) error {
	log.InfoContext(ctx, "initializing mongodb memory indexes...")

	models := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "app_name", Value: 1},
				{Key: "user_id", Value: 1},
				{Key: "memory_id", Value: 1},
			},
			Options: options.Index().
				SetName(fmt.Sprintf("idx_%s_unique_active", s.collection)).
				SetUnique(true).
				SetPartialFilterExpression(bson.M{"deleted_at": nil}),
		},
		{
			Keys: bson.D{
				{Key: "app_name", Value: 1},
				{Key: "user_id", Value: 1},
			},
			Options: options.Index().
				SetName(fmt.Sprintf("idx_%s_app_user", s.collection)),
		},
	}

	if _, err := s.client.CreateMany(ctx, s.database, s.collection, models); err != nil {
		return fmt.Errorf("ensure indexes on %s: %w", s.collection, err)
	}

	log.InfofContext(ctx, "mongodb memory indexes initialized on collection: %s", s.collection)
	return nil
}
