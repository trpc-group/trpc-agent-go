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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	toolImageToolOutput = `[
		{"type":"reasoning","id":"rs_images","status":"completed","summary":[],"encrypted_content":"opaque-image-state"},
		{"type":"function_call","id":"fc_bytes","status":"completed","call_id":"call_bytes","name":"image_bytes","arguments":"{}"},
		{"type":"function_call","id":"fc_url","status":"completed","call_id":"call_url","name":"image_url","arguments":"{}"}
	]`
	toolImagePNGData    = "image data bytes for png"
	toolImagePNGDataURL = "data:image/png;base64,aW1hZ2UgZGF0YSBieXRlcyBmb3IgcG5n"
	toolImageURL        = "https://images.example.test/result.jpg"
)

type toolImageResult struct {
	Text   string
	Data   []byte
	URL    string
	Format string
}

func TestToolResultImagesReachResponsesAndSurviveSQLiteReopen(t *testing.T) {
	tests := []struct {
		name          string
		parallelTools bool
		perCallEvents bool
	}{
		{name: "serial_merged_events"},
		{name: "parallel_merged_events", parallelTools: true},
		{name: "parallel_per_call_events", parallelTools: true, perCallEvents: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runToolResultImagesCase(t, tt.parallelTools, tt.perCallEvents)
		})
	}
}

func runToolResultImagesCase(t *testing.T, parallelTools, perCallEvents bool) {
	t.Helper()
	rt := newToolResultNativeTransport(toolImageToolOutput)
	m := toolResultNativeModel(rt)

	var toolCalls atomic.Int32
	imageBytes := function.NewFunctionTool(func(context.Context, struct{}) (*toolImageResult, error) {
		toolCalls.Add(1)
		return &toolImageResult{Text: "Generated PNG image.", Data: []byte(toolImagePNGData), Format: "png"}, nil
	}, function.WithName("image_bytes"))
	imageURL := function.NewFunctionTool(func(context.Context, struct{}) (*toolImageResult, error) {
		toolCalls.Add(1)
		return &toolImageResult{URL: toolImageURL, Format: "jpeg"}, nil
	}, function.WithName("image_url"))

	var callbacks atomic.Int32
	toolCallbacks := tool.NewCallbacks().RegisterToolResultMessages(
		func(_ context.Context, in *tool.ToolResultMessagesInput) (any, error) {
			callbacks.Add(1)
			result, ok := in.Result.(*toolImageResult)
			if !ok || result == nil {
				return nil, fmt.Errorf("unexpected image tool result type %T", in.Result)
			}
			msg, ok := in.DefaultToolMessage.(model.Message)
			if !ok || msg.Role != model.RoleTool {
				return nil, fmt.Errorf("unexpected default tool message %T", in.DefaultToolMessage)
			}
			// Keep the raw result's base64 JSON out of model-visible tool text.
			msg.Content = ""
			if result.Text != "" {
				text := result.Text
				msg.ContentParts = append(msg.ContentParts, model.ContentPart{
					Type: model.ContentTypeText,
					Text: &text,
				})
			}
			image := &model.Image{
				Data:   append([]byte(nil), result.Data...),
				URL:    result.URL,
				Format: result.Format,
				Detail: "high",
			}
			msg.ContentParts = append(msg.ContentParts, model.ContentPart{
				Type:  model.ContentTypeImage,
				Image: image,
			})
			return msg, nil
		},
	)

	a := llmagent.New(
		"native-images",
		llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{imageBytes, imageURL}),
		llmagent.WithToolCallbacks(toolCallbacks),
		llmagent.WithEnableParallelTools(parallelTools),
	)
	path := filepath.Join(t.TempDir(), "tool-images.db")
	svc := openToolResultNativeSQLite(t, path)
	r := runner.NewRunner("native-images", a, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = r.Close() })
	var runOptions []agent.RunOption
	if perCallEvents {
		runOptions = append(runOptions, agent.WithToolResultEventPerCallEnabled(true))
	}

	first := runToolResultNative(t, r, "show the two images", runOptions...)
	require.Empty(t, toolResultNativeErrors(first))
	require.EqualValues(t, 2, toolCalls.Load())
	require.EqualValues(t, 2, callbacks.Load())
	require.EqualValues(t, 2, rt.calls.Load())

	// The first request has only the user's prompt. The follow-up carries each
	// image on its matching function_call_output.
	<-rt.requests
	secondInput := <-rt.requests
	assertToolImageOutputs(t, secondInput, false)

	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())
	svc = openToolResultNativeSQLite(t, path)
	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName: "native-images", UserID: "user", SessionID: "session",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	r = runner.NewRunner("native-images", a, runner.WithSessionService(svc))
	t.Cleanup(func() { _ = r.Close() })
	continued := runToolResultNative(t, r, "continue with those results", runOptions...)
	require.Empty(t, toolResultNativeErrors(continued))
	require.EqualValues(t, 2, toolCalls.Load(), "replaying completed calls must not execute image tools again")
	require.EqualValues(t, 2, callbacks.Load(), "replaying history must not rerun tool-result callbacks")
	require.EqualValues(t, 3, rt.calls.Load())
	assertToolImageOutputs(t, <-rt.requests, true)
}

