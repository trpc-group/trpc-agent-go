//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package externalization

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
	if hydratedPart.ContentRef != nil {
		t.Fatalf("hydrated ContentRef = %#v, want nil", hydratedPart.ContentRef)
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

func TestAppendEventExternalizesAndHydratesAudioAndFile(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	audioData := []byte("audio-bytes")
	fileData := []byte("file-bytes")
	msg := model.NewUserMessage("content")
	msg.AddAudioData(audioData, "mp3")
	msg.AddFileData("report.pdf", fileData, "application/pdf")
	evt := responseEvent(msg)
	if err := svc.AppendEvent(ctx, sess, evt); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	persisted, err := inner.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("inner.GetSession() error = %v", err)
	}
	audioPart := persisted.Events[0].Response.Choices[0].Message.ContentParts[0]
	if len(audioPart.Audio.Data) != 0 {
		t.Fatalf("persisted audio data length = %d, want 0", len(audioPart.Audio.Data))
	}
	if audioPart.ContentRef == nil || audioPart.ContentRef.MimeType != "audio/mp3" {
		t.Fatalf("audio ContentRef = %#v, want audio/mp3", audioPart.ContentRef)
	}
	filePart := persisted.Events[0].Response.Choices[0].Message.ContentParts[1]
	if len(filePart.File.Data) != 0 {
		t.Fatalf("persisted file data length = %d, want 0", len(filePart.File.Data))
	}
	if filePart.ContentRef == nil || filePart.ContentRef.OriginalName != "report.pdf" {
		t.Fatalf("file ContentRef = %#v, want original name report.pdf", filePart.ContentRef)
	}
	if filePart.ContentRef.MimeType != "application/pdf" {
		t.Fatalf("file ContentRef MIME = %q, want application/pdf", filePart.ContentRef.MimeType)
	}

	hydrated, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	hydratedParts := hydrated.Events[0].Response.Choices[0].Message.ContentParts
	if string(hydratedParts[0].Audio.Data) != string(audioData) {
		t.Fatalf("hydrated audio data = %q, want %q", hydratedParts[0].Audio.Data, audioData)
	}
	if hydratedParts[0].Audio.Format != "mp3" {
		t.Fatalf("hydrated audio format = %q, want mp3", hydratedParts[0].Audio.Format)
	}
	if string(hydratedParts[1].File.Data) != string(fileData) {
		t.Fatalf("hydrated file data = %q, want %q", hydratedParts[1].File.Data, fileData)
	}
	if hydratedParts[1].File.Name != "report.pdf" {
		t.Fatalf("hydrated file name = %q, want report.pdf", hydratedParts[1].File.Name)
	}
}

func TestAppendEventExternalizesDataURLs(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	msg := model.NewUserMessage("data URLs")
	msg.AddImageURL("data:image/png;base64,aW1hZ2UtZGF0YQ==", "high")
	msg.AddFileURL("note.txt", "data:text/plain,note%20data", "")
	if err := svc.AppendEvent(ctx, sess, responseEvent(msg)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	persisted, err := inner.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("inner.GetSession() error = %v", err)
	}
	parts := persisted.Events[0].Response.Choices[0].Message.ContentParts
	if parts[0].Image.URL != "" {
		t.Fatalf("persisted image URL = %q, want empty", parts[0].Image.URL)
	}
	if parts[0].ContentRef == nil || !parts[0].ContentRef.FromDataURL {
		t.Fatalf("image ContentRef = %#v, want FromDataURL", parts[0].ContentRef)
	}
	if parts[1].File.URL != "" {
		t.Fatalf("persisted file URL = %q, want empty", parts[1].File.URL)
	}
	if parts[1].ContentRef == nil || !parts[1].ContentRef.FromDataURL {
		t.Fatalf("file ContentRef = %#v, want FromDataURL", parts[1].ContentRef)
	}

	hydrated, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	hydratedParts := hydrated.Events[0].Response.Choices[0].Message.ContentParts
	if string(hydratedParts[0].Image.Data) != "image-data" {
		t.Fatalf("hydrated image data = %q, want image-data", hydratedParts[0].Image.Data)
	}
	if string(hydratedParts[1].File.Data) != "note data" {
		t.Fatalf("hydrated file data = %q, want note data", hydratedParts[1].File.Data)
	}
}

