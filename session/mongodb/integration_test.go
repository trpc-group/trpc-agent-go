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
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const mongodbIntegrationURIEnv = "MONGODB_INTEGRATION_URI"

type integrationSummarizer struct {
	text string
}

func (s *integrationSummarizer) ShouldSummarize(_ *session.Session) bool { return true }
func (s *integrationSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	return s.text, nil
}
func (s *integrationSummarizer) SetPrompt(_ string)       {}
func (s *integrationSummarizer) SetModel(_ model.Model)   {}
func (s *integrationSummarizer) Metadata() map[string]any { return nil }

func newIntegrationService(t *testing.T, opts ...ServiceOpt) (context.Context, *Service, *mongo.Database) {
	t.Helper()
	uri := os.Getenv(mongodbIntegrationURIEnv)
	if uri == "" {
		t.Skipf("%s is not set", mongodbIntegrationURIEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dbName := fmt.Sprintf("trpc_agent_go_mongodb_it_%d", time.Now().UnixNano())
	cleanupClient, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	database := cleanupClient.Database(dbName)
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = database.Drop(dropCtx)
		_ = cleanupClient.Disconnect(dropCtx)
	})

	allOpts := []ServiceOpt{
		WithMongoClientURI(uri),
		WithDatabase(dbName),
	}
	allOpts = append(allOpts, opts...)
	svc, err := NewService(allOpts...)
	require.NoError(t, err, "integration MongoDB must support multi-document transactions")
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	return context.Background(), svc, database
}

func integrationEvent(id string, role model.Role, content string, ts time.Time) *event.Event {
	evt := event.New("integration-invocation", string(role))
	evt.ID = id
	evt.Timestamp = ts
	evt.Response = &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Role: role, Content: content},
		}},
	}
	evt.IsPartial = false
	return evt
}

