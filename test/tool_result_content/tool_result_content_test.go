//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolresultcontent_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
)

type emptyToolInput struct{}

type fixtureModel struct {
	mu            sync.Mutex
	responses     []*model.Response
	requests      []*model.Request
	beforeCapture func(index int, req *model.Request)
}

func newFixtureModel(responses ...*model.Response) *fixtureModel {
	return &fixtureModel{responses: responses}
}

func (m *fixtureModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	index := len(m.requests)
	if index >= len(m.responses) {
		m.mu.Unlock()
		return nil, fmt.Errorf("fixture model received unexpected request %d", index+1)
	}
	if m.beforeCapture != nil {
		m.beforeCapture(index, req)
	}
	// Keep the original message parts so the test can detect accidental aliasing
	// with the result slice returned by a tool.
	request := &model.Request{Messages: append([]model.Message(nil), req.Messages...)}
	m.requests = append(m.requests, request)
	response := m.responses[index]
	m.mu.Unlock()

	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

func (m *fixtureModel) Info() model.Info {
	return model.Info{Name: "tool-result-content-fixture"}
}

func (m *fixtureModel) capturedRequests() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.requests...)
}

func toolCallResponse(calls ...model.ToolCall) *model.Response {
	return &model.Response{
		ID:   "tool-call-response",
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:      model.RoleAssistant,
				ToolCalls: calls,
			},
		}},
	}
}

func finalResponse() *model.Response {
	return &model.Response{
		ID:   "final-response",
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage("done"),
		}},
	}
}

func functionCall(id, name string) model.ToolCall {
	return model.ToolCall{
		ID:   id,
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: []byte(`{}`),
		},
	}
}

func textPart(text string) model.ContentPart {
	return model.ContentPart{Type: model.ContentTypeText, Text: &text}
}

func imagePart(data []byte) model.ContentPart {
	return model.ContentPart{
		Type: model.ContentTypeImage,
		Image: &model.Image{
			Data:   append([]byte(nil), data...),
			Format: "png",
			Detail: "high",
		},
	}
}

func mixedContent(data []byte) model.ToolResultContent {
	return model.ToolResultContent{
		textPart("before"),
		imagePart(data),
		textPart("after"),
	}
}

func functionResultTool(
	name string,
	result model.ToolResultContent,
	calls *atomic.Int32,
	options ...function.Option,
) *function.FunctionTool[emptyToolInput, model.ToolResultContent] {
	toolOptions := []function.Option{
		function.WithName(name),
		function.WithDescription("Returns a fixed fixture result."),
	}
	toolOptions = append(toolOptions, options...)
	return function.NewFunctionTool(
		func(context.Context, emptyToolInput) (model.ToolResultContent, error) {
			if calls != nil {
				calls.Add(1)
			}
			return result, nil
		},
		toolOptions...,
	)
}

func streamResultTool(
	name string,
	result model.ToolResultContent,
) *function.StreamableFunctionTool[emptyToolInput, model.ToolResultContent] {
	return function.NewStreamableFunctionTool[emptyToolInput, model.ToolResultContent](
		func(context.Context, emptyToolInput) (*tool.StreamReader, error) {
			stream := tool.NewStream(1)
			stream.Writer.Send(tool.StreamChunk{
				Content: tool.FinalResultChunk{Result: result},
			}, nil)
			stream.Writer.Close()
			return stream.Reader, nil
		},
		function.WithName(name),
		function.WithDescription("Returns a fixed final stream result."),
	)
}

func run(t *testing.T, r runner.Runner, opts ...agent.RunOption) []*event.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := r.Run(ctx, "user", "session", model.NewUserMessage("run tool"), opts...)
	require.NoError(t, err)
	var got []*event.Event
	for evt := range events {
		got = append(got, evt)
	}
	require.NoError(t, ctx.Err(), "runner must complete within the fixture deadline")
	for _, evt := range got {
		if evt != nil {
			require.Nil(t, evt.Error)
		}
	}
	return got
}

func newRunner(modelStub *fixtureModel, tools []tool.Tool, toolCallbacks *tool.Callbacks, sessionService runner.Option) runner.Runner {
	options := []llmagent.Option{
		llmagent.WithModel(modelStub),
		llmagent.WithTools(tools),
	}
	if toolCallbacks != nil {
		options = append(options, llmagent.WithToolCallbacks(toolCallbacks))
	}
	ag := llmagent.New("tool-result-content-agent", options...)
	var runnerOptions []runner.Option
	if sessionService != nil {
		runnerOptions = append(runnerOptions, sessionService)
	}
	return runner.NewRunner("tool-result-content-app", ag, runnerOptions...)
}

func toolMessage(t *testing.T, req *model.Request, id string) model.Message {
	t.Helper()
	for _, msg := range req.Messages {
		if msg.Role == model.RoleTool && msg.ToolID == id {
			return msg
		}
	}
	t.Fatalf("request is missing tool result %q", id)
	return model.Message{}
}