func TestGetSessionHydratesMixedHistoricalSession(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	artifacts := artifactmem.NewService()
	svc := Wrap(inner, artifacts, Config{Enabled: true})
	sess, err := svc.CreateSession(ctx, key, nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := svc.AppendEvent(ctx, sess, imageEvent([]byte("image-bytes"))); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	inline := imageEvent([]byte("inline-image"))
	if err := inner.AppendEvent(ctx, sess.Clone(), inline); err != nil {
		t.Fatalf("inner.AppendEvent() error = %v", err)
	}

	hydrated, err := svc.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if len(hydrated.Events) != 2 {
		t.Fatalf("hydrated events = %d, want 2", len(hydrated.Events))
	}
	first := hydrated.Events[0].Response.Choices[0].Message.ContentParts[0]
	if string(first.Image.Data) != "image-bytes" {
		t.Fatalf("first event image data = %q, want image-bytes", first.Image.Data)
	}
	second := hydrated.Events[1].Response.Choices[0].Message.ContentParts[0]
	if string(second.Image.Data) != "inline-image" {
		t.Fatalf("second event image data = %q, want inline-image", second.Image.Data)
	}
	if second.ContentRef != nil {
		t.Fatalf("second event ContentRef = %#v, want nil", second.ContentRef)
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
	if !errors.Is(err, ErrArtifactServiceNil) {
		t.Fatalf("AppendEvent() error = %v, want ErrArtifactServiceNil", err)
	}
	persisted, getErr := inner.GetSession(ctx, key)
	if getErr != nil {
		t.Fatalf("inner.GetSession() error = %v", getErr)
	}
	if len(persisted.Events) != 0 {
		t.Fatalf("persisted events = %d, want 0", len(persisted.Events))
	}
}

func TestHydrateFailsClosedWithoutArtifactService(t *testing.T) {
	ctx := context.Background()
	sess := &session.Session{
		Events: []event.Event{
			*imageEvent(nil),
		},
	}
	sess.Events[0].Response.Choices[0].Message.ContentParts[0].ContentRef = &model.ContentRef{
		ArtifactName:    "sessionpart_ref.png",
		ArtifactVersion: 0,
	}

	_, err := hydrateSession(ctx, sess, artifact.SessionInfo{}, nil)
	if !errors.Is(err, ErrArtifactServiceNil) {
		t.Fatalf("hydrateSession() error = %v, want ErrArtifactServiceNil", err)
	}
}

func TestHydrateEventFailsClosedWithoutArtifactService(t *testing.T) {
	evt := imageEvent(nil)
	evt.Response.Choices[0].Message.ContentParts[0].ContentRef = &model.ContentRef{
		ArtifactName:    "sessionpart_ref.png",
		ArtifactVersion: 0,
	}

	_, _, err := hydrateEvent(context.Background(), evt, artifact.SessionInfo{}, nil)
	if !errors.Is(err, ErrArtifactServiceNil) {
		t.Fatalf("hydrateEvent() error = %v, want ErrArtifactServiceNil", err)
	}
}

func TestHydrateValidatesArtifactIntegrity(t *testing.T) {
	ctx := context.Background()
	info := artifact.SessionInfo{AppName: "app", UserID: "user", SessionID: "sess"}
	tests := []struct {
		name      string
		ref       *model.ContentRef
		wantError string
	}{
		{
			name: "size mismatch",
			ref: &model.ContentRef{
				ArtifactName:    "sessionpart_ref.png",
				ArtifactVersion: 0,
				SizeBytes:       99,
				SHA256:          sha256Hex([]byte("image-bytes")),
			},
			wantError: "artifact size mismatch",
		},
		{
			name: "sha mismatch",
			ref: &model.ContentRef{
				ArtifactName:    "sessionpart_ref.png",
				ArtifactVersion: 0,
				SizeBytes:       int64(len("image-bytes")),
				SHA256:          sha256Hex([]byte("other-bytes")),
			},
			wantError: "artifact sha256 mismatch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifacts := artifactmem.NewService()
			_, err := artifacts.SaveArtifact(ctx, info, tt.ref.ArtifactName, &artifact.Artifact{
				Data:     []byte("image-bytes"),
				MimeType: "image/png",
			})
			if err != nil {
				t.Fatalf("SaveArtifact() error = %v", err)
			}
			sess := &session.Session{
				Events: []event.Event{
					*imageEvent(nil),
				},
			}
			sess.Events[0].Response.Choices[0].Message.ContentParts[0].ContentRef = tt.ref

			_, err = hydrateSession(ctx, sess, info, artifacts)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("hydrateSession() error = %v, want %q", err, tt.wantError)
			}
		})
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

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	artifacts := artifactmem.NewService()
	const artifactName = "sessionpart_search.png"
	_, err := artifacts.SaveArtifact(ctx, artifactSessionInfo(key), artifactName, &artifact.Artifact{
		Data:     []byte("search-image"),
		MimeType: "image/png",
	})
	if err != nil {
		t.Fatalf("SaveArtifact() error = %v", err)
	}
	searchEvent := imageEvent(nil)
	searchEvent.Response.Choices[0].Message.ContentParts[0].ContentRef = &model.ContentRef{
		ArtifactName:    artifactName,
		ArtifactVersion: 0,
		MimeType:        "image/png",
		SizeBytes:       int64(len("search-image")),
		SHA256:          sha256Hex([]byte("search-image")),
	}
	searchInner := &searchOnlyService{
		Service: inner,
		results: []session.EventSearchResult{
			{
				SessionKey: key,
				Event:      *searchEvent,
			},
		},
	}
	searchWrapped := Wrap(searchInner, artifacts, Config{Enabled: true})
	searchable, ok := searchWrapped.(session.SearchableService)
	if !ok {
		t.Fatal("wrapped searchable service does not implement SearchableService")
	}
	if _, ok := searchWrapped.(session.TrackService); ok {
		t.Fatal("wrapped search-only service unexpectedly implements TrackService")
	}
	results, err := searchable.SearchEvents(ctx, session.EventSearchRequest{
		UserKey: session.UserKey{AppName: key.AppName, UserID: key.UserID},
		Query:   "image",
	})
	if err != nil {
		t.Fatalf("SearchEvents() error = %v", err)
	}
	part := results[0].Event.Response.Choices[0].Message.ContentParts[0]
	if string(part.Image.Data) != "search-image" {
		t.Fatalf("search hydrated image data = %q, want search-image", part.Image.Data)
	}
	if part.ContentRef != nil {
		t.Fatalf("search hydrated ContentRef = %#v, want nil", part.ContentRef)
	}
}

func TestArtifactNameVersionInvalidRefIsSentinel(t *testing.T) {
	tests := []*model.ContentRef{
		nil,
		{ArtifactRef: "bad-ref"},
		{ArtifactRef: "artifact://name"},
		{ArtifactRef: "artifact://name@bad"},
	}
	for _, ref := range tests {
		_, _, err := artifactNameVersion(ref)
		if !errors.Is(err, ErrInvalidArtifactRef) {
			t.Fatalf("artifactNameVersion(%#v) error = %v, want ErrInvalidArtifactRef", ref, err)
		}
	}
}

func TestWrapOptionalInterfaceCombinationMethods(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	searchResults := []session.EventSearchResult{{SessionKey: key, Event: *imageEvent([]byte("inline"))}}
	windowResult := &session.EventWindow{
		SessionKey: key,
		Entries: []session.EventWindowEntry{
			{Event: *imageEvent([]byte("inline"))},
		},
	}
	tests := []struct {
		name       string
		inner      session.Service
		wantSearch bool
		wantWindow bool
		wantTrack  bool
	}{
		{
			name:       "search window",
			wantSearch: true,
			wantWindow: true,
			inner: &searchWindowOnlyService{
				Service:  sessionmem.NewSessionService(),
				behavior: &optionalBehavior{results: searchResults, window: windowResult},
			},
		},
		{
			name:       "search track",
			wantSearch: true,
			wantTrack:  true,
			inner: &searchTrackOnlyService{
				Service:  sessionmem.NewSessionService(),
				behavior: &optionalBehavior{results: searchResults},
			},
		},
		{
			name:       "window track",
			wantWindow: true,
			wantTrack:  true,
			inner: &windowTrackOnlyService{
				Service:  sessionmem.NewSessionService(),
				behavior: &optionalBehavior{window: windowResult},
			},
		},
		{
			name:       "search window track",
			wantSearch: true,
			wantWindow: true,
			wantTrack:  true,
			inner: &searchWindowTrackService{
				Service:  sessionmem.NewSessionService(),
				behavior: &optionalBehavior{results: searchResults, window: windowResult},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := Wrap(tt.inner, artifactmem.NewService(), Config{Enabled: true})
			if searchable, ok := wrapped.(session.SearchableService); ok != tt.wantSearch {
				t.Fatalf("SearchableService ok = %v, want %v", ok, tt.wantSearch)
			} else if ok {
				results, err := searchable.SearchEvents(ctx, session.EventSearchRequest{
					UserKey: session.UserKey{AppName: key.AppName, UserID: key.UserID},
					Query:   "image",
				})
				if err != nil {
					t.Fatalf("SearchEvents() error = %v", err)
				}
				if len(results) != 1 {
					t.Fatalf("SearchEvents() len = %d, want 1", len(results))
				}
			}
			if window, ok := wrapped.(session.WindowService); ok != tt.wantWindow {
				t.Fatalf("WindowService ok = %v, want %v", ok, tt.wantWindow)
			} else if ok {
				result, err := window.GetEventWindow(ctx, session.EventWindowRequest{Key: key})
				if err != nil {
					t.Fatalf("GetEventWindow() error = %v", err)
				}
				if result == nil || len(result.Entries) != 1 {
					t.Fatalf("GetEventWindow() = %#v, want one entry", result)
				}
			}
			if track, ok := wrapped.(session.TrackService); ok != tt.wantTrack {
				t.Fatalf("TrackService ok = %v, want %v", ok, tt.wantTrack)
			} else if ok {
				if err := track.AppendTrackEvent(ctx, &session.Session{}, &session.TrackEvent{Track: "trace"}); err != nil {
					t.Fatalf("AppendTrackEvent() error = %v", err)
				}
			}
		})
	}
}

func TestExternalizeTargetForPartEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		part    *model.ContentPart
		wantOK  bool
		wantErr string
		check   func(*testing.T, externalizeTarget, *model.ContentPart)
	}{
		{name: "nil part"},
		{
			name: "existing ref",
			part: &model.ContentPart{
				Type:       model.ContentTypeImage,
				Image:      &model.Image{Data: []byte("image")},
				ContentRef: &model.ContentRef{ArtifactName: "already"},
			},
		},
		{name: "nil image", part: &model.ContentPart{Type: model.ContentTypeImage}},
		{
			name: "ordinary image url",
			part: &model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/a.png"}},
		},
		{
			name:    "invalid image data url",
			part:    &model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: "data:image/png;base64,%%"}},
			wantErr: "decode data URL",
		},
		{name: "nil audio", part: &model.ContentPart{Type: model.ContentTypeAudio}},
		{name: "nil file", part: &model.ContentPart{Type: model.ContentTypeFile}},
		{
			name:   "file data default mime",
			part:   &model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: "blob", Data: []byte("file")}},
			wantOK: true,
			check: func(t *testing.T, target externalizeTarget, part *model.ContentPart) {
				if target.mimeType != defaultMime {
					t.Fatalf("target MIME = %q, want %q", target.mimeType, defaultMime)
				}
				target.apply(part, &model.ContentRef{ArtifactName: "ref"})
				if len(part.File.Data) != 0 || part.ContentRef == nil {
					t.Fatalf("file data/ref after apply = %q/%#v, want cleared/ref", part.File.Data, part.ContentRef)
				}
			},
		},
		{
			name:   "file data url default mime",
			part:   &model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: "blob", URL: "data:,hello%20file"}},
			wantOK: true,
			check: func(t *testing.T, target externalizeTarget, part *model.ContentPart) {
				if string(target.data) != "hello file" || target.mimeType != defaultMime || !target.fromDataURL {
					t.Fatalf("target = data %q MIME %q fromDataURL %v, want decoded default data URL", target.data, target.mimeType, target.fromDataURL)
				}
				target.apply(part, &model.ContentRef{ArtifactName: "ref"})
				if part.File.URL != "" || part.ContentRef == nil {
					t.Fatalf("file URL/ref after apply = %q/%#v, want cleared/ref", part.File.URL, part.ContentRef)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, ok, err := externalizeTargetForPart(tt.part)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("externalizeTargetForPart() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("externalizeTargetForPart() error = %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("externalizeTargetForPart() ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.check != nil {
				tt.check(t, target, tt.part)
			}
		})
	}
}

