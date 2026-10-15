// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package a2aagent

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestStreamingSilentChannelCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := make(chan protocol.StreamingMessageEvent)
	done := make(chan streamingEventResult, 1)
	a := &A2AAgent{}
	started := make(chan struct{})
	go func() {
		close(started)
		done <- a.processStreamingEvents(ctx, &agent.Invocation{}, make(chan *event.Event), stream, nil)
	}()
	<-started
	cancel()

	select {
	case result := <-done:
		if result.terminalError == nil {
			t.Fatal("cancellation must suppress final success")
		}
	case <-time.After(time.Second):
		close(stream)
		<-done
		t.Fatal("silent A2A channel failed to cancel")
	}
}

type streamCancellationBody struct {
	entered, closed chan struct{}
	once            sync.Once
	entry           sync.Once
}

func (b *streamCancellationBody) Read([]byte) (int, error) {
	b.entry.Do(func() { close(b.entered) })
	<-b.closed
	return 0, io.EOF
}
func (b *streamCancellationBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

type streamCancellationHandler struct{ body *streamCancellationBody }

func (h streamCancellationHandler) Handle(context.Context, *http.Client, *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: h.body}, nil
}
func TestStreamingRunEntrySilentCancellation(t *testing.T) {
	body := &streamCancellationBody{entered: make(chan struct{}), closed: make(chan struct{})}
	defer body.Close()
	cl, e := client.NewA2AClient("http://unused", client.WithHTTPReqHandler(streamCancellationHandler{body}))
	if e != nil {
		t.Fatal(e)
	}
	a := &A2AAgent{name: "streamCancellation", agentCard: &server.AgentCard{URL: "http://unused"}, eventConverter: &defaultA2AEventConverter{}, a2aMessageConverter: stubInvocationConverter{}, streamingBufSize: 4, a2aClient: cl}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, e := a.runStreaming(ctx, &agent.Invocation{InvocationID: "streamCancellation", Message: model.Message{Role: model.RoleUser, Content: "test"}})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("SDK not reading")
	}
	cancel()
	done := make(chan struct{})
	go func() {
		for ev := range events {
			if ev != nil && ev.Response != nil && ev.Response.Done && ev.Response.Error == nil {
				t.Error("success event after cancellation")
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runStreaming failed to cancel independently of SDK")
	}
}
