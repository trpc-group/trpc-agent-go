//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const responsesProviderProtocol = "openai.responses"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func collect(t *testing.T, ch <-chan *model.Response) []*model.Response {
	t.Helper()
	var out []*model.Response
	for resp := range ch {
		out = append(out, resp)
	}
	return out
}

func mustReadJSONBody(t *testing.T, r *http.Request) map[string]json.RawMessage {
	t.Helper()
	defer r.Body.Close()
	var body map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	return body
}

func writeSSE(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	for _, event := range events {
		_, _ = io.WriteString(w, "data: "+event+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func requireTerminalError(t *testing.T, got []*model.Response) *model.ResponseError {
	t.Helper()
	require.NotEmpty(t, got)
	for _, resp := range got {
		for _, choice := range resp.Choices {
			require.Nil(t, choice.Message.ProviderData, "a failed response must not publish native output")
			require.Empty(t, choice.Message.ToolCalls)
		}
	}
	final := got[len(got)-1]
	require.True(t, final.Done)
	require.NotNil(t, final.Error)
	require.Equal(t, model.ObjectTypeError, final.Object)
	return final.Error
}
