//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type cancellationTrackedBody struct {
	io.ReadCloser
	closed chan struct{}
	onRead func([]byte)
	once   sync.Once
}

func (b *cancellationTrackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.onRead != nil {
		b.onRead(p[:n])
	}
	return n, err
}

func (b *cancellationTrackedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { close(b.closed) })
	return err
}

func TestStreamBackpressureCancellationReleasesBodyBeforeDrain(t *testing.T) {
	for _, eventType := range []string{
		"response.output_text.delta",
		"response.reasoning_summary_text.delta",
		"response.refusal.delta",
	} {
		t.Run(eventType, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			firstPreviewFlushed := make(chan struct{})
			allowRemainingPreviews := make(chan struct{})
			remainingPreviewsFlushed := make(chan struct{})
			secondPreviewRead := make(chan struct{})
			bodyClosed := make(chan struct{})
			serverCancelled := make(chan struct{})
			var requests atomic.Int32
			var streamRequested atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) != 1 {
					http.Error(w, "request budget exceeded", http.StatusInternalServerError)
					return
				}
				var request struct {
					Stream bool `json:"stream"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				streamRequested.Store(request.Stream)
				writeSSE(w,
					`{"type":"response.created","response":{"id":"r_pending","status":"in_progress"}}`,
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_pending","call_id":"call_pending","name":"calculator","arguments":""}}`,
					`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_pending","delta":"{\"a\":"}`,
					fmt.Sprintf(`{"type":%q,"output_index":1,"content_index":0,"delta":"first"}`, eventType),
				)
				close(firstPreviewFlushed)
				select {
				case <-allowRemainingPreviews:
				case <-r.Context().Done():
					close(serverCancelled)
					return
				}
				writeSSE(w,
					fmt.Sprintf(`{"type":%q,"output_index":1,"content_index":0,"delta":"second"}`, eventType),
					fmt.Sprintf(`{"type":%q,"output_index":1,"content_index":0,"delta":"third"}`, eventType),
				)
				close(remainingPreviewsFlushed)
				<-r.Context().Done()
				close(serverCancelled)
			}))
			t.Cleanup(srv.Close)
			t.Cleanup(cancel)
			transport := http.DefaultTransport.(*http.Transport).Clone()
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(r)
				if err == nil && response.Body != nil {
					var readBytes []byte
					var readOnce sync.Once
					response.Body = &cancellationTrackedBody{
						ReadCloser: response.Body,
						closed:     bodyClosed,
						onRead: func(data []byte) {
							readBytes = append(readBytes, data...)
							if bytes.Contains(readBytes, []byte(`"delta":"second"`)) {
								readOnce.Do(func() { close(secondPreviewRead) })
							}
						},
					}
				}
				return response, err
			})}
			m := New("test-model", WithAPIKey("test"), WithBaseURL(srv.URL), WithChannelBufferSize(1),
				WithOpenAIOptions(option.WithMaxRetries(0), option.WithHTTPClient(client)))
			ch, err := m.GenerateContent(ctx, &model.Request{
				Messages:         []model.Message{model.NewUserMessage("start")},
				GenerationConfig: model.GenerationConfig{Stream: true},
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				cancel()
				timer := time.NewTimer(2 * time.Second)
				defer timer.Stop()
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							return
						}
					case <-timer.C:
						t.Error("response producer did not stop during cleanup")
						return
					}
				}
			})
			awaitCancellationSignal(t, firstPreviewFlushed, "server did not flush the first preview")
			require.Eventually(t, func() bool { return len(ch) == 1 }, 2*time.Second, time.Millisecond,
				"the adapter's unread first preview must fill its output buffer")
			close(allowRemainingPreviews)
			awaitCancellationSignal(t, remainingPreviewsFlushed, "server did not flush later previews")
			awaitCancellationSignal(t, secondPreviewRead, "adapter did not read the second preview")
			require.Equal(t, 1, len(ch), "the adapter output buffer must remain full before cancellation")
			cancel()

			// The body must close before the consumer helps drain the channel.
			// The channel must then close with only its already-buffered preview.
			awaitCancellationSignal(t, bodyClosed, "HTTP body stayed open while the output buffer was full")
			awaitCancellationSignal(t, serverCancelled, "server did not observe request cancellation")
			require.True(t, streamRequested.Load())
			require.EqualValues(t, 1, requests.Load())

			var delivered int
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			for {
				select {
				case response, ok := <-ch:
					if !ok {
						require.Equal(t, 1, delivered, "only the already-buffered preview may remain after cancellation")
						return
					}
					delivered++
					require.Nil(t, response.Error)
					require.False(t, response.Done)
					require.True(t, response.IsPartial)
					for _, choice := range response.Choices {
						require.Empty(t, choice.Message.ToolCalls)
						require.Empty(t, choice.Delta.ToolCalls)
						require.Nil(t, choice.Message.Replay)
						require.Nil(t, choice.Delta.Replay)
					}
				case <-timer.C:
					t.Fatal("response channel did not close after cancellation")
				}
			}
		})
	}
}

func awaitCancellationSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}
