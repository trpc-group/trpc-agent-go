//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates opt-in session content externalization.
//
// The example uses a recording model instead of a real provider so it can run
// without API keys. It shows the three important views:
//   - the model request still sees inline content payloads;
//   - the underlying persisted session stores ContentRef instead of bytes;
//   - a later turn hydrates persisted ContentRef back into normal ContentParts.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/externalization"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName   = "session-externalization-example"
	userID    = "demo-user"
	sessionID = "demo-session"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Printf("session externalization example failed: %v", err)
	}
}

func run(ctx context.Context) (err error) {
	rawSessionService := sessioninmemory.NewSessionService()
	artifactService := artifactinmemory.NewService()

	fmt.Println("Step 1: enable session content externalization")
	sessionService := externalization.Wrap(
		rawSessionService,
		artifactService,
		externalization.Config{Enabled: true},
	)
	rec := &recordingModel{name: "recording-model"}
	agent := llmagent.New("externalization-demo-agent", llmagent.WithModel(rec))
	r := runner.NewRunner(
		appName,
		agent,
		runner.WithSessionService(sessionService),
		runner.WithArtifactService(artifactService),
	)
	defer func() {
		if closeErr := r.Close(); closeErr != nil {
			if err != nil {
				log.Printf("close runner: %v", closeErr)
				return
			}
			err = fmt.Errorf("close runner: %w", closeErr)
		}
	}()

	fmt.Println("Step 2: first user turn stores compact session content")
	imageData := []byte("tiny-demo-image")
	msg := model.NewUserMessage("Please inspect this image and note.")
	msg.AddImageData(imageData, "auto", "png")
	msg.AddFileURL("note.txt", "data:text/plain;base64,aGVsbG8=", "text/plain")

	if err := runUserTurn(ctx, r, msg); err != nil {
		return fmt.Errorf("first user turn: %w", err)
	}

	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}
	persisted, err := rawSessionService.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("read persisted session: %w", err)
	}
	persistedEvent, err := firstUserEvent(persisted)
	if err != nil {
		return err
	}
	firstRequest, err := recordedRequest(rec, 0)
	if err != nil {
		return err
	}

	printModelRequestView("First model request", firstRequest)
	if err := printPersistedSessionView("Persisted session event", persistedEvent); err != nil {
		return err
	}

	fmt.Println("Step 3: second user turn hydrates history for model request")
	if err := runUserTurn(ctx, r, model.NewUserMessage("Continue with the previous image.")); err != nil {
		return fmt.Errorf("second user turn: %w", err)
	}
	secondRequest, err := recordedRequest(rec, 1)
	if err != nil {
		return err
	}
	printModelRequestView("Second model request after hydrate", secondRequest)
	return nil
}

type recordingModel struct {
	mu       sync.Mutex
	name     string
	captured []*model.Request
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
		Done: true,
		Choices: []model.Choice{
			{
				Message: model.NewAssistantMessage("ok"),
			},
		},
	}
	close(ch)
	return ch, nil
}

func (m *recordingModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *recordingModel) requestsSnapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*model.Request, len(m.captured))
	copy(out, m.captured)
	return out
}

func (m *recordingModel) requests() []*model.Request {
	return m.requestsSnapshot()
}

// runUserTurn runs one user turn and consumes the returned event stream.
// Each runner.Run call represents one user turn in the same session.
func runUserTurn(ctx context.Context, r runner.Runner, msg model.Message) error {
	ch, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return err
	}
	for evt := range ch {
		if evt == nil || evt.Response == nil || evt.Response.Error == nil {
			continue
		}
		return errors.New(evt.Response.Error.Message)
	}
	return nil
}

func recordedRequest(rec *recordingModel, index int) (*model.Request, error) {
	requests := rec.requests()
	if index < 0 || index >= len(requests) {
		return nil, fmt.Errorf("recorded request %d not found", index)
	}
	return requests[index], nil
}

func printModelRequestView(title string, req *model.Request) {
	fmt.Println(title + ":")
	fmt.Printf("- image bytes len: %d\n", len(firstImageData(req)))
	fmt.Printf("- file data URL present: %t\n", requestFileURL(req) != "")
	fmt.Printf("- contains ContentRef: %t\n", requestHasContentRef(req))
}

func printPersistedSessionView(title string, evt event.Event) error {
	persistedImage, err := firstPart(evt, model.ContentTypeImage)
	if err != nil {
		return err
	}
	persistedFile, err := firstPart(evt, model.ContentTypeFile)
	if err != nil {
		return err
	}
	fmt.Println(title + ":")
	fmt.Printf("- image bytes len: %d\n", len(persistedImage.Image.Data))
	fmt.Printf("- image artifact ref: %s\n", artifactRef(persistedImage.ContentRef))
	fmt.Printf("- file URL len: %d\n", len(persistedFile.File.URL))
	fmt.Printf("- file artifact ref: %s\n", artifactRef(persistedFile.ContentRef))
	return nil
}

func firstUserEvent(sess *session.Session) (event.Event, error) {
	for _, evt := range sess.Events {
		if evt.Author == "user" {
			return evt, nil
		}
	}
	return event.Event{}, errors.New("session has no user event")
}

func firstPart(evt event.Event, typ model.ContentType) (model.ContentPart, error) {
	for _, choice := range evt.Response.Choices {
		for _, part := range choice.Message.ContentParts {
			if part.Type == typ {
				return part, nil
			}
		}
	}
	return model.ContentPart{}, fmt.Errorf("event has no %s part", typ)
}

func firstImageData(req *model.Request) []byte {
	for _, msg := range req.Messages {
		for _, part := range msg.ContentParts {
			if part.Type == model.ContentTypeImage && part.Image != nil && len(part.Image.Data) > 0 {
				return part.Image.Data
			}
		}
	}
	return nil
}

func requestFileURL(req *model.Request) string {
	for _, msg := range req.Messages {
		for _, part := range msg.ContentParts {
			if part.Type == model.ContentTypeFile && part.File != nil && part.File.URL != "" {
				return part.File.URL
			}
		}
	}
	return ""
}

func requestHasContentRef(req *model.Request) bool {
	for _, msg := range req.Messages {
		for _, part := range msg.ContentParts {
			if part.ContentRef != nil {
				return true
			}
		}
	}
	return false
}

func artifactRef(ref *model.ContentRef) string {
	if ref == nil {
		return "<nil>"
	}
	return ref.ArtifactRef
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
