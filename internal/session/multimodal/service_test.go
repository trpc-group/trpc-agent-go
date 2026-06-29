//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package multimodal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactmem "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionmem "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestAppendEventExternalizesAndGetSessionHydrates(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	originalData := []byte("image-bytes")
	evt := imageEvent(originalData)
	if err := svc.AppendEvent(ctx, sess, evt); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	// The runtime event remains untouched.
	gotRuntimeData := evt.Response.Choices[0].Message.ContentParts[0].Image.Data
	if string(gotRuntimeData) != string(originalData) {
		t.Fatalf("runtime data = %q, want %q", gotRuntimeData, originalData)
	}
	if len(sess.Events) != 1 {
		t.Fatalf("live session events = %d, want 1", len(sess.Events))
	}
	livePart := sess.Events[0].Response.Choices[0].Message.ContentParts[0]
	if string(livePart.Image.Data) != string(originalData) {
		t.Fatalf("live session data = %q, want %q", livePart.Image.Data, originalData)
	}
	if livePart.ContentRef != nil {
		t.Fatalf("live session ContentRef = %#v, want nil", livePart.ContentRef)
	}

	persisted, err := inner.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("inner.GetSession() error = %v", err)
	}
	part := persisted.Events[0].Response.Choices[0].Message.ContentParts[0]
	if len(part.Image.Data) != 0 {
		t.Fatalf("persisted image data length = %d, want 0", len(part.Image.Data))
	}
	if part.ContentRef == nil {
		t.Fatal("persisted ContentRef is nil")
	}
	if !strings.HasPrefix(part.ContentRef.ArtifactName, "sessionpart_") {
		t.Fatalf("artifact name = %q, want sessionpart_ prefix", part.ContentRef.ArtifactName)
	}
	if !strings.HasPrefix(part.ContentRef.ArtifactRef, "artifact://") {
		t.Fatalf("artifact ref = %q, want artifact:// prefix", part.ContentRef.ArtifactRef)
	}
	if part.ContentRef.ArtifactVersion != 0 {
		t.Fatalf("artifact version = %d, want 0", part.ContentRef.ArtifactVersion)
	}
	if persisted.Events[0].ID != evt.ID {
		t.Fatalf("persisted event ID = %q, want %q", persisted.Events[0].ID, evt.ID)
	}

	hydrated, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	hydratedPart := hydrated.Events[0].Response.Choices[0].Message.ContentParts[0]
	if string(hydratedPart.Image.Data) != string(originalData) {
		t.Fatalf("hydrated data = %q, want %q", hydratedPart.Image.Data, originalData)
	}

	// Hydration should not write bytes back into the persisted view.
	persistedAgain, err := inner.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("inner.GetSession() after hydrate error = %v", err)
	}
	persistedPart := persistedAgain.Events[0].Response.Choices[0].Message.ContentParts[0]
	if len(persistedPart.Image.Data) != 0 {
		t.Fatalf("persisted image data after hydrate length = %d, want 0", len(persistedPart.Image.Data))
	}

	windowSvc, ok := svc.(session.WindowService)
	if !ok {
		t.Fatal("wrapped service does not implement WindowService")
	}
	window, err := windowSvc.GetEventWindow(ctx, session.EventWindowRequest{
		Key:           key,
		AnchorEventID: persisted.Events[0].ID,
		Before:        0,
		After:         0,
	})
	if err != nil {
		t.Fatalf("GetEventWindow() error = %v", err)
	}
	if len(window.Entries) != 1 {
		t.Fatalf("window entries = %d, want 1", len(window.Entries))
	}
	windowPart := window.Entries[0].Event.Response.Choices[0].Message.ContentParts[0]
	if string(windowPart.Image.Data) != string(originalData) {
		t.Fatalf("window hydrated data = %q, want %q", windowPart.Image.Data, originalData)
	}
}

func TestListSessionsHydratesFullSessionResults(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	originalData := []byte("image-bytes")
	if err := svc.AppendEvent(ctx, sess, imageEvent(originalData)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	list, err := svc.ListSessions(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListSessions() len = %d, want 1", len(list))
	}
	part := list[0].Events[0].Response.Choices[0].Message.ContentParts[0]
	if string(part.Image.Data) != string(originalData) {
		t.Fatalf("listed hydrated data = %q, want %q", part.Image.Data, originalData)
	}

	metaList, err := svc.ListSessions(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	}, session.WithListSessionOnlyMeta())
	if err != nil {
		t.Fatalf("ListSessions(meta) error = %v", err)
	}
	if len(metaList) != 1 {
		t.Fatalf("ListSessions(meta) len = %d, want 1", len(metaList))
	}
	if len(metaList[0].Events) != 0 {
		t.Fatalf("metadata-only events = %d, want 0", len(metaList[0].Events))
	}
}

