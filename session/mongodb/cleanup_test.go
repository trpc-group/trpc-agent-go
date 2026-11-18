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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
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