func TestHydrateMessageEdgeCases(t *testing.T) {
	ctx := context.Background()
	info := artifact.SessionInfo{AppName: "app", UserID: "user", SessionID: "sess"}
	refFor := func(name, mimeType string, data []byte) *model.ContentRef {
		return &model.ContentRef{
			ArtifactName:    name,
			ArtifactVersion: 0,
			MimeType:        mimeType,
			SizeBytes:       int64(len(data)),
			SHA256:          sha256Hex(data),
			OriginalName:    name,
		}
	}
	t.Run("load error", func(t *testing.T) {
		msg := model.Message{ContentParts: []model.ContentPart{{
			Type:       model.ContentTypeImage,
			ContentRef: refFor("missing.png", "image/png", []byte("image")),
		}}}
		err := hydrateMessage(ctx, &msg, info, &loadArtifactService{
			Service: artifactmem.NewService(),
			err:     errors.New("load failed"),
		})
		if err == nil || !strings.Contains(err.Error(), "load failed") {
			t.Fatalf("hydrateMessage() error = %v, want load failed", err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		msg := model.Message{ContentParts: []model.ContentPart{{
			Type:       model.ContentTypeImage,
			ContentRef: refFor("missing.png", "image/png", []byte("image")),
		}}}
		err := hydrateMessage(ctx, &msg, info, &loadArtifactService{Service: artifactmem.NewService()})
		if err == nil || !strings.Contains(err.Error(), "artifact not found") {
			t.Fatalf("hydrateMessage() error = %v, want artifact not found", err)
		}
	})
	t.Run("fills nil content parts", func(t *testing.T) {
		artifacts := artifactmem.NewService()
		entries := []struct {
			name     string
			data     []byte
			mimeType string
		}{
			{name: "image.jpg", data: []byte("image"), mimeType: "image/jpeg"},
			{name: "audio.wav", data: []byte("audio"), mimeType: "audio/wav"},
			{name: "file.txt", data: []byte("file"), mimeType: "text/plain"},
		}
		for _, entry := range entries {
			if _, err := artifacts.SaveArtifact(ctx, info, entry.name, &artifact.Artifact{
				Data:     entry.data,
				MimeType: entry.mimeType,
				Name:     entry.name,
			}); err != nil {
				t.Fatalf("SaveArtifact(%q) error = %v", entry.name, err)
			}
		}
		if _, err := artifacts.SaveArtifact(ctx, info, "text.bin", &artifact.Artifact{
			Data:     []byte("ignored"),
			MimeType: defaultMime,
			Name:     "text.bin",
		}); err != nil {
			t.Fatalf("SaveArtifact(text.bin) error = %v", err)
		}
		msg := model.Message{ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage, ContentRef: refFor("image.jpg", "image/jpeg", []byte("image"))},
			{Type: model.ContentTypeAudio, ContentRef: refFor("audio.wav", "audio/wav", []byte("audio"))},
			{Type: model.ContentTypeFile, ContentRef: refFor("file.txt", "text/plain", []byte("file"))},
			{Type: model.ContentTypeText, ContentRef: refFor("text.bin", defaultMime, []byte("ignored"))},
		}}
		if err := hydrateMessage(ctx, &msg, info, artifacts); err != nil {
			t.Fatalf("hydrateMessage() error = %v", err)
		}
		if string(msg.ContentParts[0].Image.Data) != "image" || msg.ContentParts[0].Image.Format != "jpg" {
			t.Fatalf("hydrated image = %#v, want data and jpg format", msg.ContentParts[0].Image)
		}
		if string(msg.ContentParts[1].Audio.Data) != "audio" || msg.ContentParts[1].Audio.Format != "wav" {
			t.Fatalf("hydrated audio = %#v, want data and wav format", msg.ContentParts[1].Audio)
		}
		if string(msg.ContentParts[2].File.Data) != "file" || msg.ContentParts[2].File.Name != "file.txt" ||
			msg.ContentParts[2].File.MimeType != "text/plain" {
			t.Fatalf("hydrated file = %#v, want data/name/MIME", msg.ContentParts[2].File)
		}
		for i := 0; i < 3; i++ {
			if msg.ContentParts[i].ContentRef != nil {
				t.Fatalf("part %d ContentRef = %#v, want nil", i, msg.ContentParts[i].ContentRef)
			}
		}
		if msg.ContentParts[3].ContentRef == nil {
			t.Fatal("unsupported content type ContentRef = nil, want preserved")
		}
	})
}