func TestAppendEventFailsClosedWithoutArtifactService(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	svc := Wrap(inner, nil, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	err = svc.AppendEvent(ctx, sess, imageEvent([]byte("image-bytes")))
	if err == nil {
		t.Fatal("AppendEvent() error = nil, want error")
	}
	persisted, getErr := inner.GetSession(ctx, key)
	if getErr != nil {
		t.Fatalf("inner.GetSession() error = %v", getErr)
	}
	if len(persisted.Events) != 0 {
		t.Fatalf("persisted events = %d, want 0", len(persisted.Events))
	}
}

func TestAppendEventSkipsPartialEventExternalization(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	evt := imageEvent([]byte("image-bytes"))
	evt.IsPartial = true

	if err := svc.AppendEvent(ctx, sess, evt); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	keys, listErr := artifacts.ListArtifactKeys(ctx, artifactSessionInfo(key))
	if listErr != nil {
		t.Fatalf("ListArtifactKeys() error = %v", listErr)
	}
	if len(keys) != 0 {
		t.Fatalf("artifact keys = %v, want empty for partial event", keys)
	}
	if len(sess.Events) != 0 {
		t.Fatalf("live session events = %d, want 0 for partial event", len(sess.Events))
	}
}

func TestAppendEventHookSkipNextDoesNotUpdateLiveSession(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService(
		sessionmem.WithAppendEventHook(
			func(ctx *session.AppendEventContext, next func() error) error {
				return nil
			},
		),
	)
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if err := svc.AppendEvent(ctx, sess, imageEvent([]byte("image-bytes"))); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if len(sess.Events) != 0 {
		t.Fatalf("live session events = %d, want 0 when hook skips next", len(sess.Events))
	}
	persisted, err := inner.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("inner.GetSession() error = %v", err)
	}
	if len(persisted.Events) != 0 {
		t.Fatalf("persisted events = %d, want 0 when hook skips next", len(persisted.Events))
	}
	keys, listErr := artifacts.ListArtifactKeys(ctx, artifactSessionInfo(key))
	if listErr != nil {
		t.Fatalf("ListArtifactKeys() error = %v", listErr)
	}
	if len(keys) != 0 {
		t.Fatalf("artifact keys = %v, want empty after hook skips next", keys)
	}
}

func TestAppendEventFailureDeletesSavedArtifacts(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	base := sessionmem.NewSessionService()
	sess, err := base.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	inner := &appendFailService{
		Service: base,
		err:     errors.New("append failed"),
	}
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})

	err = svc.AppendEvent(ctx, sess, imageEvent([]byte("image-bytes")))
	if !errors.Is(err, inner.err) {
		t.Fatalf("AppendEvent() error = %v, want %v", err, inner.err)
	}
	keys, listErr := artifacts.ListArtifactKeys(ctx, artifactSessionInfo(key))
	if listErr != nil {
		t.Fatalf("ListArtifactKeys() error = %v", listErr)
	}
	if len(keys) != 0 {
		t.Fatalf("artifact keys = %v, want empty after cleanup", keys)
	}
}

func TestWrapPreservesOptionalInterfaces(t *testing.T) {
	inner := sessionmem.NewSessionService()
	wrapped := Wrap(inner, artifactmem.NewService(), Config{Enabled: true})
	if _, ok := wrapped.(session.TrackService); !ok {
		t.Fatal("wrapped inmemory service does not implement TrackService")
	}
	if _, ok := wrapped.(session.WindowService); !ok {
		t.Fatal("wrapped inmemory service does not implement WindowService")
	}
	if _, ok := wrapped.(session.SearchableService); ok {
		t.Fatal("wrapped inmemory service unexpectedly implements SearchableService")
	}

	searchInner := &searchOnlyService{Service: inner}
	searchWrapped := Wrap(searchInner, artifactmem.NewService(), Config{Enabled: true})
	if _, ok := searchWrapped.(session.SearchableService); !ok {
		t.Fatal("wrapped searchable service does not implement SearchableService")
	}
	if _, ok := searchWrapped.(session.TrackService); ok {
		t.Fatal("wrapped search-only service unexpectedly implements TrackService")
	}
}

type appendFailService struct {
	session.Service
	err error
}

func (s *appendFailService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	return s.err
}

type searchOnlyService struct {
	session.Service
}

func (s *searchOnlyService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return nil, nil
}

func imageEvent(data []byte) *event.Event {
	msg := model.NewUserMessage("image")
	msg.AddImageData(data, "high", "png")
	return event.NewResponseEvent("invocation", "user", &model.Response{
		Choices: []model.Choice{
			{
				Index:   0,
				Message: msg,
			},
		},
	})
}

func artifactSessionInfo(key session.Key) artifact.SessionInfo {
	return artifact.SessionInfo{
		AppName:   key.AppName,
		UserID:    key.UserID,
		SessionID: key.SessionID,
	}
}