func assertToolImageOutputs(t *testing.T, input json.RawMessage, hasFollowupUser bool) {
	t.Helper()
	require.Equal(t, 1, strings.Count(string(input), "aW1hZ2UgZGF0YSBieXRlcyBmb3IgcG5n"),
		"raw image data must appear only in the image data URL, not duplicated in tool text")
	var items []json.RawMessage
	require.NoError(t, json.Unmarshal(input, &items))

	var gotCalls []string
	gotOutputs := make(map[string][]json.RawMessage)
	roleUsers := 0
	for _, raw := range items {
		var item struct {
			Type   string            `json:"type"`
			Role   string            `json:"role"`
			CallID string            `json:"call_id"`
			Output []json.RawMessage `json:"output"`
		}
		require.NoError(t, json.Unmarshal(raw, &item))
		if item.Role == "user" {
			roleUsers++
		}
		switch item.Type {
		case "function_call":
			gotCalls = append(gotCalls, item.CallID)
		case "function_call_output":
			require.NotEmpty(t, item.CallID)
			require.NotEmpty(t, item.Output, "image-bearing result must use content-part output")
			require.NotContains(t, gotOutputs, item.CallID, "each tool call must have exactly one output")
			gotOutputs[item.CallID] = item.Output
		}
	}
	require.ElementsMatch(t, []string{"call_bytes", "call_url"}, gotCalls)
	require.Len(t, gotOutputs, 2)
	if hasFollowupUser {
		require.Equal(t, 2, roleUsers, "history has only the original and continuation prompts")
	} else {
		require.Equal(t, 1, roleUsers, "tool images must not become synthetic user messages")
	}

	assertImageToolOutput(t, gotOutputs["call_bytes"], "Generated PNG image.", toolImagePNGDataURL)
	assertImageToolOutput(t, gotOutputs["call_url"], "", toolImageURL)
}

func assertImageToolOutput(t *testing.T, output []json.RawMessage, wantText, wantImageURL string) {
	t.Helper()
	wantParts := 1
	if wantText != "" {
		wantParts++
	}
	require.Len(t, output, wantParts)

	index := 0
	if wantText != "" {
		var textPart struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		require.NoError(t, json.Unmarshal(output[index], &textPart))
		require.Equal(t, "input_text", textPart.Type)
		require.Equal(t, wantText, textPart.Text)
		index++
	}
	var imagePart struct {
		Type     string `json:"type"`
		Detail   string `json:"detail"`
		ImageURL string `json:"image_url"`
	}
	require.NoError(t, json.Unmarshal(output[index], &imagePart))
	require.Equal(t, "input_image", imagePart.Type)
	require.Equal(t, "high", imagePart.Detail)
	require.Equal(t, wantImageURL, imagePart.ImageURL)
	if strings.HasPrefix(wantImageURL, "data:") {
		_, encoded, ok := strings.Cut(wantImageURL, ",")
		require.True(t, ok)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
		require.Equal(t, toolImagePNGData, string(decoded))
	}
}
