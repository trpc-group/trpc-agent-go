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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestStreamingSilentChannelCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			stream := make(chan protocol.StreamingMessageEvent)
			defer close(stream)
			done := make(chan streamingEventResult, 1)
			a := &A2AAgent{}
			started := make(chan struct{})
			go func() {
				close(started)
				done <- a.processStreamingEvents(ctx, &agent.Invocation{}, make(chan *event.Event), stream, nil)
			}()
			<-started
			if !deadline {
				cancel()
			}

			select {
			case result := <-done:
				wantErr := context.Canceled
				if deadline {
					wantErr = context.DeadlineExceeded
				}
				require.ErrorIs(t, ctx.Err(), wantErr)
				require.Equal(t, &model.ResponseError{
					Type: model.ErrorTypeCancelled, Message: ctx.Err().Error(),
				}, result.terminalError)
			case <-time.After(time.Second):
				t.Fatal("silent A2A channel failed to cancel")
			}
		})
	}
}

type streamCancellationConverter struct {
	defaultA2AEventConverter
	calls int
}

func (c *streamCancellationConverter) ConvertStreamingToEvents(
	protocol.StreamingMessageEvent, string, *agent.Invocation,
) ([]*event.Event, error) {
	c.calls++
	return nil, nil
}

func TestStreamingCancellationWithReadyChannel(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name := "buffered event"
		if closed {
			name = "closed channel"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			// Both select cases are ready. Repeat to exercise the receive path
			// as well as the direct cancellation path without scheduling sleeps.
			for i := 0; i < 100; i++ {
				stream := make(chan protocol.StreamingMessageEvent, 1)
				if closed {
					close(stream)
				} else {
					stream <- protocol.StreamingMessageEvent{}
				}
				converter := &streamCancellationConverter{}
				a := &A2AAgent{eventConverter: converter}
				events := make(chan *event.Event, 1)
				result := a.processStreamingEvents(ctx, &agent.Invocation{}, events, stream, nil)
				require.Zero(t, converter.calls, "canceled events must not reach the converter")
				require.Empty(t, events)
				require.Equal(t, &model.ResponseError{
					Type: model.ErrorTypeCancelled, Message: ctx.Err().Error(),
				}, result.terminalError)
			}
		})
	}
}

func streamCancellationArtifact() *protocol.TaskArtifactUpdateEvent {
	filePart := protocol.NewFilePartWithURI(
		"report.pdf", "application/pdf", "https://example.com/report.pdf",
	)
	return &protocol.TaskArtifactUpdateEvent{
		Kind: protocol.KindTaskArtifactUpdate, TaskID: "task",
		Artifact: protocol.Artifact{
			ArtifactID: "response",
			Parts: []protocol.Part{
				&protocol.TextPart{Kind: protocol.KindText, Text: "partial text"},
				&filePart,
			},
		},
	}
}

func TestStreamingCancellationPreservesPartialContent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := make(chan protocol.StreamingMessageEvent, 1)
	defer close(stream)
	stream <- protocol.StreamingMessageEvent{Result: streamCancellationArtifact()}
	events := make(chan *event.Event, 1)
	done := make(chan streamingEventResult, 1)
	a := &A2AAgent{name: "remote", eventConverter: &defaultA2AEventConverter{}}
	go func() {
		done <- a.processStreamingEvents(
			ctx, &agent.Invocation{InvocationID: "invocation"}, events, stream, nil,
		)
	}()

	// Receiving the partial event ensures that aggregation precedes cancellation.
	select {
	case evt := <-events:
		require.NotNil(t, evt.Response)
		require.True(t, evt.Response.IsPartial)
		require.False(t, evt.Response.Done)
	case <-time.After(time.Second):
		t.Fatal("partial event not emitted")
	}
	cancel()
	select {
	case result := <-done:
		require.Equal(t, &model.ResponseError{
			Type: model.ErrorTypeCancelled, Message: ctx.Err().Error(),
		}, result.terminalError)
		require.Equal(t, "response", result.responseID)
		require.Equal(t, "partial text", result.aggregatedContent)
		require.Len(t, result.aggregatedContentParts, 1)
		part := result.aggregatedContentParts[0]
		require.Equal(t, model.ContentTypeFile, part.Type)
		require.NotNil(t, part.File)
		require.Equal(t, "report.pdf", part.File.Name)
		require.Equal(t, "application/pdf", part.File.MimeType)
		require.Equal(t, "https://example.com/report.pdf", part.File.URL)
	case <-time.After(time.Second):
		t.Fatal("partial A2A stream failed to cancel")
	}
}

