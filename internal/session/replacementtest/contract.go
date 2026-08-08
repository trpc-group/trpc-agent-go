//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package replacementtest provides the shared latest-turn replacement
// contract for session backend tests.
package replacementtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Run exercises the private contract shared by every backend that advertises
// latest-turn replacement support.
func Run(t *testing.T, service session.Service) {
	t.Helper()
	t.Run("restore and fencing", func(t *testing.T) {
		testRestoreAndFencing(t, service)
	})
	t.Run("unfinished restore and fencing", func(t *testing.T) {
		testUnfinishedRestoreAndFencing(t, service)
	})
	t.Run("unsafe checkpoint", func(t *testing.T) {
		testUnsafeCheckpoint(t, service)
	})
	t.Run("request id reuse", func(t *testing.T) {
		testRequestIDReuse(t, service)
	})
	t.Run("authoritative checkpoint", func(t *testing.T) {
		testAuthoritativeCheckpoint(t, service)
	})
	t.Run("listed projection fencing", func(t *testing.T) {
		testListedProjectionFencing(t, service)
	})
}

// RunAsync exercises replacement boundaries that must remain durable when the
// backend acknowledges ordinary writes before they reach persistent storage.
func RunAsync(t *testing.T, service session.Service) {
	t.Helper()
	ctx := context.Background()
	key := contractKey(t, "async")
	sess, err := service.CreateSession(
		ctx,
		key,
		session.StateMap{"phase": []byte("before")},
	)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		messageEvent("before", "before", "before-invocation", model.RoleUser),
	))
	trackService, hasTracks := service.(session.TrackService)
	if hasTracks {
		requireNoError(t, trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track:     "ui",
			Payload:   []byte(`{"value":"before"}`),
			Timestamp: time.Now().Add(-time.Second),
		}))
	}

	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "latest",
		InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(
		turnCtx,
		sess,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	partial := messageEvent(
		"partial", "latest", "latest-invocation", model.RoleAssistant,
	)
	partial.StateDelta = session.StateMap{"phase": []byte("after")}
	requireNoError(t, service.AppendEvent(ctx, sess, partial))
	if hasTracks {
		requireNoError(t, trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track:     "ui",
			RequestID: "latest",
			Payload:   []byte(`{"value":"latest"}`),
			Timestamp: time.Now(),
		}))
	}

	result, err := revision.ReplaceLatestTurn(
		ctx,
		service,
		revision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "latest",
			IdempotencyKey:    "replacement",
		},
	)
	requireNoError(t, err)
	if len(result.ActiveSession.Events) != 1 ||
		result.ActiveSession.Events[0].ID != "before" {
		t.Fatalf("restored async events = %#v", result.ActiveSession.Events)
	}
	phase, ok := result.ActiveSession.GetState("phase")
	if !ok || string(phase) != "before" {
		t.Fatalf("restored async phase = %q, %v", phase, ok)
	}
	if hasTracks {
		tracks, err := result.ActiveSession.GetTrackEvents("ui")
		requireNoError(t, err)
		if len(tracks.Events) != 1 ||
			string(tracks.Events[0].Payload) != `{"value":"before"}` {
			t.Fatalf("restored async tracks = %#v", tracks.Events)
		}
	}
}

func testUnfinishedRestoreAndFencing(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "unfinished")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		messageEvent("before", "before", "before-invocation", model.RoleUser),
	))
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "latest",
		InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(
		turnCtx,
		sess,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		messageEvent("partial", "latest", "latest-invocation", model.RoleAssistant),
	))

	result, err := revision.ReplaceLatestTurn(
		ctx,
		service,
		revision.LatestTurnReplacementRequest{
			Key:               key,
			ExpectedRequestID: "latest",
			IdempotencyKey:    "replacement",
		},
	)
	requireNoError(t, err)
	if len(result.ActiveSession.Events) != 1 ||
		result.ActiveSession.Events[0].ID != "before" {
		t.Fatalf("restored unfinished events = %#v", result.ActiveSession.Events)
	}
	err = service.AppendEvent(ctx, sess, messageEvent(
		"stale", "latest", "latest-invocation", model.RoleAssistant,
	))
	if !errors.Is(err, revision.ErrStaleGeneration) {
		t.Fatalf("unfinished stale append error = %v", err)
	}
}

