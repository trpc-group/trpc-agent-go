//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates opt-in session multimodal externalization.
//
// The example uses a recording model instead of a real provider so it can run
// without API keys. It shows the three important views:
//   - the model request still sees inline multimodal bytes;
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
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionmultimodal "trpc.group/trpc-go/trpc-agent-go/session/multimodal"
)

const (
	appName   = "session-multimodal-externalization-example"
	userID    = "demo-user"
	sessionID = "demo-session"
)

func main() {
	ctx := context.Background()
	sessionService := sessioninmemory.NewSessionService()
	artifactService := artifactinmemory.NewService()
	governedSessionService := sessionmultimodal.Wrap(
		sessionService,
		artifactService,
		sessionmultimodal.Config{Enabled: true},
	)
	rec := &recordingModel{name: "recording-model"}
	agent := llmagent.New("multimodal-demo-agent", llmagent.WithModel(rec))
	r := runner.NewRunner(
		appName,
		agent,
		runner.WithSessionService(governedSessionService),
		runner.WithArtifactService(artifactService),
	)
	defer func() {
		if err := r.Close(); err != nil {
			log.Printf("close runner: %v", err)
		}
	}()

	imageData := []byte("tiny-demo-image")
	msg := model.NewUserMessage("Please inspect this image and note.")
	msg.AddImageData(imageData, "auto", "png")
	msg.AddFileURL("note.txt", "data:text/plain;base64,aGVsbG8=", "text/plain")

	if err := drainRun(ctx, r, msg); err != nil {
		log.Fatalf("first run failed: %v", err)
	}

	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}
	persisted, err := sessionService.GetSession(ctx, key)
	if err != nil {
		log.Fatalf("read persisted session: %v", err)
	}
	persistedEvent := firstUserEvent(persisted)
	persistedImage := firstPart(persistedEvent, model.ContentTypeImage)
	persistedFile := firstPart(persistedEvent, model.ContentTypeFile)
	firstRequest := rec.requests()[0]

	fmt.Println("After first turn:")
	fmt.Println("Runtime request sent to model:")
	fmt.Printf("- image bytes len: %d\n", len(firstImageData(firstRequest)))
	fmt.Printf("- file data URL present: %t\n", requestFileURL(firstRequest) != "")
	fmt.Printf("- contains ContentRef: %t\n", requestHasContentRef(firstRequest))
	fmt.Println("Persisted session event:")
	fmt.Printf("- image bytes len: %d\n", len(persistedImage.Image.Data))
	fmt.Printf("- image artifact ref: %s\n", artifactRef(persistedImage.ContentRef))
	fmt.Printf("- file URL len: %d\n", len(persistedFile.File.URL))
	fmt.Printf("- file artifact ref: %s\n", artifactRef(persistedFile.ContentRef))

	if err := drainRun(ctx, r, model.NewUserMessage("Continue with the previous image.")); err != nil {
		log.Fatalf("second run failed: %v", err)
	}
	secondRequest := rec.requests()[1]
	fmt.Println("After second turn:")
	fmt.Println("Runtime request sent to model:")
	fmt.Printf("- hydrated historical image bytes len: %d\n", len(firstImageData(secondRequest)))
	fmt.Printf("- contains ContentRef: %t\n", requestHasContentRef(secondRequest))
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

func drainRun(ctx context.Context, r runner.Runner, msg model.Message) error {
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

func firstUserEvent(sess *session.Session) event.Event {
	for _, evt := range sess.Events {
		if evt.Author == "user" {
			return evt
		}
	}
	log.Fatal("session has no user event")
	return event.Event{}
}

func firstPart(evt event.Event, typ model.ContentType) model.ContentPart {
	for _, choice := range evt.Response.Choices {
		for _, part := range choice.Message.ContentParts {
			if part.Type == typ {
				return part
			}
		}
	}
	log.Fatalf("event has no %s part", typ)
	return model.ContentPart{}
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
