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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const emptyCompletedEvent = `{"type":"response.completed","response":{"id":"resp_terminal","status":"completed","created_at":1700000000,"output":[]}}`

func TestStreamEmptyCompletionAfterOutputIsRejected(t *testing.T) {
	functionCall := `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"calculator","arguments":""}`
	for _, tc := range []struct {
		name           string
		events         []string
		wantContent    string
		wantReasoning  string
		wantNoPreviews bool
	}{
		{
			name:        "text delta",
			events:      []string{`{"type":"response.output_text.delta","delta":"hello"}`, emptyCompletedEvent},
			wantContent: "hello",
		},
		{
			name:        "refusal delta",
			events:      []string{`{"type":"response.refusal.delta","delta":"no"}`, emptyCompletedEvent},
			wantContent: "no",
		},
		{
			name:          "reasoning summary delta",
			events:        []string{`{"type":"response.reasoning_summary_text.delta","delta":"think"}`, emptyCompletedEvent},
			wantReasoning: "think",
		},
		{
			name:          "reasoning text delta",
			events:        []string{`{"type":"response.reasoning_text.delta","delta":"hidden"}`, emptyCompletedEvent},
			wantReasoning: "hidden",
		},
		{
			name:           "function call item",
			events:         []string{`{"type":"response.output_item.added","output_index":0,"item":` + functionCall + `}`, emptyCompletedEvent},
			wantNoPreviews: true,
		},
		{
			name:           "function call argument delta",
			events:         []string{`{"type":"response.function_call_arguments.delta","delta":"{\"a\":1}"}`, emptyCompletedEvent},
			wantNoPreviews: true,
		},
		{
			name:           "function call argument done",
			events:         []string{`{"type":"response.function_call_arguments.done","name":"calculator","arguments":"{\"a\":1}"}`, emptyCompletedEvent},
			wantNoPreviews: true,
		},
		{
			name:           "legacy function call name only",
			events:         []string{`{"type":"response.function_call_arguments.done","name":"calculator"}`, emptyCompletedEvent},
			wantNoPreviews: true,
		},
		{
			name:           "native reasoning item",
			events:         []string{`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`, emptyCompletedEvent},
			wantNoPreviews: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := collectStream(t, tc.events...)
			if tc.wantNoPreviews {
				require.Len(t, got, 1)
			} else {
				require.GreaterOrEqual(t, len(got), 2)
				preview := got[0]
				require.True(t, preview.IsPartial)
				require.False(t, preview.Done)
				require.Nil(t, preview.Error)
				require.Equal(t, tc.wantContent, preview.Choices[0].Delta.Content)
				require.Equal(t, tc.wantReasoning, preview.Choices[0].Delta.ReasoningContent)
			}
			var terminals int
			for _, resp := range got {
				if resp.Done {
					terminals++
				}
				require.NotEqual(t, "hello", messageContent(resp), "streamed text must not become the committed message")
			}
			require.Equal(t, 1, terminals)
			respErr := requireTerminalError(t, got)
			require.Contains(t, respErr.Message, "response.completed output is empty after streamed output")
			require.Empty(t, got[len(got)-1].Choices)
		})
	}
}

func TestStreamNonEmptyCompletionRemainsAuthoritative(t *testing.T) {
	message := `{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"final"}]}`
	got := collectStream(t,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.completed","response":{"id":"resp_final","status":"completed","output":[`+message+`]}}`,
	)
	require.Equal(t, "hello", got[0].Choices[0].Delta.Content)
	final := got[len(got)-1]
	require.Nil(t, final.Error)
	require.True(t, final.Done)
	require.False(t, final.IsPartial)
	require.Equal(t, "final", final.Choices[0].Message.Content)
	require.Empty(t, final.Choices[0].Message.ToolCalls)
	require.NotNil(t, final.Choices[0].Message.Replay)
}

func TestStreamEmptyCompletionWithoutOutputStaysValid(t *testing.T) {
	got := collectStream(t,
		`{"type":"response.created","response":{"id":"resp_empty","status":"in_progress","created_at":1700000000,"output":[]}}`,
		`{"type":"response.in_progress","response":{"id":"resp_empty","status":"in_progress","output":[]}}`,
		`{"type":"keepalive"}`,
		`{"type":"response.output_item.added","output_index":0,"item":{}}`,
		`{"type":"response.output_text.delta","delta":""}`,
		`{"type":"response.refusal.delta","delta":""}`,
		`{"type":"response.reasoning_summary_text.delta","delta":""}`,
		`{"type":"response.reasoning_text.delta","delta":""}`,
		`{"type":"response.function_call_arguments.delta","delta":""}`,
		`{"type":"response.completed","response":{"id":"resp_empty","status":"completed","created_at":1700000000,"output":[]}}`,
	)
	require.Len(t, got, 1)
	final := got[0]
	require.Nil(t, final.Error)
	require.True(t, final.Done)
	require.False(t, final.IsPartial)
	require.Empty(t, final.Choices[0].Message.Content)
	require.Empty(t, final.Choices[0].Message.ReasoningContent)
	require.Empty(t, final.Choices[0].Message.ToolCalls)
	require.Nil(t, final.Choices[0].Message.Replay)
	require.NotNil(t, final.Choices[0].FinishReason)
	require.Equal(t, finishReasonStop, *final.Choices[0].FinishReason)
}

func TestStreamDoneItemCountStillRejectsEmptyCompletion(t *testing.T) {
	item := `{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello"}]}`
	got := collectStream(t,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.output_item.done","output_index":0,"item":`+item+`}`,
		emptyCompletedEvent,
	)
	require.Equal(t, "hello", got[0].Choices[0].Delta.Content)
	respErr := requireTerminalError(t, got)
	require.Contains(t, respErr.Message, "output_item.done covered 1")
	require.Empty(t, got[len(got)-1].Choices)
}

func TestStreamFailureAfterOutputStaysTerminal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []string
		want   string
	}{
		{name: "failed", events: []string{
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.failed","response":{"id":"r","status":"failed","error":{"message":"failed"}}}`,
		}, want: "failed"},
		{name: "incomplete", events: []string{
			`{"type":"response.refusal.delta","delta":"no"}`,
			`{"type":"response.incomplete","response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`,
		}, want: "responses request was incomplete"},
		{name: "cancelled", events: []string{
			`{"type":"response.reasoning_text.delta","delta":"hidden"}`,
			`{"type":"response.cancelled","response":{"id":"r","status":"cancelled"}}`,
		}, want: "responses request was cancelled"},
		{name: "eof", events: []string{
			`{"type":"response.function_call_arguments.delta","delta":"{\"a\":1}"}`,
		}, want: "stream ended before a terminal response event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			respErr := requireTerminalError(t, collectStream(t, tc.events...))
			require.Contains(t, respErr.Message, tc.want)
			require.NotContains(t, respErr.Message, "empty after streamed output")
		})
	}
}

func collectStream(t *testing.T, events ...string) []*model.Response {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w, events...)
	}))
	t.Cleanup(srv.Close)
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0)))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)
	return collect(t, ch)
}

func messageContent(resp *model.Response) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}
