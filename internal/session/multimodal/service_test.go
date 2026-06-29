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

func imageEvent(data []byte) *event.Event {
	msg := model.NewUserMessage("")
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
