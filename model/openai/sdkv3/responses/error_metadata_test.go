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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const pendingFunctionCallEvents = `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_pending","status":"in_progress","call_id":"call_pending","name":"lookup","arguments":""}}`

func TestStreamErrorEventPreservesCodeAndParam(t *testing.T) {
	for _, tc := range []struct {
		name      string
		event     string
		wantMsg   string
		wantCode  string
		wantParam string
	}{
		{
			name:      "message and metadata",
			event:     `{"type":"error","message":"authentication failed","code":"invalid_api_key","param":"api_key"}`,
			wantMsg:   "authentication failed",
			wantCode:  "invalid_api_key",
			wantParam: "api_key",
		},
		{
			name:      "fallback message retains metadata",
			event:     `{"type":"error","code":"rate_limit","param":"input"}`,
			wantMsg:   "responses stream did not complete",
			wantCode:  "rate_limit",
			wantParam: "input",
		},
		{
			name:    "missing metadata stays nil",
			event:   `{"type":"error","message":"upstream failure"}`,
			wantMsg: "upstream failure",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := collectStream(t, pendingFunctionCallEvents, tc.event)
			respErr := requireTerminalError(t, got)
			require.Equal(t, tc.wantMsg, respErr.Message)
			if tc.wantCode == "" {
				require.Nil(t, respErr.Code)
			} else {
				require.NotNil(t, respErr.Code)
				require.Equal(t, tc.wantCode, *respErr.Code)
			}
			if tc.wantParam == "" {
				require.Nil(t, respErr.Param)
			} else {
				require.NotNil(t, respErr.Param)
				require.Equal(t, tc.wantParam, *respErr.Param)
			}
		})
	}
}

func TestStreamFailedResponsePreservesNestedCode(t *testing.T) {
	for _, tc := range []struct {
		name      string
		errorJSON string
		wantMsg   string
	}{
		{
			name:      "nested message and code",
			errorJSON: `{"code":"server_error","message":"nested failure"}`,
			wantMsg:   "nested failure",
		},
		{
			name:      "fallback message retains code",
			errorJSON: `{"code":"server_error"}`,
			wantMsg:   "responses request failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed := `{"type":"response.failed","response":{"id":"resp_failed","status":"failed","model":"gpt-5-resolved","created_at":1700000000,"error":` + tc.errorJSON + `,"usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14}}}`
			got := collectStream(t, pendingFunctionCallEvents, failed)
			respErr := requireTerminalError(t, got)
			require.Equal(t, tc.wantMsg, respErr.Message)
			require.NotNil(t, respErr.Code)
			require.Equal(t, "server_error", *respErr.Code)
			// The nested Responses SDK error has no Param field.
			require.Nil(t, respErr.Param)

			final := got[len(got)-1]
			require.Equal(t, "resp_failed", final.ID)
			require.Equal(t, "gpt-5-resolved", final.Model)
			require.Equal(t, int64(1700000000), final.Created)
			require.NotNil(t, final.Usage)
			require.Equal(t, 11, final.Usage.PromptTokens)
			require.Equal(t, 3, final.Usage.CompletionTokens)
			require.Equal(t, 14, final.Usage.TotalTokens)
		})
	}
}

func TestNonStreamFailedResponsePreservesCodeWithoutMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_failed","status":"failed","model":"gpt-5-resolved","created_at":1700000000,"error":{"code":"server_error"},"usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14}}`)
	}))
	defer srv.Close()

	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL))
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}})
	require.NoError(t, err)
	got := collect(t, ch)
	respErr := requireTerminalError(t, got)
	require.Equal(t, "responses request failed", respErr.Message)
	require.NotNil(t, respErr.Code)
	require.Equal(t, "server_error", *respErr.Code)
	// The nested Responses SDK error has no Param field.
	require.Nil(t, respErr.Param)

	final := got[len(got)-1]
	require.Equal(t, "resp_failed", final.ID)
	require.Equal(t, "gpt-5-resolved", final.Model)
	require.Equal(t, int64(1700000000), final.Created)
	require.NotNil(t, final.Usage)
	require.Equal(t, 11, final.Usage.PromptTokens)
	require.Equal(t, 3, final.Usage.CompletionTokens)
	require.Equal(t, 14, final.Usage.TotalTokens)
}
