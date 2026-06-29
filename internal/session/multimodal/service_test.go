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
	msg := model.NewUserMessage("multimodal")
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
}

func (s *searchOnlyService) SearchEvents(
	ctx context.Context,
	req session.EventSearchRequest,
) ([]session.EventSearchResult, error) {
	return s.results, nil
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
