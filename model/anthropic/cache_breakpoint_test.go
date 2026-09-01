//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package anthropic

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	oneHourCacheControl = `{"type":"ephemeral","ttl":"1h"}`

	stubMessageJSON = `{"id":"m1","model":"claude","role":"assistant","type":"message",` +
		`"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},` +
		`"content":[{"type":"text","text":"done"}]}`
)

// stubMessageSSE is the smallest streamed reply the accumulator accepts.
var stubMessageSSE = strings.Join([]string{
	"event: message_start",
	`data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude","content":[]}}`,
	"",
	"event: content_block_start",
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	"",
	"event: content_block_delta",
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
	"",
	"event: content_block_stop",
	`data: {"type":"content_block_stop","index":0}`,
	"",
	"event: message_delta",
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`,
	"",
	"event: message_stop",
	`data: {"type":"message_stop"}`,
	"",
}, "\n")

// stubResponse is a successful reply of the shape the request asked for.
func stubResponse(stream bool) *http.Response {
	h := make(http.Header)
	body := stubMessageJSON
	h.Set("Content-Type", "application/json")
	if stream {
		body = stubMessageSSE
		h.Set("Content-Type", "text/event-stream")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

// withStubTransport routes the model's HTTP client through rt for the test.
func withStubTransport(t *testing.T, rt func(*http.Request) (*http.Response, error)) {
	t.Helper()
	orig := model.DefaultNewHTTPClient
	t.Cleanup(func() { model.DefaultNewHTTPClient = orig })
	model.DefaultNewHTTPClient = func(_ ...HTTPClientOption) model.HTTPClient {
		return &http.Client{Transport: rtFunc(rt)}
	}
}

// captureOutgoingBody runs GenerateContent and returns the request body as the
// transport received it: after the callback, after every request option, after
// the middleware. That is the only place the tool-result breakpoint can be
// observed, since it is placed on the serialized body.
//
// mutate stands in for a caller's own request callback and may be nil. It is
// installed as the model's callback, so opts must not supply another: the model
// keeps one, and the last option wins.
func captureOutgoingBody(
	t *testing.T,
	request *model.Request,
	mutate func(*anthropic.MessageNewParams),
	opts ...Option,
) []byte {
	t.Helper()
	bodies := captureOutgoingBodies(t, request, mutate, nil, opts...)
	require.Len(t, bodies, 1, "exactly one request must reach the transport")
	return bodies[0]
}

// captureOutgoingBodies is captureOutgoingBody for a transport that may be
// asked more than once. respond decides each attempt's reply and may be nil for
// a stub success.
func captureOutgoingBodies(
	t *testing.T,
	request *model.Request,
	mutate func(*anthropic.MessageNewParams),
	respond func(attempt int) *http.Response,
	opts ...Option,
) [][]byte {
	t.Helper()
	var bodies [][]byte
	withStubTransport(t, func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		bodies = append(bodies, body)
		if respond != nil {
			return respond(len(bodies)), nil
		}
		return stubResponse(request.Stream), nil
	})
	if mutate != nil {
		opts = append(opts, WithChatRequestCallback(func(_ context.Context, req *anthropic.MessageNewParams) {
			mutate(req)
		}))
	}
	ch, err := New("claude-3-5-sonnet", opts...).GenerateContent(context.Background(), request)
	require.NoError(t, err)
	for range ch { //revive:disable-line:empty-block
	}
	require.NotEmpty(t, bodies, "the request must have reached the transport")
	return bodies
}

// cacheControlAt returns the marker on a block of the serialized request, by
// gjson path, and whether there is one.
func cacheControlAt(body []byte, path string) (gjson.Result, bool) {
	marker := gjson.GetBytes(body, path+".cache_control")
	return marker, marker.IsObject()
}

// lastBlock is the gjson path of a message's final content block.
func lastBlock(body []byte, message int) string {
	blocks := gjson.GetBytes(body, fmt.Sprintf("messages.%d.content", message)).Array()
	return fmt.Sprintf("messages.%d.content.%d", message, len(blocks)-1)
}

// toolResultTurnMessages is a turn whose tool output is followed by a per-request
// note. convertMessages merges the two tool results into one user message, so the
// assembled request is [user, assistant, tool results, note].
//
// The trailing note earns its place: as the last cacheable block it is where the
// API puts the marker it adds for top-level cache_control, making that marker a
// breakpoint of its own rather than a duplicate of the tool-result one.
func toolResultTurnMessages() []model.Message {
	return []model.Message{
		{Role: model.RoleSystem, Content: "system policy"},
		{Role: model.RoleUser, Content: "read both files"},
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Function: model.FunctionDefinitionParam{Name: "read", Arguments: []byte(`{}`)}},
				{ID: "call_2", Function: model.FunctionDefinitionParam{Name: "read", Arguments: []byte(`{}`)}},
			},
		},
		{Role: model.RoleTool, ToolID: "call_1", Content: "file one"},
		{Role: model.RoleTool, ToolID: "call_2", Content: "file two"},
		{Role: model.RoleUser, Content: "[Turn 3/40]"},
	}
}

// readTool is the single tool the breakpoint tests declare, so that the tools
// breakpoint has something to land on.
func readTool() map[string]tool.Tool {
	return map[string]tool.Tool{
		"read": stubTool{decl: &tool.Declaration{
			Name:        "read",
			Description: "read a file",
			InputSchema: &tool.Schema{Type: "object"},
		}},
	}
}

// allCaching turns on every built-in breakpoint, which is the configuration
// that leaves exactly one slot for the tool-result marker.
func allCaching() []Option {
	return []Option{WithCacheSystemPrompt(true), WithCacheTools(true), WithCacheMessages(true)}
}

// A turn's tool output is marked in the request that carries it, alongside the
// last-assistant breakpoint, with a trailing note left outside the cached prefix.
func TestGenerateContent_ToolResultBreakpoint(t *testing.T) {
	body := captureOutgoingBody(t,
		&model.Request{Messages: toolResultTurnMessages(), Tools: readTool()},
		nil, WithCacheMessages(true))

	require.Len(t, gjson.GetBytes(body, "messages").Array(), 4)
	_, marked := cacheControlAt(body, lastBlock(body, 1))
	assert.True(t, marked, "last assistant message should be marked")
	marker, marked := cacheControlAt(body, "messages.2.content.1")
	assert.True(t, marked, "last tool result block should be marked")
	assert.Equal(t, "ephemeral", marker.Get("type").String())
	_, marked = cacheControlAt(body, "messages.2.content.0")
	assert.False(t, marked, "only the final tool result block is marked")
	_, marked = cacheControlAt(body, "messages.3.content.0")
	assert.False(t, marked, "the trailing per-request note stays outside the prefix")
	assert.Equal(t, 2, countCacheBreakpoints(body),
		"system and tools caching are off, so these are the only two markers")
}

// The streaming path sends the same body through the same middleware.
func TestGenerateContent_ToolResultBreakpointReachesTheStreamingPath(t *testing.T) {
	body := captureOutgoingBody(t,
		&model.Request{
			Messages:         toolResultTurnMessages(),
			Tools:            readTool(),
			GenerationConfig: model.GenerationConfig{Stream: true},
		},
		nil, WithCacheMessages(true))

	assert.True(t, gjson.GetBytes(body, "stream").Bool(), "precondition: a streaming request")
	_, marked := cacheControlAt(body, "messages.2.content.1")
	assert.True(t, marked, "the streaming request carries the tool-result marker too")
	assert.Equal(t, 2, countCacheBreakpoints(body))
}

// Message caching off means no message breakpoints at all, and the body goes
// out as the SDK serialized it.
func TestGenerateContent_MessageCachingOffPlacesNoMarker(t *testing.T) {
	body := captureOutgoingBody(t,
		&model.Request{Messages: toolResultTurnMessages(), Tools: readTool()},
		nil, WithCacheMessages(false))

	assert.Zero(t, countCacheBreakpoints(body))
}

// The model's own breakpoints never push a request past the limit, and the
// tool-result one is what goes unplaced when a caller claims the last slot —
// from the callback, or from a request or client option the SDK applies to the
// serialized body, which the typed request never shows.
func TestGenerateContent_CacheBreakpointBudget(t *testing.T) {
	topLevel := func(req *anthropic.MessageNewParams) {
		req.CacheControl = anthropic.NewCacheControlEphemeralParam()
	}
	tests := []struct {
		name   string
		mutate func(*anthropic.MessageNewParams)
		opts   []Option
		// wantToolResult is whether a slot was left for the tool-result marker;
		// wantBreakpoints is the total on the wire, never more than four.
		wantToolResult  bool
		wantBreakpoints int
	}{
		{
			name:            "without a caller marker the tool result is marked",
			wantToolResult:  true,
			wantBreakpoints: 4,
		},
		{
			name:            "top-level cache control from the callback claims the slot",
			mutate:          topLevel,
			wantToolResult:  false,
			wantBreakpoints: 4,
		},
		{
			name: "top-level cache control from a request option claims the slot",
			opts: []Option{WithAnthropicRequestOptions(
				anthropicopt.WithJSONSet("cache_control", map[string]any{"type": "ephemeral"}),
			)},
			wantToolResult:  false,
			wantBreakpoints: 4,
		},
		{
			name: "top-level cache control from a client option claims the slot",
			opts: []Option{WithAnthropicClientOptions(
				anthropicopt.WithJSONSet("cache_control", map[string]any{"type": "ephemeral"}),
			)},
			wantToolResult:  false,
			wantBreakpoints: 4,
		},
		{
			name: "an explicit marker from a request option claims the slot",
			opts: []Option{WithAnthropicRequestOptions(
				anthropicopt.WithJSONSet("messages.3.content.0.cache_control", map[string]any{"type": "ephemeral"}),
			)},
			wantToolResult:  false,
			wantBreakpoints: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := captureOutgoingBody(t,
				&model.Request{Messages: toolResultTurnMessages(), Tools: readTool()},
				tt.mutate, append(allCaching(), tt.opts...)...)

			require.Len(t, gjson.GetBytes(body, "messages").Array(), 4)
			_, marked := cacheControlAt(body, "messages.2.content.1")
			assert.Equal(t, tt.wantToolResult, marked, "tool-result breakpoint presence")
			_, marked = cacheControlAt(body, lastBlock(body, 1))
			assert.True(t, marked, "the last-assistant breakpoint is unconditional")
			_, marked = cacheControlAt(body, "system.0")
			assert.True(t, marked, "the system breakpoint is unconditional")
			assert.Equal(t, tt.wantBreakpoints, countCacheBreakpoints(body),
				"the request must never exceed the breakpoints Anthropic accepts")
			assert.LessOrEqual(t, countCacheBreakpoints(body), cacheBreakpointLimit)
		})
	}
}

// A request callback that rewrites the message list, alone and together with
// spending the last free slot.
//
// A marker placed during construction survives neither move: an appended tool
// result leaves it on a message that is no longer the newest, and top-level
// cache control claims the slot it was occupying, putting the request one over
// the limit. Choosing from the serialized list, only while a slot is free,
// settles both.
func TestGenerateContent_ToolResultBreakpointFollowsTheCallback(t *testing.T) {
	// appendToolResult splices in a second tool-result message, unmarked, the
	// way a caller adding tool output of its own would.
	appendToolResult := func(req *anthropic.MessageNewParams) {
		req.Messages = append(req.Messages,
			anthropic.NewUserMessage(anthropic.NewToolResultBlock("call_3", "file three", false)))
	}

	tests := []struct {
		name   string
		mutate func(*anthropic.MessageNewParams)
		// wantMarked is the message index that must carry the tool-result
		// marker, or -1 when no slot was left for one.
		wantMarked      int
		wantBreakpoints int
	}{
		{
			name:            "the marker follows the appended tool result",
			mutate:          appendToolResult,
			wantMarked:      4,
			wantBreakpoints: 4,
		},
		{
			name: "no marker at all once the callback spends the last slot",
			mutate: func(req *anthropic.MessageNewParams) {
				appendToolResult(req)
				req.CacheControl = anthropic.NewCacheControlEphemeralParam()
			},
			wantMarked:      -1,
			wantBreakpoints: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := captureOutgoingBody(t,
				&model.Request{Messages: toolResultTurnMessages(), Tools: readTool()},
				tt.mutate, allCaching()...)

			// [user, assistant, tool results, note, appended tool result].
			messages := gjson.GetBytes(body, "messages").Array()
			require.Len(t, messages, 5)
			for i := range messages {
				_, marked := cacheControlAt(body, lastBlock(body, i))
				switch i {
				case 1:
					assert.True(t, marked, "the last-assistant breakpoint is unconditional")
				case tt.wantMarked:
					assert.True(t, marked, "the newest tool result carries the marker")
				default:
					assert.False(t, marked, "message %d must not be marked", i)
				}
			}
			assert.Equal(t, tt.wantBreakpoints, countCacheBreakpoints(body))
			assert.LessOrEqual(t, countCacheBreakpoints(body), cacheBreakpointLimit,
				"the request must never exceed the breakpoints Anthropic accepts")
		})
	}
}

// A caller that configures one-hour caching — the last-assistant marker aligned
// to 1h and top-level cache control set to 1h — must not have a five-minute
// marker slipped in behind it.
//
// The API places the automatic marker on the last cacheable block and rejects
// TTLs that shorten and then lengthen again, so the tool-result marker has to
// carry the caller's TTL when it precedes the automatic one, and has to stay away
// entirely when the tool result *is* the last cacheable block: the automatic
// marker already covers it, and a second, shorter one on the same block is a 400.
// The caller's TTL may arrive through the callback or through a request option;
// the serialized body shows both.
func TestGenerateContent_ToolResultBreakpointMatchesTheCallersTTL(t *testing.T) {
	oneHour := anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL1h}
	// alignAssistant is the caller's side of the existing marker: the assistant
	// marker the model placed is aligned to 1h.
	alignAssistant := func(req *anthropic.MessageNewParams) {
		assistant := req.Messages[1].Content
		last := assistant[len(assistant)-1].OfToolUse
		require.NotNil(t, last)
		last.CacheControl = oneHour
	}
	// configureOneHour also enables automatic caching at 1h.
	configureOneHour := func(req *anthropic.MessageNewParams) {
		alignAssistant(req)
		req.CacheControl = oneHour
	}
	// A turn that ends on its tool output, with no per-request note after it.
	finalToolResult := toolResultTurnMessages()[:5]

	tests := []struct {
		name     string
		messages []model.Message
		mutate   func(*anthropic.MessageNewParams)
		opts     []Option
		// wantMarked says whether the tool result may carry a marker at all;
		// wantTTL is the TTL it must then have, "" being the default.
		wantMarked      bool
		wantTTL         string
		wantBreakpoints int
	}{
		{
			name:            "the marker ahead of the automatic one matches its TTL",
			messages:        toolResultTurnMessages(),
			mutate:          configureOneHour,
			wantMarked:      true,
			wantTTL:         "1h",
			wantBreakpoints: 3,
		},
		{
			name:     "a TTL set through a request option is matched the same way",
			messages: toolResultTurnMessages(),
			mutate:   alignAssistant,
			opts: []Option{WithAnthropicRequestOptions(
				anthropicopt.WithJSONSet("cache_control", map[string]any{"type": "ephemeral", "ttl": "1h"}),
			)},
			wantMarked:      true,
			wantTTL:         "1h",
			wantBreakpoints: 3,
		},
		{
			name:            "the automatic marker on the final tool result is left alone",
			messages:        finalToolResult,
			mutate:          configureOneHour,
			wantMarked:      false,
			wantBreakpoints: 2,
		},
		{
			// Without automatic caching there is nothing after the marker for
			// its TTL to conflict with, so the default is still the default.
			name:            "no automatic marker keeps the default TTL",
			messages:        toolResultTurnMessages(),
			mutate:          alignAssistant,
			wantMarked:      true,
			wantTTL:         "",
			wantBreakpoints: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := captureOutgoingBody(t,
				&model.Request{Messages: tt.messages, Tools: readTool()},
				tt.mutate, append([]Option{WithCacheMessages(true)}, tt.opts...)...)

			marker, marked := cacheControlAt(body, lastBlock(body, 2))
			if !tt.wantMarked {
				assert.False(t, marked, "the automatic marker already covers the final tool result")
			} else {
				require.True(t, marked, "the tool result carries the marker")
				assert.Equal(t, tt.wantTTL, marker.Get("ttl").String(),
					"the marker's TTL must agree with what follows it")
			}
			assert.Equal(t, tt.wantBreakpoints, countCacheBreakpoints(body))
		})
	}
}

// A marker the caller placed on the newest tool result is the caller's. The
// model does not replace it: doing so would shorten a one-hour entry to five
// minutes, and with a one-hour marker after it, make the TTLs along the prompt
// shorten and then lengthen, which the API rejects. Whether the caller placed it
// from the callback or through a request option, the serialized body shows it.
func TestGenerateContent_PreservesTheCallersToolResultMarker(t *testing.T) {
	oneHour := anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL1h}
	tests := []struct {
		name   string
		mutate func(*anthropic.MessageNewParams)
		opts   []Option
	}{
		{
			name: "placed from the callback",
			mutate: func(req *anthropic.MessageNewParams) {
				results := req.Messages[2].Content
				last := results[len(results)-1].OfToolResult
				require.NotNil(t, last)
				last.CacheControl = oneHour
				note := req.Messages[3].Content[0].OfText
				require.NotNil(t, note)
				note.CacheControl = oneHour
			},
		},
		{
			name: "placed through a request option",
			opts: []Option{WithAnthropicRequestOptions(
				anthropicopt.WithJSONSet("messages.2.content.1.cache_control",
					map[string]any{"type": "ephemeral", "ttl": "1h"}),
				anthropicopt.WithJSONSet("messages.3.content.0.cache_control",
					map[string]any{"type": "ephemeral", "ttl": "1h"}),
			)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := captureOutgoingBody(t,
				&model.Request{Messages: toolResultTurnMessages(), Tools: readTool()},
				tt.mutate, append([]Option{WithCacheMessages(true)}, tt.opts...)...)

			marker, marked := cacheControlAt(body, "messages.2.content.1")
			require.True(t, marked)
			assert.Equal(t, "1h", marker.Get("ttl").String(),
				"the caller's one-hour marker must not be replaced by the default")
			assert.Equal(t, 3, countCacheBreakpoints(body),
				"assistant, the caller's tool-result marker, the caller's trailing marker; nothing added")
		})
	}
}

// The edge of that guarantee, and the boundary is deliberate.
//
// A callback adding two markers of its own is over the limit on its own
// arithmetic: three unconditional breakpoints plus two is five, which was already
// a 400 before the tool-result breakpoint existed. Withholding ours returns the
// count to that pre-existing number and stops there — going further would discard
// the caller's placement or the system and tools ones, trading an error that names
// the problem for a silent cache regression.
func TestGenerateContent_CallerOverSubscribesBreakpoints(t *testing.T) {
	body := captureOutgoingBody(t,
		&model.Request{
			Messages: []model.Message{
				{Role: model.RoleSystem, Content: "system policy"},
				{Role: model.RoleUser, Content: "read both files"},
				{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "call_1", Function: model.FunctionDefinitionParam{Name: "read", Arguments: []byte(`{}`)}},
					},
				},
				{Role: model.RoleTool, ToolID: "call_1", Content: "file one"},
				{Role: model.RoleUser, Content: "[Turn 3/40]"},
			},
			Tools: readTool(),
		},
		func(req *anthropic.MessageNewParams) {
			// Two markers of the caller's own: automatic placement, plus an
			// explicit one on the first user turn.
			req.CacheControl = anthropic.NewCacheControlEphemeralParam()
			first := req.Messages[0].Content[0].OfText
			require.NotNil(t, first)
			first.CacheControl = anthropic.NewCacheControlEphemeralParam()
		},
		allCaching()...,
	)

	_, marked := cacheControlAt(body, lastBlock(body, 2))
	assert.False(t, marked, "the tool-result slot is left alone even though leaving it is not enough")

	// The count this request would have had before this breakpoint existed.
	assert.Equal(t, 5, countCacheBreakpoints(body),
		"the model withholds its own marker and leaves the caller's alone")
	assert.True(t, gjson.GetBytes(body, "cache_control").IsObject(),
		"the caller's top-level cache control is untouched")
	_, marked = cacheControlAt(body, "messages.0.content.0")
	assert.True(t, marked, "the caller's explicit marker is untouched")
	_, marked = cacheControlAt(body, "system.0")
	assert.True(t, marked, "the system breakpoint is not sacrificed to force a fit")
}

// The SDK retries from the original body, so the marker has to be placed on
// every attempt rather than once. An overloaded reply that asks for an immediate
// retry shows both attempts going out marked, and neither over budget.
func TestGenerateContent_ToolResultBreakpointIsPlacedOnEveryAttempt(t *testing.T) {
	bodies := captureOutgoingBodies(t,
		&model.Request{Messages: toolResultTurnMessages(), Tools: readTool()},
		nil,
		func(attempt int) *http.Response {
			if attempt == 1 {
				h := make(http.Header)
				h.Set("Content-Type", "application/json")
				h.Set("Retry-After-Ms", "1")
				return &http.Response{
					StatusCode: 529,
					Header:     h,
					Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)),
				}
			}
			return stubResponse(false)
		},
		allCaching()...,
	)

	require.Len(t, bodies, 2, "the overloaded reply must be retried once")
	for i, body := range bodies {
		_, marked := cacheControlAt(body, "messages.2.content.1")
		assert.True(t, marked, "attempt %d must carry the tool-result marker", i+1)
		assert.Equal(t, 4, countCacheBreakpoints(body), "attempt %d must stay within budget", i+1)
	}
	assert.Equal(t, bodies[0], bodies[1], "a retry sends the same body")
}

// The placement rule on serialized bodies, case by case.
func TestPlaceToolResultCacheBreakpoint(t *testing.T) {
	const turn = `{"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"task"}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"read","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"one"},` +
		`{"type":"tool_result","tool_use_id":"b","content":"two"}]},` +
		`{"role":"user","content":[{"type":"text","text":"[Turn 3/40]"}]}]}`
	// finalToolResult ends on its tool output.
	const finalToolResult = `{"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"task"}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"read","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"one"}]}]}`
	withTopLevel := func(body, cacheControl string) string {
		return `{"cache_control":` + cacheControl + `,` + body[1:]
	}

	tests := []struct {
		name string
		body string
		// wantMarker is the marker expected on messages.2's last block, or ""
		// when the body must come back unchanged.
		wantMarker string
	}{
		{name: "not JSON", body: "not json"},
		{name: "messages is not an array", body: `{"messages":"x"}`},
		{name: "single message", body: `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"ok"}]}]}`},
		{name: "no tool results", body: `{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"text","text":"hello"}]}]}`},
		{name: "marks the newest tool result", body: turn, wantMarker: defaultCacheControl},
		{
			name: "leaves a caller's marker alone",
			body: strings.Replace(turn, `"content":"two"}`, `"content":"two","cache_control":`+oneHourCacheControl+`}`, 1),
		},
		{
			name: "no slot left",
			body: withTopLevel(strings.NewReplacer(
				`"text":"task"}`, `"text":"task","cache_control":{"type":"ephemeral"}}`,
				`"input":{}}`, `"input":{},"cache_control":{"type":"ephemeral"}}`,
				`"text":"[Turn 3/40]"}`, `"text":"[Turn 3/40]","cache_control":{"type":"ephemeral"}}`,
			).Replace(turn), `{"type":"ephemeral"}`),
		},
		{
			name:       "top-level cache control lends its TTL",
			body:       withTopLevel(turn, oneHourCacheControl),
			wantMarker: oneHourCacheControl,
		},
		{
			name: "top-level cache control already covers a final tool result",
			body: withTopLevel(finalToolResult, `{"type":"ephemeral"}`),
		},
		{
			name:       "a null top-level cache control is not a marker",
			body:       withTopLevel(turn, "null"),
			wantMarker: defaultCacheControl,
		},
		{
			name:       "a final tool result is marked without automatic caching",
			body:       finalToolResult,
			wantMarker: defaultCacheControl,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := countCacheBreakpoints([]byte(tt.body))
			got := placeToolResultCacheBreakpoint([]byte(tt.body))
			if tt.wantMarker == "" {
				assert.Equal(t, tt.body, string(got), "the body must come back unchanged")
				return
			}
			marker, marked := cacheControlAt(got, lastBlock(got, 2))
			require.True(t, marked, "the newest tool result must be marked: %s", got)
			assert.JSONEq(t, tt.wantMarker, marker.Raw)
			assert.Equal(t, before+1, countCacheBreakpoints(got), "exactly one marker is added")
		})
	}
}

// The selection rule on serialized message lists.
func TestLastToolResultMessageIndex(t *testing.T) {
	toolResults := func(ids ...string) string {
		blocks := make([]string, 0, len(ids))
		for _, id := range ids {
			blocks = append(blocks, `{"type":"tool_result","tool_use_id":"`+id+`","content":"ok"}`)
		}
		return `{"role":"user","content":[` + strings.Join(blocks, ",") + `]}`
	}
	text := func(role, s string) string {
		return `{"role":"` + role + `","content":[{"type":"text","text":"` + s + `"}]}`
	}
	toolResultWithText := func(id, s string) string {
		return `{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"},` +
			`{"type":"text","text":"` + s + `"}]}`
	}
	list := func(messages ...string) []gjson.Result {
		return gjson.Parse(`[` + strings.Join(messages, ",") + `]`).Array()
	}

	tests := []struct {
		name     string
		messages []gjson.Result
		minIndex int
		expected int
	}{
		{name: "no messages", messages: list(), minIndex: -1, expected: -1},
		{name: "no tool results", messages: list(text("user", "hi"), text("assistant", "hello")), minIndex: -1, expected: -1},
		{
			// The block loop would vacuously agree that an empty message is
			// all tool results, and mark a breakpoint onto nothing.
			name:     "empty message is not a tool-result message",
			messages: list(text("user", "task"), text("assistant", "calling"), `{"role":"user","content":[]}`),
			minIndex: 1,
			expected: -1,
		},
		{
			name:     "string content is not a tool-result message",
			messages: list(text("user", "task"), text("assistant", "calling"), `{"role":"user","content":"plain"}`),
			minIndex: 1,
			expected: -1,
		},
		{
			name:     "tool results are the final message",
			messages: list(text("user", "task"), text("assistant", "calling"), toolResults("a", "b")),
			minIndex: 1,
			expected: 2,
		},
		{
			name:     "tool results followed by a trailing note",
			messages: list(text("user", "task"), text("assistant", "calling"), toolResults("a"), text("user", "[Turn 3/40]")),
			minIndex: 1,
			expected: 2,
		},
		{
			name:     "tool results at or before minIndex are skipped",
			messages: list(text("user", "task"), toolResults("a"), text("assistant", "done"), text("user", "[Turn 4/40]")),
			minIndex: 2,
			expected: -1,
		},
		{
			name: "newest of several tool-result messages wins",
			messages: list(text("user", "task"), text("assistant", "calling"), toolResults("a"),
				text("assistant", "calling again"), toolResults("b"), text("user", "[Turn 5/40]")),
			minIndex: 3,
			expected: 4,
		},
		{
			// Every block must be a tool result: that is the shape
			// convertMessages merges a turn's results into.
			name:     "trailing mixed-content message is not a tool-result message",
			messages: list(text("user", "task"), text("assistant", "calling"), toolResults("a"), toolResultWithText("b", "[Turn 3/40]")),
			minIndex: 1,
			expected: 2,
		},
		{
			// The same shape with nothing valid behind it must select nothing
			// rather than fall back to it.
			name:     "mixed-content message alone is never selected",
			messages: list(text("user", "task"), text("assistant", "calling"), toolResultWithText("a", "[Turn 3/40]")),
			minIndex: 1,
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, lastToolResultMessageIndex(tt.messages, tt.minIndex))
		})
	}
}

