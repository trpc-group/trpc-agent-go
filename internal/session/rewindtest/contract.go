//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rewindtest provides the shared rewind contract for session backend
// tests.
package rewindtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Run exercises the contract shared by every backend that supports rewind.
func Run(t *testing.T, service session.Service) {
	t.Helper()
	t.Run("invalid request", func(t *testing.T) {
		testInvalidRequest(t, service)
	})
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
	t.Run("turn-start restore state", func(t *testing.T) {
		testTurnStartRestoreState(t, service)
	})
	t.Run("shared state retained", func(t *testing.T) {
		testSharedStateRetained(t, service)
	})
	t.Run("listed projection fencing", func(t *testing.T) {
		testListedProjectionFencing(t, service)
	})
	t.Run("first replacement write fencing", func(t *testing.T) {
		testFirstReplacementWriteFencing(t, service)
	})
	t.Run("concurrent rewind identities", func(t *testing.T) {
		testConcurrentRewindIdentities(t, service)
	})
	t.Run("concurrent idempotent retries", func(t *testing.T) {
		testConcurrentIdempotentRetries(t, service)
	})
}

func testInvalidRequest(t *testing.T, service session.Service) {
	t.Helper()
	rewinder, ok := service.(session.RewindService)
	if !ok {
		t.Fatal("service does not implement RewindService")
	}
	key := contractKey(t, "invalid-request")
	for _, req := range []session.RewindRequest{
		{},
		{Key: key, ExpectedHeadRequestID: "head", IdempotencyKey: "idempotency"},
		{Key: key, TargetRequestID: "target", IdempotencyKey: "idempotency"},
		{Key: key, TargetRequestID: "target", ExpectedHeadRequestID: "head"},
	} {
		_, err := rewinder.Rewind(context.Background(), req)
		if !errors.Is(err, session.ErrInvalidRewindRequest) {
			t.Fatalf("Rewind(%+v) error = %v, want ErrInvalidRewindRequest", req, err)
		}
	}

	sess, err := service.CreateSession(context.Background(), key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(
		context.Background(),
		sess,
		messageEvent("before", "before", "before-invocation", model.RoleUser),
	))
	turnCtx := revision.ContextWithTurnStart(
		context.Background(),
		revision.TurnStart{RequestID: "latest", InvocationID: "latest-invocation"},
	)
	requireNoError(t, service.AppendEvent(
		turnCtx,
		sess,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	requireNoError(t, service.AppendEvent(
		context.Background(),
		sess,
		completionEvent("latest", "latest-invocation"),
	))
	beforeInvalid, err := service.GetSession(context.Background(), key)
	requireNoError(t, err)
	beforeBoundary, err := revision.NewBoundary(beforeInvalid)
	requireNoError(t, err)
	_, err = rewinder.Rewind(context.Background(), session.RewindRequest{
		Key: key, TargetRequestID: "latest", IdempotencyKey: "replacement",
	})
	if !errors.Is(err, session.ErrInvalidRewindRequest) {
		t.Fatalf("invalid Rewind() error = %v, want ErrInvalidRewindRequest", err)
	}
	afterInvalid, err := service.GetSession(context.Background(), key)
	requireNoError(t, err)
	afterBoundary, err := revision.NewBoundary(afterInvalid)
	requireNoError(t, err)
	if !bytes.Equal(afterBoundary, beforeBoundary) {
		t.Fatal("invalid Rewind() changed the active session projection")
	}
	result, err := rewinder.Rewind(context.Background(), session.RewindRequest{
		Key: key, TargetRequestID: "latest",
		ExpectedHeadRequestID: "latest", IdempotencyKey: "replacement",
	})
	requireNoError(t, err)
	if result == nil || result.Session == nil || findEvent(result.Session.Events, "latest") != nil {
		t.Fatalf("valid Rewind() after invalid request = %#v", result)
	}
}

func testConcurrentIdempotentRetries(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "concurrent-idempotent-retries")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(ctx, sess, messageEvent(
		"before", "before", "before-invocation", model.RoleUser,
	)))
	latestCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID: "latest", InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(latestCtx, sess, messageEvent(
		"latest", "latest", "latest-invocation", model.RoleUser,
	)))
	requireNoError(t, service.AppendEvent(
		ctx, sess, completionEvent("latest", "latest-invocation"),
	))

	request := revision.RewindRequest{
		Key: key, TargetRequestID: "latest",
		ExpectedHeadRequestID: "latest", IdempotencyKey: "replacement",
	}
	start := make(chan struct{})
	results := make(chan *session.RewindResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := revision.Rewind(ctx, service, request)
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		requireNoError(t, err)
	}
	for result := range results {
		if result == nil || result.Session == nil {
			t.Fatal("concurrent idempotent retry returned a nil result")
		}
		if findEvent(result.Session.Events, "before") == nil {
			t.Fatal("concurrent idempotent retry omitted the retained event")
		}
		if findEvent(result.Session.Events, "latest") != nil {
			t.Fatal("concurrent idempotent retry retained the rewound event")
		}
	}
}

