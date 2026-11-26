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

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRecentEventsErrors(t *testing.T) {
	queryErr := errors.New("query failed")
	cases := []struct {
		name  string
		find  func(any) (*mongo.Cursor, error)
		want  string
		cause error
	}{
		{"query", func(any) (*mongo.Cursor, error) { return nil, queryErr }, "query events", queryErr},
		{"decode", func(any) (*mongo.Cursor, error) { return docsCursor([]any{bson.M{"event": 123}}) }, "decode event", nil},
		{"payload", func(any) (*mongo.Cursor, error) { return docsCursor([]any{bson.M{"event": []byte("invalid")}}) }, "unmarshal event", nil},
		{"cursor", func(any) (*mongo.Cursor, error) { return mongo.NewCursorFromDocuments(nil, queryErr, nil) }, "iterate events", queryErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newServiceForTest(t, &mockClient{findFn: tc.find})
			_, err := svc.getEventsList(context.Background(), []session.Key{{AppName: "app", UserID: "user", SessionID: "session"}}, 10, time.Time{}, nil)
			require.ErrorContains(t, err, tc.want)
			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}
		})
	}
}
