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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestUnknownNativeFieldsSurviveMessageJSONAndNextInput(t *testing.T) {
	const nativeItem = `{"type":"reasoning","id":"rs_future","status":"completed","summary":[],"encrypted_content":"opaque-state","future_native":{"flag":true,"items":[1,"x"]}}`
	const nativeOutput = `[` + nativeItem + `]`
	var calls int
	var nextInput json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"resp_1","status":"completed","output":`+nativeOutput+`}`)
			return
		}
		var request struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode continued request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		nextInput = request.Input
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_2","status":"completed","output":[]}`)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	first, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("start")}})
	require.NoError(t, err)
	firstResult := collect(t, first)
	require.Len(t, firstResult, 1)
	require.Nil(t, firstResult[0].Error)

	encoded, err := json.Marshal(firstResult[0].Choices[0].Message)
	require.NoError(t, err)
	var restored model.Message
	require.NoError(t, json.Unmarshal(encoded, &restored))
	payload, err := restored.ReplayDataFor(responseProvider, "gpt-5")
	require.NoError(t, err)
	require.Equal(t, nativeOutput, string(payload), "Message JSON must retain opaque fields in the provider payload")

	continued, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{
		model.NewUserMessage("start"),
		restored,
		model.NewUserMessage("continue"),
	}})
	require.NoError(t, err)
	require.Nil(t, collect(t, continued)[0].Error)
	require.Equal(t, 2, calls)
	var inputItems []json.RawMessage
	require.NoError(t, json.Unmarshal(nextInput, &inputItems))
	require.Len(t, inputItems, 3)
	require.Equal(t, nativeItem, string(inputItems[1]), "the next request must send the complete native item unchanged")
}

func TestKeepaliveAndUnknownStreamExtensionsAreNotProjected(t *testing.T) {
	const keepalive = `{"type":"keepalive","cursor":{"sequence":7}}`
	const extension = `{"type":"response.vendor.trace","cursor":{"opaque":"v1"},"item":{"type":"function_call","id":"fc_fake","call_id":"call_fake","name":"calculator","arguments":"{}"}}`
	const message = `{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w,
			keepalive,
			extension,
			`{"type":"response.output_text.delta","delta":"preview"}`,
			`{"type":"response.output_item.done","output_index":0,"item":`+message+`}`,
			`{"type":"response.completed","response":{"id":"resp_stream","status":"completed","output":[`+message+`]}}`,
		)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	got := collect(t, ch)
	require.Len(t, got, 2, "keepalive and extension events do not create model responses")
	for _, response := range got {
		require.NotNil(t, response)
		require.NotEmpty(t, response.Choices)
		require.Empty(t, response.Choices[0].Message.ToolCalls, "unknown extension fields must never become executable calls")
	}
	require.True(t, got[0].IsPartial)
	require.Equal(t, "preview", got[0].Choices[0].Delta.Content)
	require.Nil(t, got[0].Choices[0].Message.Replay)
	require.Nil(t, got[1].Error)
	require.True(t, got[1].Done)
	require.NotNil(t, got[1].Choices[0].Message.Replay)
}

func TestUnknownOutputAndContentTypesFailForStreamingAndNonStreaming(t *testing.T) {
	for _, tc := range []struct {
		name string
		item string
		want string
	}{
		{
			name: "output item",
			item: `{"type":"web_search_call","id":"ws_1","status":"completed","call_id":"call_fake","name":"calculator","arguments":"{}"}`,
			want: `unsupported native item type "web_search_call"`,
		},
		{
			name: "nested content part",
			item: `{"type":"message","id":"msg_future","status":"completed","role":"assistant","content":[{"type":"future_part","opaque":{"value":1}}]}`,
			want: `message item "msg_future" has unsupported content type "future_part"`,
		},
	} {
		for _, streaming := range []bool{false, true} {
			name := "non-streaming"
			if streaming {
				name = "streaming"
			}
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				output := `[` + tc.item + `]`
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if streaming {
						writeSSE(w,
							`{"type":"response.output_text.delta","delta":"preview"}`,
							`{"type":"response.output_item.added","output_index":0,"item":`+tc.item+`}`,
							`{"type":"response.completed","response":{"id":"resp_bad","status":"completed","output":`+output+`}}`,
						)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"resp_bad","status":"completed","output":`+output+`}`)
				}))
				defer srv.Close()
				m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
				ch, err := m.GenerateContent(context.Background(), &model.Request{
					Messages:         []model.Message{model.NewUserMessage("hi")},
					GenerationConfig: model.GenerationConfig{Stream: streaming},
				})
				require.NoError(t, err)
				respErr := requireTerminalError(t, collect(t, ch))
				require.Contains(t, respErr.Message, tc.want)
			})
		}
	}
}

func TestUnknownTerminalExtensionDoesNotCommitResponse(t *testing.T) {
	const unknownTerminal = `{"type":"response.vendor.completed","response":{"id":"resp_future","status":"completed","output":[{"type":"function_call","id":"fc_fake","call_id":"call_fake","name":"calculator","arguments":"{}"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w, unknownTerminal)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	respErr := requireTerminalError(t, collect(t, ch))
	require.Contains(t, respErr.Message, "stream ended before a terminal response event")
}

func TestCompletedStreamWithoutStatusDoesNotBecomeSuccessful(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w, `{"type":"response.completed","response":{"id":"resp_malformed","output":[]}}`)
	}))
	defer srv.Close()
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	respErr := requireTerminalError(t, collect(t, ch))
	require.Contains(t, respErr.Message, "responses request did not complete")
}