func testConcurrentRewindIdentities(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "concurrent-rewind")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(ctx, sess, messageEvent(
		"before", "before", "before-invocation", model.RoleUser,
	)))
	latestCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID: "latest", InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(latestCtx, sess, messageEvent(
		"latest", "latest", "latest-invocation", model.RoleUser,
	)))
	requireNoError(t, service.AppendEvent(
		ctx, sess, completionEvent("latest", "latest-invocation"),
	))
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, idempotencyKey := range []string{"replacement-a", "replacement-b"} {
		wg.Add(1)
		go func(idempotencyKey string) {
			defer wg.Done()
			<-start
			_, err := revision.Rewind(ctx, service, revision.RewindRequest{
				Key: key, TargetRequestID: "latest",
				ExpectedHeadRequestID: "latest", IdempotencyKey: idempotencyKey,
			})
			errs <- err
		}(idempotencyKey)
	}
	close(start)
	wg.Wait()
	close(errs)
	var succeeded, conflicted int
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, session.ErrRewindConflict):
			conflicted++
		default:
			t.Fatalf("concurrent rewind error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent rewinds: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func testFirstReplacementWriteFencing(t *testing.T, service session.Service) {
	t.Run("ordinary event append conflicts", func(t *testing.T) {
		ctx := context.Background()
		key := contractKey(t, "ordinary-first-write-conflict")
		fenced := prepareRewindResult(t, ctx, service, key)

		concurrent, err := service.GetSession(ctx, key)
		requireNoError(t, err)
		concurrentCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID: "concurrent", InvocationID: "concurrent-invocation",
		})
		requireNoError(t, service.AppendEvent(
			concurrentCtx,
			concurrent,
			messageEvent(
				"concurrent", "concurrent", "concurrent-invocation", model.RoleUser,
			),
		))

		err = service.AppendEvent(
			ctx,
			fenced,
			messageEvent("edit", "edit", "edit-invocation", model.RoleUser),
		)
		if !errors.Is(err, session.ErrRewindConflict) {
			t.Fatalf("ordinary edited first append error = %v", err)
		}
		if findEvent(fenced.Events, "edit") != nil {
			t.Fatal("conflicting ordinary event mutated the fenced session")
		}
	})

	t.Run("concurrent turn conflicts", func(t *testing.T) {
		ctx := context.Background()
		key := contractKey(t, "first-write-conflict")
		fenced := prepareRewindResult(t, ctx, service, key)

		concurrent, err := service.GetSession(ctx, key)
		requireNoError(t, err)
		concurrentCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID: "concurrent", InvocationID: "concurrent-invocation",
		})
		requireNoError(t, service.AppendEvent(
			concurrentCtx,
			concurrent,
			messageEvent(
				"concurrent", "concurrent", "concurrent-invocation", model.RoleUser,
			),
		))

		editCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID: "edit", InvocationID: "edit-invocation",
		})
		err = service.AppendEvent(
			editCtx,
			fenced,
			messageEvent("edit", "edit", "edit-invocation", model.RoleUser),
		)
		if !errors.Is(err, session.ErrRewindConflict) {
			t.Fatalf("edited first append error = %v", err)
		}
		if findEvent(fenced.Events, "edit") != nil {
			t.Fatal("conflicting edited event mutated the fenced session")
		}

		active, err := service.GetSession(ctx, key)
		requireNoError(t, err)
		if findEvent(active.Events, "edit") != nil {
			t.Fatal("conflicting edited event was persisted")
		}
		if findEvent(active.Events, "concurrent") == nil {
			t.Fatal("concurrent event is missing")
		}
	})

	t.Run("successful first append consumes fence", func(t *testing.T) {
		ctx := context.Background()
		key := contractKey(t, "first-write-success")
		fenced := prepareRewindResult(t, ctx, service, key)
		editCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID: "edit", InvocationID: "edit-invocation",
		})
		requireNoError(t, service.AppendEvent(
			editCtx,
			fenced,
			messageEvent("edit", "edit", "edit-invocation", model.RoleUser),
		))
		requireNoError(t, service.AppendEvent(
			ctx,
			fenced,
			messageEvent("edit-followup", "edit", "edit-invocation", model.RoleAssistant),
		))
		requireNoError(t, service.AppendEvent(
			ctx, fenced, completionEvent("edit", "edit-invocation"),
		))
		nextCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID: "next", InvocationID: "next-invocation",
		})
		requireNoError(t, service.AppendEvent(
			nextCtx,
			fenced,
			messageEvent("next", "next", "next-invocation", model.RoleUser),
		))
	})
}

