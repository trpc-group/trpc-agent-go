//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/externalization"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionmongodb "trpc.group/trpc-go/trpc-agent-go/session/mongodb"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

const externalizationE2EApp = "session-externalization-e2e"

func TestSessionExternalizationE2E(t *testing.T) {
	for _, backend := range sessionBackendFactories(t) {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			inner := backend.newService(t)
			closeSessionService(t, inner)
			artifacts := artifactinmemory.NewService()
			wrapped := wrappedSessionService(t, inner, artifacts)
			rec := &recordingModel{name: "capture", responseText: "ok"}
			ag := llmagent.New("agent", llmagent.WithModel(rec))
			r := runner.NewRunner(
				externalizationE2EApp,
				ag,
				runner.WithSessionService(wrapped),
				runner.WithArtifactService(artifacts),
			)
			defer func() { require.NoError(t, r.Close()) }()

			userID := uniqueID("user")
			sessionID := uniqueID("session")
			imageData := []byte("tiny-image")
			msg := model.NewUserMessage("please inspect")
			msg.AddImageData(imageData, "auto", "png")
			msg.AddFileURL(
				"note.txt",
				"data:text/plain;base64,aGVsbG8=",
				"text/plain",
			)

			require.NoError(t, drainRun(ctx, r, userID, sessionID, msg))
			require.Len(t, rec.requests(), 1)
			require.Equal(t, imageData, firstImageData(t, rec.requests()[0]))

			key := session.Key{
				AppName:   externalizationE2EApp,
				UserID:    userID,
				SessionID: sessionID,
			}
			persisted := getSession(t, ctx, inner, key)
			userEvent := firstUserEvent(t, persisted)
			imagePart := findPart(t, userEvent, model.ContentTypeImage)
			require.Empty(t, imagePart.Image.Data)
			require.Empty(t, imagePart.Image.URL)
			require.NotNil(t, imagePart.ContentRef)
			require.NotContains(t, imagePart.Image.URL, "artifact://")

			filePart := findPart(t, userEvent, model.ContentTypeFile)
			require.Empty(t, filePart.File.Data)
			require.Empty(t, filePart.File.URL)
			require.NotNil(t, filePart.ContentRef)
			require.NotContains(t, filePart.File.URL, "artifact://")

			hydrated := getSession(t, ctx, wrapped, key)
			hydratedUserEvent := firstUserEvent(t, hydrated)
			require.Equal(t, imageData, findPart(t, hydratedUserEvent, model.ContentTypeImage).Image.Data)
			require.Equal(t, []byte("hello"), findPart(t, hydratedUserEvent, model.ContentTypeFile).File.Data)

			persistedAgain := getSession(t, ctx, inner, key)
			require.Empty(t, findPart(t, firstUserEvent(t, persistedAgain), model.ContentTypeImage).Image.Data)

			require.NoError(t, drainRun(ctx, r, userID, sessionID, model.NewUserMessage("second turn")))
			requests := rec.requests()
			require.Len(t, requests, 2)
			require.Equal(t, imageData, firstImageData(t, requests[1]))
			requireNoArtifactRefsInRequest(t, requests[1])
		})
	}
}

func TestSessionExternalizationDisabledKeepsInline(t *testing.T) {
	ctx := context.Background()
	inner := sessioninmemory.NewSessionService()
	rec := &recordingModel{name: "capture", responseText: "ok"}
	r := runner.NewRunner(
		externalizationE2EApp,
		llmagent.New("agent", llmagent.WithModel(rec)),
		runner.WithSessionService(inner),
		runner.WithArtifactService(artifactinmemory.NewService()),
	)
	defer func() { require.NoError(t, r.Close()) }()

	userID := uniqueID("user")
	sessionID := uniqueID("session")
	imageData := []byte("inline-when-disabled")
	msg := model.NewUserMessage("please inspect")
	msg.AddImageData(imageData, "auto", "png")

	require.NoError(t, drainRun(ctx, r, userID, sessionID, msg))

	persisted := getSession(t, ctx, inner, session.Key{
		AppName:   externalizationE2EApp,
		UserID:    userID,
		SessionID: sessionID,
	})
	part := findPart(t, firstUserEvent(t, persisted), model.ContentTypeImage)
	require.Equal(t, imageData, part.Image.Data)
	require.Nil(t, part.ContentRef)
}