func testListedProjectionFencing(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "listed")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	listed, err := service.ListSessions(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	requireNoError(t, err)
	var stale *session.Session
	for _, candidate := range listed {
		if candidate.ID == key.SessionID {
			stale = candidate
			break
		}
	}
	if stale == nil {
		t.Fatal("created session is missing from ListSessions")
	}
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID: "latest", InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(
		turnCtx,
		sess,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		completionEvent("latest", "latest-invocation"),
	))
	_, err = revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key: key, ExpectedRequestID: "latest", IdempotencyKey: "replacement",
	})
	requireNoError(t, err)
	err = service.AppendEvent(ctx, stale, messageEvent(
		"stale", "stale", "stale-invocation", model.RoleAssistant,
	))
	if !errors.Is(err, revision.ErrStaleGeneration) {
		t.Fatalf("listed stale append error = %v", err)
	}
}

func testAuthoritativeCheckpoint(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "authoritative")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	stale := sess.Clone()
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		messageEvent("concurrent", "earlier", "earlier-invocation", model.RoleUser),
	))
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "latest",
		InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(
		turnCtx,
		stale,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	requireNoError(t, service.AppendEvent(
		ctx,
		stale,
		completionEvent("latest", "latest-invocation"),
	))
	result, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "latest",
		IdempotencyKey:    "replacement",
	})
	requireNoError(t, err)
	if len(result.ActiveSession.Events) != 1 ||
		result.ActiveSession.Events[0].ID != "concurrent" {
		t.Fatalf("restored authoritative events = %#v", result.ActiveSession.Events)
	}
}

func testRestoreAndFencing(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "restore")
	sess, err := service.CreateSession(
		ctx,
		key,
		session.StateMap{"phase": []byte("before")},
	)
	requireNoError(t, err)
	baseline := messageEvent("before", "baseline", "baseline-invocation", model.RoleUser)
	requireNoError(t, service.AppendEvent(ctx, sess, baseline))
	trackService, hasTracks := service.(session.TrackService)
	if hasTracks {
		requireNoError(t, trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track:     "ui",
			Payload:   []byte(`{"value":"before"}`),
			Timestamp: time.Now().Add(-time.Second),
		}))
	}

	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "latest",
		InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(
		turnCtx,
		sess,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	stateEvent := messageEvent("state", "latest", "latest-invocation", model.RoleAssistant)
	stateEvent.StateDelta = session.StateMap{"phase": []byte("after")}
	requireNoError(t, service.AppendEvent(ctx, sess, stateEvent))
	if hasTracks {
		requireNoError(t, trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track:     "ui",
			RequestID: "latest",
			Payload:   []byte(`{"value":"latest"}`),
			Timestamp: time.Now(),
		}))
	}
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		completionEvent("latest", "latest-invocation"),
	))

	req := revision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "latest",
		IdempotencyKey:    "replacement",
	}
	result, err := revision.ReplaceLatestTurn(ctx, service, req)
	requireNoError(t, err)
	if !result.Applied {
		t.Fatal("first replacement reported Applied=false")
	}
	if len(result.ActiveSession.Events) != 1 ||
		result.ActiveSession.Events[0].ID != "before" {
		t.Fatalf("restored events = %#v", result.ActiveSession.Events)
	}
	phase, ok := result.ActiveSession.GetState("phase")
	if !ok || string(phase) != "before" {
		t.Fatalf("restored phase = %q, %v", phase, ok)
	}
	if hasTracks {
		tracks, err := result.ActiveSession.GetTrackEvents("ui")
		requireNoError(t, err)
		if len(tracks.Events) != 1 || string(tracks.Events[0].Payload) != `{"value":"before"}` {
			t.Fatalf("restored tracks = %#v", tracks.Events)
		}
	}

	replay, err := revision.ReplaceLatestTurn(ctx, service, req)
	requireNoError(t, err)
	if replay.Applied {
		t.Fatal("idempotent replay reported Applied=true")
	}
	if _, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "other",
		IdempotencyKey:    req.IdempotencyKey,
	}); !errors.Is(err, revision.ErrLatestTurnReplacementConflict) {
		t.Fatalf("idempotency identity error = %v", err)
	}

	err = service.AppendEvent(ctx, sess, messageEvent(
		"stale",
		"stale",
		"stale-invocation",
		model.RoleAssistant,
	))
	if !errors.Is(err, revision.ErrStaleGeneration) {
		t.Fatalf("stale append error = %v", err)
	}
	requireNoError(t, service.AppendEvent(ctx, result.ActiveSession, messageEvent(
		"after",
		"after",
		"after-invocation",
		model.RoleAssistant,
	)))
	if _, err := revision.ReplaceLatestTurn(ctx, service, req); !errors.Is(
		err,
		revision.ErrLatestTurnReplacementConflict,
	) {
		t.Fatalf("replay after subsequent write error = %v", err)
	}
}

