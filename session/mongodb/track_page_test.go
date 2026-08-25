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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

func TestGetTrackEventPagePaginatesDocs(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	baseTime := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	ids := []primitive.ObjectID{
		primitive.NewObjectIDFromTimestamp(baseTime),
		primitive.NewObjectIDFromTimestamp(baseTime.Add(time.Second)),
		primitive.NewObjectIDFromTimestamp(baseTime.Add(2 * time.Second)),
	}
	findCalls := 0
	mc := &mockClient{
		findFn: func(_ any) (*mongo.Cursor, error) {
			findCalls++
			if findCalls == 1 {
				return docsCursor([]any{
					mongoTrackPageDoc(t, key, ids[2], "newest", baseTime.Add(2*time.Second)),
					mongoTrackPageDoc(t, key, ids[1], "middle", baseTime.Add(time.Second)),
					mongoTrackPageDoc(t, key, ids[0], "oldest", baseTime),
				})
			}
			return docsCursor([]any{
				mongoTrackPageDoc(t, key, ids[0], "oldest", baseTime),
			})
		},
	}
	s := newServiceForTest(t, mc)
	ctx := context.Background()

	page, err := s.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		EventLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, page.Entries, 2)
	assert.True(t, page.HasMore)
	assert.Equal(t, session.Track("alpha"), page.Track)
	assert.Equal(t, json.RawMessage(`"middle"`), page.Entries[0].Event.Payload)
	assert.Equal(t, json.RawMessage(`"newest"`), page.Entries[1].Event.Payload)

	cursor, err := trackpage.Decode(page.Entries[0].Cursor)
	require.NoError(t, err)
	assert.Equal(t, trackEventPageCursorKindMongoDB, cursor.Kind)
	assert.Equal(t, ids[1].Hex(), cursor.ID)
	assert.Equal(t, trackpage.TimeToUnixNano(baseTime.Add(time.Second)), cursor.CreatedAt)

	next, err := s.GetTrackEventPage(ctx, session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     page.Entries[0].Cursor,
		EventLimit: 2,
	})
	require.NoError(t, err)
	require.Len(t, next.Entries, 1)
	assert.False(t, next.HasMore)
	assert.Equal(t, json.RawMessage(`"oldest"`), next.Entries[0].Event.Payload)
	assert.Equal(t, 2, findCalls)

	ops := mc.recorded()
	require.Len(t, ops, 2)
	firstFilter, ok := ops[0].filter.(bson.M)
	require.True(t, ok)
	assert.Equal(t, session.Track("alpha"), firstFilter["track"])
	secondFilter, ok := ops[1].filter.(bson.M)
	require.True(t, ok)
	assert.Contains(t, secondFilter, "$and")
}

func TestGetTrackEventPageRejectsWrongCursorBinding(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	mc := &mockClient{}
	s := newServiceForTest(t, mc)
	cursor, err := trackpage.CursorFor(
		trackEventPageCursorKindMongoDB,
		key,
		"beta",
		time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC),
		primitive.NewObjectID().Hex(),
	)
	require.NoError(t, err)

	_, err = s.GetTrackEventPage(context.Background(), session.TrackEventPageRequest{
		Key:        key,
		Track:      "alpha",
		Cursor:     cursor,
		EventLimit: 1,
	})
	require.Error(t, err)
	assert.Empty(t, mc.recorded())
}

func mongoTrackPageDoc(
	t *testing.T,
	key session.Key,
	id primitive.ObjectID,
	payload string,
	createdAt time.Time,
) sessionTrackDoc {
	t.Helper()
	event := session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"` + payload + `"`),
		Timestamp: createdAt,
	}
	data, err := json.Marshal(event)
	require.NoError(t, err)
	return sessionTrackDoc{
		ID:        id,
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		Track:     "alpha",
		Event:     data,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}