func assertImageAppearsOnce(t *testing.T, req *model.Request, msg model.Message, data []byte) {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString(data)
	serialized, err := json.Marshal(req.Messages)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(serialized), encoded),
		"the image bytes must occur once in the request, not again in JSON tool text")
	require.NotContains(t, msg.Content, encoded)
	require.NotContains(t, msg.Content, `"image"`)
}

func TestRunnerToolResultContentSerialMessagesAndSQLiteReplay(t *testing.T) {
	imageData := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
	imageResult := mixedContent(imageData)
	textResult := model.ToolResultContent{
		textPart("left"),
		textPart("right"),
	}
	var toolCalls atomic.Int32
	imageTool := functionResultTool("image_result", imageResult, &toolCalls)
	textTool := functionResultTool("text_result", textResult, &toolCalls)
	modelStub := newFixtureModel(
		toolCallResponse(functionCall("call-image", "image_result")),
		toolCallResponse(functionCall("call-text", "text_result")),
		finalResponse(),
		finalResponse(),
	)
	modelStub.beforeCapture = func(index int, _ *model.Request) {
		if index == 1 {
			// Mutating the source slice after tool execution must not rewrite the
			// already assembled message sent to the model.
			imageResult[0] = textPart("mutated")
		}
	}

	path := filepath.Join(t.TempDir(), "tool-result-content.db")
	openService := func() *sqlite.Service {
		db, err := sql.Open("sqlite3", path)
		require.NoError(t, err)
		svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false))
		require.NoError(t, err)
		return svc
	}
	service := openService()
	var activeRunner runner.Runner
	t.Cleanup(func() {
		if activeRunner != nil {
			_ = activeRunner.Close()
		}
		if service != nil {
			_ = service.Close()
		}
	})
	activeRunner = newRunner(modelStub, []tool.Tool{imageTool, textTool}, nil,
		runner.WithSessionService(service))

	run(t, activeRunner)
	requests := modelStub.capturedRequests()
	require.Len(t, requests, 3)
	require.EqualValues(t, 2, toolCalls.Load())
	imageMessage := toolMessage(t, requests[1], "call-image")
	require.Equal(t, model.RoleTool, imageMessage.Role)
	require.Equal(t, "image_result", imageMessage.ToolName)
	require.Empty(t, imageMessage.Content,
		"mixed text and image content is carried in ContentParts without duplicating text")
	require.Equal(t, imageResultParts(imageData), imageMessage.ContentParts)
	assertImageAppearsOnce(t, requests[1], imageMessage, imageData)

	textMessage := toolMessage(t, requests[2], "call-text")
	require.Equal(t, model.RoleTool, textMessage.Role)
	require.Equal(t, "leftright", textMessage.Content,
		"text-only result parts must concatenate without separators")
	require.Nil(t, textMessage.ContentParts)
	// Prior tool outputs remain present when later serial calls are projected.
	require.Empty(t, toolMessage(t, requests[2], "call-image").Content)

	require.NoError(t, activeRunner.Close())
	activeRunner = nil
	require.NoError(t, service.Close())
	service = nil

	// Replaying the session from a fresh SQLite service must preserve the
	// multimodal tool receipt without re-running either tool.
	service = openService()
	activeRunner = newRunner(modelStub, []tool.Tool{imageTool, textTool}, nil,
		runner.WithSessionService(service))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := activeRunner.Run(ctx, "user", "session", model.NewUserMessage("recall"))
	require.NoError(t, err)
	for evt := range events {
		if evt != nil {
			require.Nil(t, evt.Error)
		}
	}
	require.NoError(t, ctx.Err())
	requests = modelStub.capturedRequests()
	require.Len(t, requests, 4)
	replayedImage := toolMessage(t, requests[3], "call-image")
	require.Empty(t, replayedImage.Content)
	require.Equal(t, imageResultParts(imageData), replayedImage.ContentParts)
	assertImageAppearsOnce(t, requests[3], replayedImage, imageData)
	require.EqualValues(t, 2, toolCalls.Load(), "replaying history must not execute completed tools")
}

func imageResultParts(imageData []byte) []model.ContentPart {
	return []model.ContentPart{
		textPart("before"),
		imagePart(imageData),
		textPart("after"),
	}
}