func testUnsafeCheckpoint(t *testing.T, service session.Service) {
	ctx := context.Background()
	t.Run("unfenced state write", func(t *testing.T) {
		key, sess := createStartedTurn(t, service, "state")
		requireNoError(t, service.UpdateSessionState(
			ctx,
			key,
			session.StateMap{"phase": []byte("unfenced")},
		))
		requireNoError(t, service.AppendEvent(
			ctx,
			sess,
			completionEvent("latest", "latest-invocation"),
		))
		_, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
			Key: key, ExpectedRequestID: "latest", IdempotencyKey: "replacement",
		})
		if !errors.Is(err, revision.ErrLatestTurnReplacementUnavailable) {
			t.Fatalf("unfenced state write error = %v", err)
		}
	})

	t.Run("unassociated track", func(t *testing.T) {
		trackService, ok := service.(session.TrackService)
		if !ok {
			t.Skip("service has no track capability")
		}
		key, sess := createStartedTurn(t, service, "track")
		requireNoError(t, trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
			Track:     "ui",
			Payload:   []byte(`{"value":"unknown"}`),
			Timestamp: time.Now(),
		}))
		requireNoError(t, service.AppendEvent(
			ctx,
			sess,
			completionEvent("latest", "latest-invocation"),
		))
		_, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
			Key: key, ExpectedRequestID: "latest", IdempotencyKey: "replacement",
		})
		if !errors.Is(err, revision.ErrLatestTurnReplacementUnavailable) {
			t.Fatalf("unassociated track error = %v", err)
		}
	})
}

func testRequestIDReuse(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "reuse")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	for i := 1; i <= 2; i++ {
		invocationID := fmt.Sprintf("invocation-%d", i)
		turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID:    "same-request",
			InvocationID: invocationID,
		})
		requireNoError(t, service.AppendEvent(
			turnCtx,
			sess,
			messageEvent(fmt.Sprintf("turn-%d", i), "same-request", invocationID, model.RoleUser),
		))
		requireNoError(t, service.AppendEvent(
			ctx,
			sess,
			completionEvent("same-request", invocationID),
		))
	}
	result, err := revision.ReplaceLatestTurn(ctx, service, revision.LatestTurnReplacementRequest{
		Key:               key,
		ExpectedRequestID: "same-request",
		IdempotencyKey:    "second-turn-replacement",
	})
	requireNoError(t, err)
	if len(result.ActiveSession.Events) != 1 ||
		result.ActiveSession.Events[0].ID != "turn-1" {
		t.Fatalf("restored repeated-request events = %#v", result.ActiveSession.Events)
	}
}

func createStartedTurn(
	t *testing.T,
	service session.Service,
	suffix string,
) (session.Key, *session.Session) {
	t.Helper()
	ctx := context.Background()
	key := contractKey(t, suffix)
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "latest",
		InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(
		turnCtx,
		sess,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	return key, sess
}

func contractKey(t *testing.T, suffix string) session.Key {
	t.Helper()
	id := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	return session.Key{
		AppName:   "replacement-contract",
		UserID:    "user",
		SessionID: id + "-" + suffix,
	}
}

func messageEvent(
	id string,
	requestID string,
	invocationID string,
	role model.Role,
) *event.Event {
	evt := event.NewResponseEvent(invocationID, "author", &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: role, Content: id},
		}},
	})
	evt.ID = id
	evt.RequestID = requestID
	return evt
}

func completionEvent(requestID, invocationID string) *event.Event {
	evt := event.NewResponseEvent(invocationID, "app", &model.Response{
		Done:   true,
		Object: model.ObjectTypeRunnerCompletion,
	})
	evt.RequestID = requestID
	return evt
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