func TestSessionExternalizationProviderBoundaryRejectsUnresolvedRefs(t *testing.T) {
	ctx := context.Background()
	inner := sessioninmemory.NewSessionService()
	artifacts := artifactinmemory.NewService()
	wrapped := wrappedSessionService(t, inner, artifacts)
	rec := &recordingModel{name: "capture", responseText: "ok"}
	r := runner.NewRunner(
		externalizationE2EApp,
		llmagent.New("agent", llmagent.WithModel(rec)),
		runner.WithSessionService(wrapped),
		runner.WithArtifactService(artifacts),
	)
	defer func() { require.NoError(t, r.Close()) }()

	userID := uniqueID("user")
	sessionID := uniqueID("session")
	key := session.Key{
		AppName:   externalizationE2EApp,
		UserID:    userID,
		SessionID: sessionID,
	}
	sess, err := inner.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, inner.AppendEvent(ctx, sess, unresolvedRefEvent()))

	err = drainRun(ctx, r, userID, sessionID, model.NewUserMessage("continue"))
	require.ErrorContains(t, err, "artifact not found")
	require.ErrorContains(t, err, "missing.png@0")
	require.Empty(t, rec.requests())
}

func TestSessionExternalizationArtifactFailuresAreVisible(t *testing.T) {
	t.Run("save failure prevents damaged write", func(t *testing.T) {
		ctx := context.Background()
		inner := sessioninmemory.NewSessionService()
		artifacts := &failingArtifactService{
			Service: artifactinmemory.NewService(),
			saveErr: errors.New("save failed"),
		}
		wrapped := wrappedSessionService(t, inner, artifacts)
		r := runner.NewRunner(
			externalizationE2EApp,
			llmagent.New("agent", llmagent.WithModel(&recordingModel{name: "capture", responseText: "ok"})),
			runner.WithSessionService(wrapped),
			runner.WithArtifactService(artifacts),
		)
		defer func() { require.NoError(t, r.Close()) }()

		userID := uniqueID("user")
		sessionID := uniqueID("session")
		msg := model.NewUserMessage("please inspect")
		msg.AddImageData([]byte("image"), "auto", "png")

		err := drainRun(ctx, r, userID, sessionID, msg)
		require.ErrorContains(t, err, "save artifact")
		require.ErrorContains(t, err, "save failed")
		persisted := getSession(t, ctx, inner, session.Key{
			AppName:   externalizationE2EApp,
			UserID:    userID,
			SessionID: sessionID,
		})
		require.Empty(t, persisted.Events)
	})

	t.Run("hydrate failure is returned by run", func(t *testing.T) {
		ctx := context.Background()
		inner := sessioninmemory.NewSessionService()
		baseArtifacts := artifactinmemory.NewService()
		wrapped := wrappedSessionService(t, inner, baseArtifacts)
		r1 := runner.NewRunner(
			externalizationE2EApp,
			llmagent.New("agent", llmagent.WithModel(&recordingModel{name: "capture", responseText: "ok"})),
			runner.WithSessionService(wrapped),
			runner.WithArtifactService(baseArtifacts),
		)

		userID := uniqueID("user")
		sessionID := uniqueID("session")
		msg := model.NewUserMessage("please inspect")
		msg.AddImageData([]byte("image"), "auto", "png")
		require.NoError(t, drainRun(ctx, r1, userID, sessionID, msg))
		require.NoError(t, r1.Close())

		rec := &recordingModel{name: "capture", responseText: "ok"}
		failingArtifacts := &failingArtifactService{
			Service: baseArtifacts,
			loadErr: errors.New("load failed"),
		}
		failingWrapped := wrappedSessionService(t, inner, failingArtifacts)
		r2 := runner.NewRunner(
			externalizationE2EApp,
			llmagent.New("agent", llmagent.WithModel(rec)),
			runner.WithSessionService(failingWrapped),
			runner.WithArtifactService(failingArtifacts),
		)
		defer func() { require.NoError(t, r2.Close()) }()

		err := drainRun(ctx, r2, userID, sessionID, model.NewUserMessage("continue"))
		require.ErrorContains(t, err, "load artifact")
		require.ErrorContains(t, err, "load failed")
		require.Empty(t, rec.requests())
	})
}

