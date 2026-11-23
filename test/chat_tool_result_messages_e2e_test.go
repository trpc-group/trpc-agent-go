//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2e

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const chatToolResultRemoteImage = "https://example.test/second.png"

type chatToolResultMessage struct {
	Role       string          `json:"role"`
	ToolCallID string          `json:"tool_call_id"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []struct {
		ID string `json:"id"`
	} `json:"tool_calls"`
}

func TestChatToolResultMessagesImageContinuation(t *testing.T) {
	var imageBytes bytes.Buffer
	require.NoError(t, png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	expectedImages := map[string]string{
		"first":  "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes()),
		"second": chatToolResultRemoteImage,
	}
	for _, testCase := range []struct {
		name     string
		imageIDs []string
		perCall  bool
	}{
		{name: "single", imageIDs: []string{"first"}},
		{name: "parallel", imageIDs: []string{"first", "second"}},
		{name: "parallel per-call events", imageIDs: []string{"first", "second"}, perCall: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requests := make(chan []chatToolResultMessage, 3)
			server := newChatToolResultServer(t, testCase.imageIDs, requests)
			chatModel := openai.New("chat-fixture",
				openai.WithAPIKey("test"),
				openai.WithBaseURL(server.URL+"/v1"),
				openai.WithOpenAIOptions(option.WithMaxRetries(0)),
			)
			var executions atomic.Int32
			readImage := function.NewFunctionTool(func(_ context.Context, args struct {
				ID string `json:"id"`
			}) (string, error) {
				executions.Add(1)
				return args.ID, nil
			}, function.WithName("read_image"), function.WithDescription("Read a test image by ID."))
			callbacks := tool.NewCallbacks().RegisterToolResultMessages(
				func(_ context.Context, in *tool.ToolResultMessagesInput) (any, error) {
					userMessage := model.Message{Role: model.RoleUser}
					switch in.Result {
					case "first":
						userMessage.ToolID = in.ToolCallID
						userMessage.AddImageData(imageBytes.Bytes(), "", "png")
					case "second":
						userMessage.AddImageURL(chatToolResultRemoteImage, "")
					default:
						return nil, fmt.Errorf("unexpected tool result: %v", in.Result)
					}
					return []model.Message{in.DefaultToolMessage.(model.Message), userMessage}, nil
				},
			)
			databasePath := filepath.Join(t.TempDir(), "chat.db")
			run := func(prompt string) int {
				db, err := sql.Open("sqlite3", databasePath)
				require.NoError(t, err)
				defer db.Close()
				service, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
				require.NoError(t, err)
				defer service.Close()
				chatAgent := llmagent.New("chat",
					llmagent.WithModel(chatModel),
					llmagent.WithTools([]tool.Tool{readImage}),
					llmagent.WithToolCallbacks(callbacks),
					llmagent.WithEnableParallelTools(true),
				)
				chatRunner := runner.NewRunner("chat", chatAgent, runner.WithSessionService(service))
				defer chatRunner.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				events, err := chatRunner.Run(ctx, "user", "session", model.NewUserMessage(prompt),
					agent.WithToolResultEventPerCallEnabled(testCase.perCall),
				)
				require.NoError(t, err)
				resultEvents := 0
				for evt := range events {
					if evt.Response != nil {
						require.Nil(t, evt.Error)
					}
					if !evt.IsPartial && evt.IsToolResultResponse() {
						resultEvents++
					}
				}
				require.NoError(t, ctx.Err())
				return resultEvents
			}
			expectedEvents := 1
			if testCase.perCall {
				expectedEvents = len(testCase.imageIDs)
			}
			require.Equal(t, expectedEvents, run("Read the images."))
			require.Len(t, requests, 2)
			<-requests
			assertChatToolResultImageRound(t, <-requests, testCase.imageIDs, expectedImages)

			require.Zero(t, run("Use those images again."))
			require.Len(t, requests, 1)
			assertChatToolResultImageRound(t, <-requests, testCase.imageIDs, expectedImages)
			require.EqualValues(t, len(testCase.imageIDs), executions.Load())
		})
	}
}

func newChatToolResultServer(
	t *testing.T,
	imageIDs []string,
	requests chan<- []chatToolResultMessage,
) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []chatToolResultMessage `json:"messages"`
		}
		if req.URL.Path != "/v1/chat/completions" || json.NewDecoder(req.Body).Decode(&body) != nil {
			http.Error(w, "invalid chat request", http.StatusBadRequest)
			return
		}
		select {
		case requests <- body.Messages:
		default:
			http.Error(w, "unexpected extra chat request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			toolCalls := make([]map[string]any, 0, len(imageIDs))
			for _, id := range imageIDs {
				toolCalls = append(toolCalls, map[string]any{
					"id": "call_" + id, "type": "function",
					"function": map[string]string{
						"name": "read_image", "arguments": fmt.Sprintf(`{"id":%q}`, id),
					},
				})
			}
			encoded, _ := json.Marshal(toolCalls)
			fmt.Fprintf(w, `{"id":"tool","object":"chat.completion","model":"chat-fixture","choices":[{"index":0,"message":{"role":"assistant","tool_calls":%s},"finish_reason":"tool_calls"}]}`, encoded)
			return
		}
		io.WriteString(w, `{"id":"done","object":"chat.completion","model":"chat-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func assertChatToolResultImageRound(t *testing.T, messages []chatToolResultMessage, ids []string, expectedImages map[string]string) {
	t.Helper()
	callIndex := -1
	for index, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			require.Equal(t, -1, callIndex, "tool round must appear only once")
			callIndex = index
		}
	}
	require.NotEqual(t, -1, callIndex, "missing assistant tool calls")
	call := messages[callIndex]
	require.Equal(t, "assistant", call.Role)
	require.Len(t, call.ToolCalls, len(ids))
	round := messages[callIndex+1:]
	require.GreaterOrEqual(t, len(round), 2*len(ids), "missing tool results or attached images")
	for index, id := range ids {
		callID := "call_" + id
		require.Equal(t, callID, call.ToolCalls[index].ID)
		require.Equal(t, "tool", round[index].Role)
		require.Equal(t, callID, round[index].ToolCallID)
		var text string
		require.NoError(t, json.Unmarshal(round[index].Content, &text))
		require.Equal(t, strconv.Quote(id), text)

		image := round[len(ids)+index]
		require.Equal(t, "user", image.Role)
		require.Empty(t, image.ToolCallID)
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		require.NoError(t, json.Unmarshal(image.Content, &parts))
		require.Len(t, parts, 1)
		require.Equal(t, "image_url", parts[0].Type)
		require.Equal(t, expectedImages[id], parts[0].ImageURL.URL)
	}
	for _, msg := range round[2*len(ids):] {
		require.NotContains(t, string(msg.Content), `"image_url"`, "image must not be sent twice")
	}
}