// streamCancellationBody delivers an optional SSE prefix, then blocks until
// Close. Ignoring the request context simulates an unresponsive SDK transport.
type streamCancellationBody struct {
	prefix          *strings.Reader
	entered, closed chan struct{}
	once            sync.Once
	entry           sync.Once
}

func (b *streamCancellationBody) Read(p []byte) (int, error) {
	if n, err := b.prefix.Read(p); n != 0 || err != io.EOF {
		return n, err
	}
	b.entry.Do(func() { close(b.entered) })
	<-b.closed
	return 0, io.EOF
}

func (b *streamCancellationBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

type streamCancellationHandler struct{ body *streamCancellationBody }

func (h streamCancellationHandler) Handle(context.Context, *http.Client, *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: h.body}, nil
}

func TestStreamingRunEntrySilentCancellation(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "silent"
		if partial {
			name = "partial text and file"
		}
		t.Run(name, func(t *testing.T) {
			var prefix string
			if partial {
				prefix = mustBuildSSEBody(t, []sseEvent{{
					eventType: protocol.EventArtifactUpdate,
					payload:   streamCancellationArtifact(),
				}})
			}
			body := &streamCancellationBody{
				prefix: strings.NewReader(prefix), entered: make(chan struct{}), closed: make(chan struct{}),
			}
			defer body.Close()
			cl, err := client.NewA2AClient("http://unused", client.WithHTTPReqHandler(streamCancellationHandler{body}))
			require.NoError(t, err)
			a := &A2AAgent{
				name:                "streamCancellation",
				agentCard:           &server.AgentCard{URL: "http://unused"},
				eventConverter:      &defaultA2AEventConverter{},
				a2aMessageConverter: stubInvocationConverter{},
				streamingBufSize:    4,
				a2aClient:           cl,
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events, err := a.runStreaming(ctx, &agent.Invocation{
				InvocationID: "streamCancellation",
				Message:      model.Message{Role: model.RoleUser, Content: "test"},
			})
			require.NoError(t, err)
			if partial {
				select {
				case evt, ok := <-events:
					require.True(t, ok, "stream closed before partial content")
					require.NotNil(t, evt.Response)
					require.True(t, evt.Response.IsPartial)
					require.False(t, evt.Response.Done)
					require.Len(t, evt.Response.Choices, 1)
					delta := evt.Response.Choices[0].Delta
					require.Equal(t, "partial text", delta.Content)
					require.Len(t, delta.ContentParts, 1)
					require.NotNil(t, delta.ContentParts[0].File)
					require.Equal(t, "https://example.com/report.pdf", delta.ContentParts[0].File.URL)
				case <-time.After(time.Second):
					t.Fatal("partial event not emitted")
				}
			}
			select {
			case <-body.entered:
			case <-time.After(time.Second):
				t.Fatal("SDK not reading")
			}
			cancel()
			timeout := time.NewTimer(time.Second)
			defer timeout.Stop()
			for {
				select {
				case evt, ok := <-events:
					if !ok {
						return
					}
					if evt != nil && evt.Response != nil {
						require.False(t, evt.Response.Done && evt.Response.Error == nil,
							"success event after cancellation")
					}
				case <-timeout.C:
					t.Fatal("runStreaming failed to cancel independently of SDK")
				}
			}
		})
	}
}