type sessionBackendFactory struct {
	name       string
	newService func(t *testing.T) session.Service
}

func sessionBackendFactories(t *testing.T) []sessionBackendFactory {
	t.Helper()
	return []sessionBackendFactory{
		{
			name: "inmemory",
			newService: func(t *testing.T) session.Service {
				t.Helper()
				return sessioninmemory.NewSessionService()
			},
		},
		{
			name: "redis",
			newService: func(t *testing.T) session.Service {
				t.Helper()
				mr, err := miniredis.Run()
				require.NoError(t, err)
				t.Cleanup(mr.Close)
				svc, err := sessionredis.NewService(
					sessionredis.WithRedisClientURL("redis://" + mr.Addr()),
				)
				require.NoError(t, err)
				return svc
			},
		},
		{
			name: "mongodb",
			newService: func(t *testing.T) session.Service {
				t.Helper()
				uri := os.Getenv("MONGO_TEST_URI")
				if uri == "" {
					t.Skip("MONGO_TEST_URI is not set")
				}
				svc, err := sessionmongodb.NewService(
					sessionmongodb.WithMongoClientURI(uri),
					sessionmongodb.WithDatabase("trpc_agent_go_externalization_e2e"),
					sessionmongodb.WithCollectionPrefix("mm_e2e"),
				)
				require.NoError(t, err)
				return svc
			},
		},
	}
}

type recordingModel struct {
	mu           sync.Mutex
	name         string
	responseText string
	captured     []*model.Request
}

func (m *recordingModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("recording model: request is nil")
	}
	m.mu.Lock()
	m.captured = append(m.captured, cloneRequest(request))
	m.mu.Unlock()

	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:        uniqueID("response"),
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(m.responseText),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *recordingModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *recordingModel) requests() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*model.Request, len(m.captured))
	copy(out, m.captured)
	return out
}

type failingArtifactService struct {
	artifact.Service
	saveErr error
	loadErr error
}

func (s *failingArtifactService) SaveArtifact(
	ctx context.Context,
	info artifact.SessionInfo,
	filename string,
	art *artifact.Artifact,
) (int, error) {
	if s.saveErr != nil {
		return 0, s.saveErr
	}
	return s.Service.SaveArtifact(ctx, info, filename, art)
}

func (s *failingArtifactService) LoadArtifact(
	ctx context.Context,
	info artifact.SessionInfo,
	filename string,
	version *int,
) (*artifact.Artifact, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return s.Service.LoadArtifact(ctx, info, filename, version)
}

func drainRun(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	message model.Message,
) error {
	ch, err := r.Run(ctx, userID, sessionID, message)
	if err != nil {
		return err
	}
	var runErr error
	for evt := range ch {
		if evt == nil || evt.Response == nil || evt.Response.Error == nil {
			continue
		}
		if runErr == nil {
			runErr = errors.New(evt.Response.Error.Message)
		}
	}
	return runErr
}

func getSession(
	t *testing.T,
	ctx context.Context,
	svc session.Service,
	key session.Key,
) *session.Session {
	t.Helper()
	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	return sess
}

