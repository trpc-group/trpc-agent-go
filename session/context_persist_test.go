//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session_test

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/noop"
)

func newPersistTestEvent(id string) event.Event {
	return event.Event{
		Response: &model.Response{},
		ID:       id,
		Author:   "test",
	}
}

type failingMaskedPersistService struct {
	*noop.Service
}

func (f *failingMaskedPersistService) UpdateSessionState(
	context.Context,
	session.Key,
	session.StateMap,
) error {
	return errors.New("persist failed")
}

func TestPersistMaskedEvents(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "persist-1"}

	t.Run("without session service updates local state only", func(t *testing.T) {
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
		sess.Events = []event.Event{
			newPersistTestEvent("e1"),
			newPersistTestEvent("e2"),
		}
		sess.MaskEvents("e1")

		if err := sess.PersistMaskedEvents(ctx, nil, key); err != nil {
			t.Fatal(err)
		}
		stored, ok := sess.GetState(session.MaskedEventsStateKey)
		if !ok {
			t.Fatal("expected masked events in local state")
		}
		if string(stored) != `["e1"]` {
			t.Fatalf("unexpected payload: %s", stored)
		}
	})

	t.Run("with session service updates local state after remote write", func(t *testing.T) {
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
		sess.Events = []event.Event{
			newPersistTestEvent("e1"),
			newPersistTestEvent("e2"),
		}
		sess.MaskEvents("e2")
		svc := noop.NewService()

		if err := sess.PersistMaskedEvents(ctx, svc, key); err != nil {
			t.Fatal(err)
		}
		stored, ok := sess.GetState(session.MaskedEventsStateKey)
		if !ok || string(stored) != `["e2"]` {
			t.Fatalf("expected persisted mask in state, got %q ok=%v", stored, ok)
		}
	})

	t.Run("does not write local state when remote persist fails", func(t *testing.T) {
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
		sess.Events = []event.Event{newPersistTestEvent("e1")}
		sess.MaskEvents("e1")
		svc := &failingMaskedPersistService{Service: noop.NewService()}

		err := sess.PersistMaskedEvents(ctx, svc, key)
		if err == nil {
			t.Fatal("expected error")
		}
		if _, ok := sess.GetState(session.MaskedEventsStateKey); ok {
			t.Fatal("local state should not be updated on remote failure")
		}
	})

	t.Run("retains mask IDs for events outside loaded window", func(t *testing.T) {
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
		sess.Events = []event.Event{
			newPersistTestEvent("e1"),
			newPersistTestEvent("e2"),
		}
		sess.MaskEvents("e1", "e2")
		sess.Events = []event.Event{newPersistTestEvent("e2")}

		if err := sess.PersistMaskedEvents(ctx, nil, key); err != nil {
			t.Fatal(err)
		}
		stored, ok := sess.GetState(session.MaskedEventsStateKey)
		if !ok {
			t.Fatal("expected masked events in local state")
		}
		if string(stored) != `["e1","e2"]` {
			t.Fatalf("expected masks retained for unloaded events, got %s", stored)
		}
	})

	t.Run("nil session", func(t *testing.T) {
		var sess *session.Session
		if err := sess.PersistMaskedEvents(ctx, nil, key); err != nil {
			t.Fatal(err)
		}
	})
}