func prepareRewindResult(
	t *testing.T,
	ctx context.Context,
	service session.Service,
	key session.Key,
) *session.Session {
	t.Helper()
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(ctx, sess, messageEvent(
		"before", "before", "before-invocation", model.RoleUser,
	)))
	latestCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID: "latest", InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(latestCtx, sess, messageEvent(
		"latest", "latest", "latest-invocation", model.RoleUser,
	)))
	requireNoError(t, service.AppendEvent(
		ctx, sess, completionEvent("latest", "latest-invocation"),
	))
	result, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key: key, TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
		IdempotencyKey: "edit",
	})
	requireNoError(t, err)
	return result.Session
}

func findEvent(events []event.Event, id string) *event.Event {
	for i := range events {
		if events[i].ID == id {
			return &events[i]
		}
	}
	return nil
}

func sessionsEqual(a, b *session.Session) bool {
	if a == nil || b == nil {
		return a == b
	}
	aJSON, aErr := json.Marshal(a)
	bJSON, bErr := json.Marshal(b)
	return aErr == nil && bErr == nil && bytes.Equal(aJSON, bJSON)
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
		trackCtx := revision.ContextWithRequestID(ctx, "latest")
		requireNoError(t, trackService.AppendTrackEvent(trackCtx, sess, &session.TrackEvent{
			Track:     "ui",
			Payload:   []byte(`{"value":"latest"}`),
			Timestamp: time.Now(),
		}))
	}

	result, err := revision.Rewind(
		ctx,
		service,
		revision.RewindRequest{
			Key:             key,
			TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
			IdempotencyKey: "replacement",
		},
	)
	requireNoError(t, err)
	if len(result.Session.Events) != 1 ||
		result.Session.Events[0].ID != "before" {
		t.Fatalf("restored async events = %#v", result.Session.Events)
	}
	phase, ok := result.Session.GetState("phase")
	if !ok || string(phase) != "before" {
		t.Fatalf("restored async phase = %q, %v", phase, ok)
	}
	if hasTracks {
		tracks, err := result.Session.GetTrackEvents("ui")
		requireNoError(t, err)
		if len(tracks.Events) != 1 ||
			string(tracks.Events[0].Payload) != `{"value":"before"}` {
			t.Fatalf("restored async tracks = %#v", tracks.Events)
		}
	}
}