func TestExternalizationHelperEdgeCases(t *testing.T) {
	if _, _, ok, err := parseDataURL("https://example.com/file"); ok || err != nil {
		t.Fatalf("parseDataURL(non-data) = ok %v err %v, want false nil", ok, err)
	}
	if _, _, _, err := parseDataURL("data:text/plain"); err == nil || !strings.Contains(err.Error(), "invalid data URL") {
		t.Fatalf("parseDataURL(missing comma) error = %v, want invalid data URL", err)
	}
	if _, _, _, err := parseDataURL("data:text/plain;base64,%%"); err == nil || !strings.Contains(err.Error(), "decode data URL") {
		t.Fatalf("parseDataURL(bad base64) error = %v, want decode data URL", err)
	}
	if _, _, _, err := parseDataURL("data:text/plain,%zz"); err == nil || !strings.Contains(err.Error(), "decode data URL") {
		t.Fatalf("parseDataURL(bad escape) error = %v, want decode data URL", err)
	}
	data, mimeType, ok, err := parseDataURL("data:,plain")
	if err != nil || !ok || string(data) != "plain" || mimeType != defaultMime {
		t.Fatalf("parseDataURL(default MIME) = %q %q %v %v, want plain default true nil", data, mimeType, ok, err)
	}

	if ext := artifactExt("", "archive.tar.gz"); ext != ".gz" {
		t.Fatalf("artifactExt(original name) = %q, want .gz", ext)
	}
	if ext := artifactExt("", "bad.\\evil"); ext != ".bin" {
		t.Fatalf("artifactExt(unsafe) = %q, want .bin", ext)
	}
	if ext := artifactExt("", "blob"); ext != ".bin" {
		t.Fatalf("artifactExt(default) = %q, want .bin", ext)
	}
	if name, version, err := artifactNameVersion(&model.ContentRef{ArtifactName: "name", ArtifactVersion: 7}); err != nil ||
		name != "name" || version != 7 {
		t.Fatalf("artifactNameVersion(name) = %q %d %v, want name 7 nil", name, version, err)
	}

	if appendObserved(nil, "event", 0) {
		t.Fatal("appendObserved(nil) = true, want false")
	}
	sess := &session.Session{Events: []event.Event{{ID: "event-1"}}}
	if !appendObserved(sess, "", 0) {
		t.Fatal("appendObserved(empty eventID) = false, want true")
	}
	if appendObserved(sess, "missing", 0) {
		t.Fatal("appendObserved(missing) = true, want false")
	}
	if info := sessionInfoFromSession(nil); info != (artifact.SessionInfo{}) {
		t.Fatalf("sessionInfoFromSession(nil) = %#v, want zero", info)
	}

	if got := chooseNonEmpty(" ", "\t"); got != "" {
		t.Fatalf("chooseNonEmpty(blank) = %q, want empty", got)
	}
	if got := normalizeMime(" "); got != defaultMime {
		t.Fatalf("normalizeMime(blank) = %q, want %q", got, defaultMime)
	}
	if got := imageMimeType(""); got != defaultMime {
		t.Fatalf("imageMimeType(empty) = %q, want %q", got, defaultMime)
	}
	if got := imageMimeType("jpg"); got != "image/jpeg" {
		t.Fatalf("imageMimeType(jpg) = %q, want image/jpeg", got)
	}
	if got := imageMimeType("image/webp"); got != "image/webp" {
		t.Fatalf("imageMimeType(full) = %q, want image/webp", got)
	}
	if got := audioMimeType(""); got != defaultMime {
		t.Fatalf("audioMimeType(empty) = %q, want %q", got, defaultMime)
	}
	if got := audioMimeType("audio/wav"); got != "audio/wav" {
		t.Fatalf("audioMimeType(full) = %q, want audio/wav", got)
	}
	if got := imageFormat("image/jpeg"); got != "jpg" {
		t.Fatalf("imageFormat(jpeg) = %q, want jpg", got)
	}
	if got := imageFormat("application/octet-stream"); got != "" {
		t.Fatalf("imageFormat(non-image) = %q, want empty", got)
	}
	if got := audioFormat("application/octet-stream"); got != "" {
		t.Fatalf("audioFormat(non-audio) = %q, want empty", got)
	}
	if got := mimeFromName("blob"); got != "" {
		t.Fatalf("mimeFromName(no ext) = %q, want empty", got)
	}
}

