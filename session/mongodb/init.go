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
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
)

// ensureIndexes creates the indexes required by the session backend.
//
// All unique indexes filter on `deleted_at $exists false` so soft-deleted
// documents do not occupy a unique slot, mirroring the partial unique index
// used by the PostgreSQL backend.
func (s *Service) ensureIndexes(ctx context.Context) error {
	notDeleted := bson.M{"deleted_at": bson.M{"$exists": false}}

	plan := []struct {
		coll   string
		models []mongo.IndexModel
	}{
		{
			coll: s.collSessionStates,
			models: []mongo.IndexModel{
				{
					Keys: bson.D{
						{Key: "app_name", Value: 1},
						{Key: "user_id", Value: 1},
						{Key: "session_id", Value: 1},
					},
					Options: options.Index().
						SetName(sqldb.BuildIndexName(s.opts.collectionPrefix,
							sqldb.TableNameSessionStates, sqldb.IndexSuffixUniqueActive)).
						SetUnique(true).
						SetPartialFilterExpression(notDeleted),
				},
			},
		},
		{
			coll: s.collSessionEvents,
			models: []mongo.IndexModel{
				// Lookup index used by AppendEvent reads / GetSession event
				// loading. Mirrors postgres' (app_name, user_id, session_id,
				// created_at) lookup index. _id (ObjectId) is the implicit
				// tie-breaker for events with identical created_at — see
				// D3 in the plan.
				{
					Keys: bson.D{
						{Key: "app_name", Value: 1},
						{Key: "user_id", Value: 1},
						{Key: "session_id", Value: 1},
						{Key: "created_at", Value: 1},
					},
					Options: options.Index().
						SetName(sqldb.BuildIndexName(s.opts.collectionPrefix,
							sqldb.TableNameSessionEvents, sqldb.IndexSuffixLookup)).
						SetPartialFilterExpression(notDeleted),
				},
			},
		},
		{
			coll: s.collSessionSummaries,
			models: []mongo.IndexModel{
				{
					Keys: bson.D{
						{Key: "app_name", Value: 1},
						{Key: "user_id", Value: 1},
						{Key: "session_id", Value: 1},
						{Key: "filter_key", Value: 1},
					},
					Options: options.Index().
						SetName(sqldb.BuildIndexName(s.opts.collectionPrefix,
							sqldb.TableNameSessionSummaries, sqldb.IndexSuffixUniqueActive)).
						SetUnique(true).
						SetPartialFilterExpression(notDeleted),
				},
			},
		},
		{
			coll: s.collAppStates,
			models: []mongo.IndexModel{
				{
					Keys: bson.D{
						{Key: "app_name", Value: 1},
						{Key: "key", Value: 1},
					},
					Options: options.Index().
						SetName(sqldb.BuildIndexName(s.opts.collectionPrefix,
							sqldb.TableNameAppStates, sqldb.IndexSuffixUniqueActive)).
						SetUnique(true).
						SetPartialFilterExpression(notDeleted),
				},
			},
		},
		{
			coll: s.collUserStates,
			models: []mongo.IndexModel{
				{
					Keys: bson.D{
						{Key: "app_name", Value: 1},
						{Key: "user_id", Value: 1},
						{Key: "key", Value: 1},
					},
					Options: options.Index().
						SetName(sqldb.BuildIndexName(s.opts.collectionPrefix,
							sqldb.TableNameUserStates, sqldb.IndexSuffixUniqueActive)).
						SetUnique(true).
						SetPartialFilterExpression(notDeleted),
				},
			},
		},
	}

	for _, p := range plan {
		if _, err := s.client.EnsureIndexes(ctx, s.database, p.coll, p.models); err != nil {
			return fmt.Errorf("ensure indexes on %s: %w", p.coll, err)
		}
	}
	return nil
}