func wrappedSessionService(
	t *testing.T,
	inner session.Service,
	artifacts artifact.Service,
) session.Service {
	t.Helper()
	return externalization.Wrap(
		inner,
		artifacts,
		externalization.Config{Enabled: true},
	)
}

func firstUserEvent(t *testing.T, sess *session.Session) event.Event {
	t.Helper()
	for _, evt := range sess.Events {
		if evt.Author == "user" {
			return evt
		}
	}
	t.Fatalf("session has no user event")
	return event.Event{}
}

func findPart(t *testing.T, evt event.Event, typ model.ContentType) model.ContentPart {
	t.Helper()
	require.NotNil(t, evt.Response)
	for _, choice := range evt.Response.Choices {
		for _, part := range choice.Message.ContentParts {
			if part.Type == typ {
				return part
			}
		}
	}
	t.Fatalf("event has no %s part", typ)
	return model.ContentPart{}
}

func firstImageData(t *testing.T, req *model.Request) []byte {
	t.Helper()
	for _, msg := range req.Messages {
		for _, part := range msg.ContentParts {
			if part.Type == model.ContentTypeImage && part.Image != nil {
				return part.Image.Data
			}
		}
	}
	t.Fatalf("request has no image data")
	return nil
}

func requireNoArtifactRefsInRequest(t *testing.T, req *model.Request) {
	t.Helper()
	for _, msg := range req.Messages {
		require.NotContains(t, msg.Content, "artifact://")
		for _, part := range msg.ContentParts {
			if part.Image != nil {
				require.NotContains(t, part.Image.URL, "artifact://")
			}
			if part.File != nil {
				require.NotContains(t, part.File.URL, "artifact://")
				require.NotContains(t, part.File.FileID, "artifact://")
			}
		}
	}
}

func unresolvedRefEvent() *event.Event {
	return event.NewResponseEvent(
		"invocation",
		"user",
		&model.Response{
			Done: false,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role: model.RoleUser,
					ContentParts: []model.ContentPart{{
						Type:  model.ContentTypeImage,
						Image: &model.Image{},
						ContentRef: &model.ContentRef{
							ArtifactRef:     "artifact://missing.png@0",
							ArtifactName:    "missing.png",
							ArtifactVersion: 0,
							MimeType:        "image/png",
						},
					}},
				},
			}},
		},
	)
}

func cloneRequest(req *model.Request) *model.Request {
	if req == nil {
		return nil
	}
	clone := *req
	clone.Messages = make([]model.Message, len(req.Messages))
	for i, msg := range req.Messages {
		clone.Messages[i] = cloneMessage(msg)
	}
	return &clone
}

func cloneMessage(msg model.Message) model.Message {
	clone := msg
	if msg.ContentParts != nil {
		clone.ContentParts = make([]model.ContentPart, len(msg.ContentParts))
		for i, part := range msg.ContentParts {
			clone.ContentParts[i] = cloneContentPart(part)
		}
	}
	return clone
}

func cloneContentPart(part model.ContentPart) model.ContentPart {
	clone := part
	if part.Text != nil {
		text := *part.Text
		clone.Text = &text
	}
	if part.Image != nil {
		image := *part.Image
		image.Data = append([]byte(nil), part.Image.Data...)
		clone.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		audio.Data = append([]byte(nil), part.Audio.Data...)
		clone.Audio = &audio
	}
	if part.File != nil {
		file := *part.File
		file.Data = append([]byte(nil), part.File.Data...)
		clone.File = &file
	}
	if part.ContentRef != nil {
		ref := *part.ContentRef
		clone.ContentRef = &ref
	}
	return clone
}

func closeSessionService(t *testing.T, svc session.Service) {
	t.Helper()
	closer, ok := svc.(interface{ Close() error })
	if !ok {
		return
	}
	t.Cleanup(func() { require.NoError(t, closer.Close()) })
}

func uniqueID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