func TestServiceEntryPointEdgeCases(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	inner := sessionmem.NewSessionService()
	if got := Wrap(nil, artifactmem.NewService(), Config{Enabled: true}); got != nil {
		t.Fatalf("Wrap(nil) = %#v, want nil", got)
	}
	if got := Wrap(inner, artifactmem.NewService(), Config{}); got != inner {
		t.Fatalf("Wrap(disabled) = %#v, want inner", got)
	}

	createErr := errors.New("create failed")
	createSvc := &edgeService{Service: inner, createErr: createErr}
	if _, err := (&Service{Service: createSvc, cfg: Config{Enabled: true}}).CreateSession(ctx, key, nil); !errors.Is(err, createErr) {
		t.Fatalf("CreateSession(error) = %v, want %v", err, createErr)
	}
	createSvc.createErr = nil
	createSvc.createSess = nil
	if sess, err := (&Service{Service: createSvc, cfg: Config{Enabled: true}}).CreateSession(ctx, key, nil); err != nil || sess != nil {
		t.Fatalf("CreateSession(nil) = %#v %v, want nil nil", sess, err)
	}

	svc := &Service{Service: inner, artifactService: artifactmem.NewService(), cfg: Config{Enabled: true}}
	if err := svc.AppendEvent(ctx, nil, imageEvent([]byte("image"))); !errors.Is(err, session.ErrNilSession) {
		t.Fatalf("AppendEvent(nil session) = %v, want ErrNilSession", err)
	}
	getErr := errors.New("get failed")
	getSvc := &edgeService{Service: inner, getErr: getErr}
	if _, err := (&Service{Service: getSvc, cfg: Config{Enabled: true}}).GetSession(ctx, key); !errors.Is(err, getErr) {
		t.Fatalf("GetSession(error) = %v, want %v", err, getErr)
	}
	getSvc.getErr = nil
	if sess, err := (&Service{Service: getSvc, cfg: Config{Enabled: true}}).GetSession(ctx, key); err != nil || sess != nil {
		t.Fatalf("GetSession(nil) = %#v %v, want nil nil", sess, err)
	}

	listErr := errors.New("list failed")
	listSvc := &edgeService{Service: inner, listErr: listErr}
	if _, err := (&Service{Service: listSvc, cfg: Config{Enabled: true}}).ListSessions(ctx, session.UserKey{}); !errors.Is(err, listErr) {
		t.Fatalf("ListSessions(error) = %v, want %v", err, listErr)
	}
	searchErr := errors.New("search failed")
	searchSvc := &searchOnlyService{Service: inner, err: searchErr}
	if _, err := (&Service{cfg: Config{Enabled: true}}).searchEvents(ctx, searchSvc, session.EventSearchRequest{}); !errors.Is(err, searchErr) {
		t.Fatalf("searchEvents(error) = %v, want %v", err, searchErr)
	}
	windowErr := errors.New("window failed")
	windowSvc := &windowOnlyService{Service: inner, err: windowErr}
	if _, err := (&Service{cfg: Config{Enabled: true}}).getEventWindow(ctx, windowSvc, session.EventWindowRequest{}); !errors.Is(err, windowErr) {
		t.Fatalf("getEventWindow(error) = %v, want %v", err, windowErr)
	}
	windowSvc.err = nil
	if result, err := (&Service{cfg: Config{Enabled: true}}).getEventWindow(ctx, windowSvc, session.EventWindowRequest{}); err != nil || result != nil {
		t.Fatalf("getEventWindow(nil) = %#v %v, want nil nil", result, err)
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
	results []session.EventSearchResult
	err     error
}

func (s *searchOnlyService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

type windowOnlyService struct {
	session.Service
	window *session.EventWindow
	err    error
}

func (s *windowOnlyService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.window, nil
}

type edgeService struct {
	session.Service
	createSess *session.Session
	createErr  error
	getSess    *session.Session
	getErr     error
	list       []*session.Session
	listErr    error
}

func (s *edgeService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	return s.createSess, s.createErr
}

func (s *edgeService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	return s.getSess, s.getErr
}

func (s *edgeService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	return s.list, s.listErr
}

type optionalBehavior struct {
	results     []session.EventSearchResult
	window      *session.EventWindow
	trackCalled bool
}

type searchWindowOnlyService struct {
	session.Service
	behavior *optionalBehavior
}

func (s *searchWindowOnlyService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.behavior.results, nil
}

func (s *searchWindowOnlyService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	return s.behavior.window, nil
}

type searchTrackOnlyService struct {
	session.Service
	behavior *optionalBehavior
}

func (s *searchTrackOnlyService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.behavior.results, nil
}

func (s *searchTrackOnlyService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	event *session.TrackEvent,
	opts ...session.Option,
) error {
	s.behavior.trackCalled = true
	return nil
}

type windowTrackOnlyService struct {
	session.Service
	behavior *optionalBehavior
}

func (s *windowTrackOnlyService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	return s.behavior.window, nil
}

func (s *windowTrackOnlyService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	event *session.TrackEvent,
	opts ...session.Option,
) error {
	s.behavior.trackCalled = true
	return nil
}

type searchWindowTrackService struct {
	session.Service
	behavior *optionalBehavior
}

func (s *searchWindowTrackService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.behavior.results, nil
}

func (s *searchWindowTrackService) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	return s.behavior.window, nil
}

func (s *searchWindowTrackService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	event *session.TrackEvent,
	opts ...session.Option,
) error {
	s.behavior.trackCalled = true
	return nil
}

type loadArtifactService struct {
	artifact.Service
	artifact *artifact.Artifact
	err      error
}

func (s *loadArtifactService) LoadArtifact(
	ctx context.Context,
	sessionInfo artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	return s.artifact, s.err
}

func imageEvent(data []byte) *event.Event {
	msg := model.NewUserMessage("image")
	msg.AddImageData(data, "high", "png")
	return responseEvent(msg)
}

func responseEvent(msg model.Message) *event.Event {
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