// RunSoftDeletedSummaryHistory verifies that replacement restores the active
// summary projection without removing summaries retained by an earlier
// soft-deleted session with the same key. The service must have an active
// summarizer, and countDeletedSummaries must count tombstoned summary rows for
// the supplied key.
func RunSoftDeletedSummaryHistory(
	t *testing.T,
	service session.Service,
	countDeletedSummaries func(session.Key) (int, error),
) {
	t.Helper()
	ctx := context.Background()
	key := contractKey(t, "soft-deleted-summary-history")

	oldSession, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(ctx, oldSession, messageEvent(
		"old-baseline", "old-baseline", "old-baseline-invocation", model.RoleUser,
	)))
	requireNoError(t, service.CreateSessionSummary(
		ctx, oldSession, session.SummaryFilterKeyAllContents, true,
	))
	requireNoError(t, service.DeleteSession(ctx, key))
	deleted, err := countDeletedSummaries(key)
	requireNoError(t, err)
	if deleted != 1 {
		t.Fatalf("soft-deleted summaries before recreation = %d, want 1", deleted)
	}

	active, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(ctx, active, messageEvent(
		"new-baseline", "new-baseline", "new-baseline-invocation", model.RoleUser,
	)))
	requireNoError(t, service.CreateSessionSummary(
		ctx, active, session.SummaryFilterKeyAllContents, true,
	))
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "latest",
		InvocationID: "latest-invocation",
	})
	requireNoError(t, service.AppendEvent(
		turnCtx,
		active,
		messageEvent("latest", "latest", "latest-invocation", model.RoleUser),
	))
	result, err := revision.Rewind(
		ctx,
		service,
		revision.RewindRequest{
			Key:             key,
			TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
			IdempotencyKey: "replacement",
		},
	)
	requireNoError(t, err)
	if result.Session.Summaries[session.SummaryFilterKeyAllContents] == nil {
		t.Fatal("restored active summary is missing")
	}
	deleted, err = countDeletedSummaries(key)
	requireNoError(t, err)
	if deleted != 1 {
		t.Fatalf("soft-deleted summaries after replacement = %d, want 1", deleted)
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

	result, err := revision.Rewind(
		ctx,
		service,
		revision.RewindRequest{
			Key:             key,
			TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
			IdempotencyKey: "replacement",
		},
	)
	requireNoError(t, err)
	if len(result.Session.Events) != 1 ||
		result.Session.Events[0].ID != "before" {
		t.Fatalf("restored unfinished events = %#v", result.Session.Events)
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
	_, err = revision.Rewind(ctx, service, revision.RewindRequest{
		Key: key, TargetRequestID: "latest", ExpectedHeadRequestID: "latest", IdempotencyKey: "replacement",
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
	result, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             key,
		TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
		IdempotencyKey: "replacement",
	})
	requireNoError(t, err)
	if len(result.Session.Events) != 1 ||
		result.Session.Events[0].ID != "concurrent" {
		t.Fatalf("restored authoritative events = %#v", result.Session.Events)
	}
}

func testTurnStartRestoreState(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "turn-start-restore-state")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		messageEvent("before", "before", "before-invocation", model.RoleUser),
	))

	const restoredKey = "consumed-route"
	turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
		RequestID:    "latest",
		InvocationID: "latest-invocation",
		RestoreState: session.StateMap{restoredKey: []byte("parent.child")},
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

	active, err := service.GetSession(ctx, key)
	requireNoError(t, err)
	if _, ok := active.GetState(restoredKey); ok {
		t.Fatalf("consumed state leaked into the active projection")
	}

	result, err := revision.Rewind(
		ctx,
		service,
		revision.RewindRequest{
			Key:             key,
			TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
			IdempotencyKey: "replacement",
		},
	)
	requireNoError(t, err)
	restored, ok := result.Session.GetState(restoredKey)
	if !ok || string(restored) != "parent.child" {
		t.Fatalf("restored turn-start state = %q, %v", restored, ok)
	}
}