func TestIntegrationLifecycleTrackWindowSummary(t *testing.T) {
	ctx, svc, _ := newIntegrationService(t,
		WithSummarizer(&integrationSummarizer{text: "integration summary"}),
		WithAsyncSummaryNum(1),
	)

	key := session.Key{AppName: "it-app", UserID: "it-user", SessionID: "it-session"}
	sess, err := svc.CreateSession(ctx, key, session.StateMap{"seed": []byte("value")})
	require.NoError(t, err)
	require.NotNil(t, sess)

	baseTime := time.Now()
	require.NoError(t, svc.AppendEvent(ctx, sess, integrationEvent("u1", model.RoleUser, "hello", baseTime)))
	require.NoError(t, svc.AppendEvent(ctx, sess, integrationEvent("a1", model.RoleAssistant, "hi", baseTime.Add(time.Second))))
	require.NoError(t, svc.AppendEvent(ctx, sess, integrationEvent("u2", model.RoleUser, "again", baseTime.Add(2*time.Second))))

	trackEvent := &session.TrackEvent{
		Track:     session.Track("tool"),
		Payload:   json.RawMessage(`{"ok":true}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svc.AppendTrackEvent(ctx, sess, trackEvent))

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got.Events, 3)
	assert.Equal(t, "u1", got.Events[0].ID)
	assert.Equal(t, []byte("value"), got.State["seed"])
	require.Contains(t, got.Tracks, session.Track("tool"))
	require.Len(t, got.Tracks["tool"].Events, 1)

	window, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "a1",
		Before:        1,
		After:         1,
	})
	require.NoError(t, err)
	require.Len(t, window.Entries, 3)
	assert.Equal(t, "u1", window.Entries[0].Event.ID)
	assert.Equal(t, "a1", window.Entries[1].Event.ID)
	assert.Equal(t, "u2", window.Entries[2].Event.ID)

	require.NoError(t, svc.CreateSessionSummary(ctx, got, session.SummaryFilterKeyAllContents, true))
	fresh := session.NewSession(got.AppName, got.UserID, got.ID,
		session.WithSessionCreatedAt(got.CreatedAt))
	text, ok := svc.GetSessionSummaryText(ctx, fresh)
	require.True(t, ok)
	assert.Equal(t, "integration summary", text)

	listed, err := svc.ListSessions(ctx, session.UserKey{AppName: key.AppName, UserID: key.UserID})
	require.NoError(t, err)
	require.Len(t, listed, 1)

	require.NoError(t, svc.DeleteSession(ctx, key))
	deleted, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

func TestIntegrationWindowIgnoresEventsBeforeRecreatedSession(t *testing.T) {
	ctx, svc, _ := newIntegrationService(t)
	key := session.Key{AppName: "it-app", UserID: "it-user", SessionID: "recreated"}

	oldTime := time.Now().Add(-time.Hour)
	oldEventBytes, err := json.Marshal(integrationEvent("old", model.RoleUser, "old", oldTime))
	require.NoError(t, err)
	_, err = svc.client.InsertOne(ctx, svc.database, svc.collSessionEvents, sessionEventDoc{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		EventID:   "old",
		Event:     oldEventBytes,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	})
	require.NoError(t, err)

	second, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(ctx, second, integrationEvent("new", model.RoleUser, "new", time.Now())))

	_, err = svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "old",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anchor event not found")

	window, err := svc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: "new",
	})
	require.NoError(t, err)
	require.Len(t, window.Entries, 1)
	assert.Equal(t, "new", window.Entries[0].Event.ID)
}

func TestIntegrationCreateSessionReplacesExpiredSameKey(t *testing.T) {
	ctx, svc, _ := newIntegrationService(t, WithSessionTTL(time.Hour))
	key := session.Key{AppName: "it-app", UserID: "it-user", SessionID: "expired-recreate"}
	oldTime := time.Now().Add(-2 * time.Hour)
	oldExpiresAt := time.Now().Add(-time.Hour)
	oldEventBytes, err := json.Marshal(integrationEvent("old", model.RoleUser, "old", oldTime))
	require.NoError(t, err)

	_, err = svc.client.InsertOne(ctx, svc.database, svc.collSessionStates, sessionStateDoc{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		State:     bson.M{"old": []byte("state")},
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
		ExpiresAt: &oldExpiresAt,
	})
	require.NoError(t, err)
	_, err = svc.client.InsertOne(ctx, svc.database, svc.collSessionEvents, sessionEventDoc{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
		EventID:   "old",
		Event:     oldEventBytes,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	})
	require.NoError(t, err)

	sess, err := svc.CreateSession(ctx, key, session.StateMap{"fresh": []byte("state")})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, []byte("state"), sess.State["fresh"])

	got, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []byte("state"), got.State["fresh"])
	assert.Empty(t, got.Events)
}

func TestIntegrationIndexesAndGroupedCleanup(t *testing.T) {
	ctx, svc, database := newIntegrationService(t,
		WithSessionTTL(time.Hour),
		WithSoftDelete(false),
	)

	assertIndexNames(t, ctx, database, svc.collSessionStates,
		"idx_session_states_unique_active", "idx_session_states_expires")
	assertIndexNames(t, ctx, database, svc.collSessionEvents,
		"idx_session_events_cleanup", "idx_session_events_event_id_lookup", "idx_session_events_lookup")
	assertIndexNames(t, ctx, database, svc.collSessionTracks,
		"idx_session_tracks_cleanup", "idx_session_tracks_lookup")
	assertIndexNames(t, ctx, database, svc.collSessionSummaries,
		"idx_session_summaries_unique_active")
	assertIndexNames(t, ctx, database, svc.collAppStates,
		"idx_app_states_unique_active", "idx_app_states_expires")
	assertIndexNames(t, ctx, database, svc.collUserStates,
		"idx_user_states_unique_active", "idx_user_states_expires")

	oldTime := time.Now().Add(-2 * time.Hour)
	eventBytes, err := json.Marshal(integrationEvent("old-event", model.RoleUser, "old", oldTime))
	require.NoError(t, err)
	trackBytes, err := json.Marshal(&session.TrackEvent{
		Track:     session.Track("tool"),
		Payload:   json.RawMessage(`{"old":true}`),
		Timestamp: oldTime,
	})
	require.NoError(t, err)

	_, err = svc.client.InsertOne(ctx, svc.database, svc.collSessionEvents, sessionEventDoc{
		AppName:   "it-app",
		UserID:    "it-user",
		SessionID: "expired",
		EventID:   "old-event",
		Event:     eventBytes,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	})
	require.NoError(t, err)
	_, err = svc.client.InsertOne(ctx, svc.database, svc.collSessionTracks, sessionTrackDoc{
		AppName:   "it-app",
		UserID:    "it-user",
		SessionID: "expired",
		Track:     session.Track("tool"),
		Event:     trackBytes,
		CreatedAt: oldTime,
		UpdatedAt: oldTime,
	})
	require.NoError(t, err)

	require.NoError(t, svc.cleanupExpiredEvents(ctx, time.Now()))
	require.NoError(t, svc.cleanupExpiredTracks(ctx, time.Now()))

	assertNoDocs(t, ctx, svc, svc.collSessionEvents, bson.M{"session_id": "expired"})
	assertNoDocs(t, ctx, svc, svc.collSessionTracks, bson.M{"session_id": "expired"})
}

func assertIndexNames(t *testing.T, ctx context.Context, database *mongo.Database, coll string, names ...string) {
	t.Helper()
	cursor, err := database.Collection(coll).Indexes().List(ctx)
	require.NoError(t, err)
	defer cursor.Close(ctx)

	found := map[string]bool{}
	for cursor.Next(ctx) {
		var row struct {
			Name string `bson:"name"`
		}
		require.NoError(t, cursor.Decode(&row))
		found[row.Name] = true
	}
	require.NoError(t, cursor.Err())
	for _, name := range names {
		assert.True(t, found[name], "missing index %s on %s", name, coll)
	}
}

func assertNoDocs(t *testing.T, ctx context.Context, svc *Service, coll string, filter bson.M) {
	t.Helper()
	cursor, err := svc.client.Find(ctx, svc.database, coll, filter)
	require.NoError(t, err)
	defer cursor.Close(ctx)
	assert.False(t, cursor.Next(ctx), "expected no documents in %s for %v", coll, filter)
	require.NoError(t, cursor.Err())
}