func TestRunnerToolResultContentParallelKeepsEmptyReceipts(t *testing.T) {
	for _, perToolEvent := range []bool{false, true} {
		name := "merged-events"
		if perToolEvent {
			name = "per-tool-events"
		}
		t.Run(name, func(t *testing.T) {
			imageData := []byte{0xde, 0xad, 0xbe, 0xef}
			imageResult := mixedContent(imageData)
			var toolCalls atomic.Int32
			imageTool := functionResultTool("image_result", imageResult, &toolCalls)
			emptyTool := functionResultTool("empty_result", nil, &toolCalls)
			modelStub := newFixtureModel(
				toolCallResponse(
					functionCall("call-image", "image_result"),
					functionCall("call-empty", "empty_result"),
				),
				finalResponse(),
			)
			ag := llmagent.New("parallel-tool-result-content-agent",
				llmagent.WithModel(modelStub),
				llmagent.WithTools([]tool.Tool{imageTool, emptyTool}),
				llmagent.WithEnableParallelTools(true),
			)
			r := runner.NewRunner("parallel-tool-result-content-app", ag)
			t.Cleanup(func() { _ = r.Close() })
			var opts []agent.RunOption
			if perToolEvent {
				opts = append(opts, agent.WithToolResultEventPerCallEnabled(true))
			}
			events := run(t, r, opts...)
			requests := modelStub.capturedRequests()
			require.Len(t, requests, 2)
			require.EqualValues(t, 2, toolCalls.Load())

			imageMessage := toolMessage(t, requests[1], "call-image")
			require.Empty(t, imageMessage.Content,
				"mixed text and image content is carried in ContentParts without duplicating text")
			require.Equal(t, imageResultParts(imageData), imageMessage.ContentParts)
			assertImageAppearsOnce(t, requests[1], imageMessage, imageData)
			emptyMessage := toolMessage(t, requests[1], "call-empty")
			require.Equal(t, model.RoleTool, emptyMessage.Role)
			require.Empty(t, emptyMessage.Content)
			require.Len(t, emptyMessage.ContentParts, 1,
				"an explicit empty text part distinguishes a completed result from a placeholder")
			require.Equal(t, model.ContentTypeText, emptyMessage.ContentParts[0].Type)
			require.NotNil(t, emptyMessage.ContentParts[0].Text)
			require.Empty(t, *emptyMessage.ContentParts[0].Text)

			var resultEvents []*event.Event
			for _, evt := range events {
				if evt != nil && evt.IsToolResultResponse() {
					resultEvents = append(resultEvents, evt)
				}
			}
			if perToolEvent {
				require.Len(t, resultEvents, 2)
				for _, evt := range resultEvents {
					require.Len(t, evt.Response.Choices, 1)
				}
			} else {
				require.Len(t, resultEvents, 1)
				require.Len(t, resultEvents[0].Response.Choices, 2)
			}
		})
	}
}

func TestRunnerFinalResultChunkToolResultContent(t *testing.T) {
	imageData := []byte{0xca, 0xfe, 0xba, 0xbe}
	modelStub := newFixtureModel(
		toolCallResponse(functionCall("call-stream", "stream_result")),
		finalResponse(),
	)
	ag := llmagent.New("stream-tool-result-content-agent",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{streamResultTool("stream_result", mixedContent(imageData))}),
	)
	r := runner.NewRunner("stream-tool-result-content-app", ag)
	t.Cleanup(func() { _ = r.Close() })
	run(t, r)

	requests := modelStub.capturedRequests()
	require.Len(t, requests, 2)
	msg := toolMessage(t, requests[1], "call-stream")
	require.Equal(t, model.RoleTool, msg.Role)
	require.Empty(t, msg.Content,
		"mixed text and image content is carried in ContentParts without duplicating text")
	require.Equal(t, imageResultParts(imageData), msg.ContentParts)
	assertImageAppearsOnce(t, requests[1], msg, imageData)
}

func TestRunnerToolResultContentFormatterAndToolResultMessagesOverride(t *testing.T) {
	result := mixedContent([]byte{0x01, 0x02, 0x03})
	t.Run("formatter", func(t *testing.T) {
		formatter := resultformat.FormatterFunc[model.ToolResultContent](
			func(context.Context, model.ToolResultContent) (string, error) {
				return "formatted result", nil
			},
		)
		toolWithFormatter := functionResultTool(
			"formatted_result", result, nil, function.WithResultFormatter(formatter),
		)
		msg := runOneToolAndCapture(t, toolWithFormatter, nil)
		require.Equal(t, "formatted result", msg.Content)
		require.Nil(t, msg.ContentParts,
			"a configured result formatter keeps the existing string-output contract")
	})

	t.Run("ToolResultMessages callback", func(t *testing.T) {
		callbacks := &tool.Callbacks{
			ToolResultMessages: func(
				_ context.Context,
				input *tool.ToolResultMessagesInput,
			) (any, error) {
				return []model.Message{
					model.NewToolMessage(input.ToolCallID, input.ToolName, "callback override"),
				}, nil
			},
		}
		toolWithoutHook := functionResultTool("callback_result", result, nil)
		msg := runOneToolAndCapture(t, toolWithoutHook, callbacks)
		require.Equal(t, "callback override", msg.Content)
		require.Nil(t, msg.ContentParts,
			"ToolResultMessages continues to replace the default multimodal message")
	})
}

func runOneToolAndCapture(
	t *testing.T,
	toolUnderTest tool.Tool,
	callbacks *tool.Callbacks,
) model.Message {
	t.Helper()
	name := toolUnderTest.Declaration().Name
	modelStub := newFixtureModel(
		toolCallResponse(functionCall("call-"+name, name)),
		finalResponse(),
	)
	r := newRunner(modelStub, []tool.Tool{toolUnderTest}, callbacks, nil)
	t.Cleanup(func() { _ = r.Close() })
	run(t, r)
	requests := modelStub.capturedRequests()
	require.Len(t, requests, 2)
	return toolMessage(t, requests[1], "call-"+name)
}