func testSharedStateRetained(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "shared-state-retained")
	appStateKey := session.StateAppPrefix + "shared"
	userStateKey := session.StateUserPrefix + "private"
	sess, err := service.CreateSession(ctx, key, session.StateMap{
		"phase": []byte("before"),
	})
	requireNoError(t, err)
	requireNoError(t, service.UpdateAppState(ctx, key.AppName, session.StateMap{
		"shared": []byte("app-before"),
	}))
	requireNoError(t, service.UpdateUserState(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}, session.StateMap{
		"private": []byte("user-before"),
	}))
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
	stateEvent := messageEvent(
		"state", "latest", "latest-invocation", model.RoleAssistant,
	)
	stateEvent.StateDelta = session.StateMap{
		"phase":      []byte("after"),
		appStateKey:  []byte("app-after"),
		userStateKey: []byte("user-after"),
	}
	requireNoError(t, service.AppendEvent(ctx, sess, stateEvent))
	requireNoError(t, service.UpdateAppState(ctx, key.AppName, session.StateMap{
		"shared": []byte("app-after"),
	}))
	requireNoError(t, service.UpdateUserState(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}, session.StateMap{
		"private": []byte("user-after"),
	}))
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		completionEvent("latest", "latest-invocation"),
	))

	result, err := revision.Rewind(
		ctx,
		service,
		revision.RewindRequest{
			Key:             key,
			TargetRequestID: "latest", ExpectedHeadRequestID: "latest",
			IdempotencyKey: "replacement",
		},
	)
	requireNoError(t, err)
	assertStateValue(t, result.Session, "phase", "before")
	assertStateValue(t, result.Session, appStateKey, "app-after")
	assertStateValue(t, result.Session, userStateKey, "user-after")
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
		trackCtx := revision.ContextWithRequestID(ctx, "latest")
		requireNoError(t, trackService.AppendTrackEvent(trackCtx, sess, &session.TrackEvent{
			Track:     "ui",
			Payload:   []byte(`{"value":"latest"}`),
			Timestamp: time.Now(),
		}))
	}
	requireNoError(t, service.AppendEvent(
		ctx,
		sess,
		completionEvent("latest", "latest-invocation"),
	))

	req := revision.RewindRequest{
		Key:                   key,
		TargetRequestID:       "latest",
		ExpectedHeadRequestID: "latest",
		IdempotencyKey:        "replacement",
	}
	if _, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key:                   key,
		TargetRequestID:       "before",
		ExpectedHeadRequestID: "latest",
		IdempotencyKey:        "older-target",
	}); !errors.Is(err, revision.ErrRewindUnavailable) {
		t.Fatalf("older target error = %v", err)
	}
	if _, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key:                   key,
		TargetRequestID:       "latest",
		ExpectedHeadRequestID: "other",
		IdempotencyKey:        "stale-head",
	}); !errors.Is(err, revision.ErrRewindConflict) {
		t.Fatalf("stale head error = %v", err)
	}
	result, err := revision.Rewind(ctx, service, req)
	requireNoError(t, err)
	if len(result.Session.Events) != 1 ||
		result.Session.Events[0].ID != "before" {
		t.Fatalf("restored events = %#v", result.Session.Events)
	}
	phase, ok := result.Session.GetState("phase")
	if !ok || string(phase) != "before" {
		t.Fatalf("restored phase = %q, %v", phase, ok)
	}
	if hasTracks {
		tracks, err := result.Session.GetTrackEvents("ui")
		requireNoError(t, err)
		if len(tracks.Events) != 1 || string(tracks.Events[0].Payload) != `{"value":"before"}` {
			t.Fatalf("restored tracks = %#v", tracks.Events)
		}
	}

	replay, err := revision.Rewind(ctx, service, req)
	requireNoError(t, err)
	if !sessionsEqual(result.Session, replay.Session) {
		t.Fatalf("idempotent replay changed projection: %#v", replay.Session)
	}
	if _, err := revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             key,
		TargetRequestID: "other", ExpectedHeadRequestID: "other",
		IdempotencyKey: req.IdempotencyKey,
	}); !errors.Is(err, revision.ErrRewindConflict) {
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
	requireNoError(t, service.AppendEvent(ctx, result.Session, messageEvent(
		"after",
		"after",
		"after-invocation",
		model.RoleAssistant,
	)))
	if _, err := revision.Rewind(ctx, service, req); !errors.Is(
		err,
		revision.ErrRewindConflict,
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
		_, err := revision.Rewind(ctx, service, revision.RewindRequest{
			Key: key, TargetRequestID: "latest", ExpectedHeadRequestID: "latest", IdempotencyKey: "replacement",
		})
		if !errors.Is(err, revision.ErrRewindUnavailable) {
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
		_, err := revision.Rewind(ctx, service, revision.RewindRequest{
			Key: key, TargetRequestID: "latest", ExpectedHeadRequestID: "latest", IdempotencyKey: "replacement",
		})
		if !errors.Is(err, revision.ErrRewindUnavailable) {
			t.Fatalf("unassociated track error = %v", err)
		}
	})
}

func testRequestIDReuse(t *testing.T, service session.Service) {
	ctx := context.Background()
	key := contractKey(t, "reuse")
	sess, err := service.CreateSession(ctx, key, nil)
	requireNoError(t, err)
	requestIDs := []string{"same-request", "middle-request", "same-request"}
	for i, requestID := range requestIDs {
		invocationID := fmt.Sprintf("invocation-%d", i+1)
		turnCtx := revision.ContextWithTurnStart(ctx, revision.TurnStart{
			RequestID:    requestID,
			InvocationID: invocationID,
		})
		requireNoError(t, service.AppendEvent(
			turnCtx,
			sess,
			messageEvent(fmt.Sprintf("turn-%d", i+1), requestID, invocationID, model.RoleUser),
		))
		requireNoError(t, service.AppendEvent(
			ctx,
			sess,
			completionEvent(requestID, invocationID),
		))
	}
	before, err := service.GetSession(ctx, key)
	requireNoError(t, err)
	_, err = revision.Rewind(ctx, service, revision.RewindRequest{
		Key:             key,
		TargetRequestID: "same-request", ExpectedHeadRequestID: "same-request",
		IdempotencyKey: "second-turn-replacement",
	})
	if !errors.Is(err, revision.ErrRewindUnavailable) {
		t.Fatalf("reused request ID rewind error = %v", err)
	}
	after, err := service.GetSession(ctx, key)
	requireNoError(t, err)
	if !sessionsEqual(before, after) {
		t.Fatalf("failed reused-request rewind changed projection: before=%#v after=%#v", before, after)
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

func assertStateValue(
	t *testing.T,
	sess *session.Session,
	key string,
	want string,
) {
	t.Helper()
	got, ok := sess.GetState(key)
	if !ok || string(got) != want {
		t.Fatalf("state %q = %q, %v; want %q", key, got, ok, want)
	}
}