// The two index rules agree with their typed counterparts on the same request,
// so the serialized placement lands on the message the typed one would have.
func TestLastAssistantMessageIndexMatchesTheTypedRule(t *testing.T) {
	m := New("claude-3-5-sonnet")
	typed, err := m.buildChatRequest(&model.Request{Messages: toolResultTurnMessages(), Tools: readTool()})
	require.NoError(t, err)
	body, err := typed.MarshalJSON()
	require.NoError(t, err)

	serialized := gjson.GetBytes(body, "messages").Array()
	assert.Equal(t, m.findLastAssistantMessageIndex(typed.Messages), lastAssistantMessageIndex(serialized))
	assert.Equal(t, 1, lastAssistantMessageIndex(serialized))
	assert.Equal(t, 2, lastToolResultMessageIndex(serialized, 1))
}

// Every place a marker can sit is counted, and nothing else is.
func TestCountCacheBreakpoints(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "empty", body: `{}`, want: 0},
		{name: "top-level", body: `{"cache_control":{"type":"ephemeral"}}`, want: 1},
		{name: "top-level null", body: `{"cache_control":null}`, want: 0},
		{name: "system as a string", body: `{"system":"policy"}`, want: 0},
		{name: "system blocks", body: `{"system":[{"type":"text","text":"a"},{"type":"text","text":"b","cache_control":{"type":"ephemeral"}}]}`, want: 1},
		{name: "tools", body: `{"tools":[{"name":"a","cache_control":{"type":"ephemeral"}},{"name":"b"}]}`, want: 1},
		{name: "message blocks", body: `{"messages":[{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}},{"type":"text","text":"b"}]},{"role":"user","content":"plain"}]}`, want: 1},
		{
			name: "all of them",
			body: `{"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}],` +
				`"tools":[{"name":"a","cache_control":{"type":"ephemeral"}}],` +
				`"messages":[{"role":"user","content":[{"type":"text","text":"a","cache_control":{"type":"ephemeral"}}]}]}`,
			want: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countCacheBreakpoints([]byte(tt.body)))
		})
	}
}
