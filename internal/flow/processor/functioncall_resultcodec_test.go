//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
)

type codecTestResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// failingCodec always returns an error, to exercise encoding failure handling.
type failingCodec struct{ err error }

func (f failingCodec) Encode(context.Context, any) (string, error) {
	return "", f.err
}

// panickingCodec panics, to exercise the flow's encode panic protection.
type panickingCodec struct{}

func (panickingCodec) Encode(context.Context, any) (string, error) {
	panic("codec boom")
}

func runCodecToolCall(
	t *testing.T,
	p *FunctionCallResponseProcessor,
	name string,
	tl tool.Tool,
) []model.Choice {
	t.Helper()
	tools := map[string]tool.Tool{name: tl}
	return runCodecToolCallWithTools(t, p, name, tools)
}

func runCodecToolCallWithTools(
	t *testing.T,
	p *FunctionCallResponseProcessor,
	name string,
	tools map[string]tool.Tool,
) []model.Choice {
	t.Helper()
	ctx := context.Background()
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID: "c1",
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: []byte(`{}`),
		},
	}
	ch := make(chan *event.Event, 64)
	_, choices, _, _, _, err := p.executeToolCall(ctx, inv, pc, tools, 0, ch)
	require.NoError(t, err)
	return choices
}

func TestExecuteToolCall_NoCodec_DefaultJSON(t *testing.T) {
	p := NewFunctionCallResponseProcessor(false, nil)
	res := codecTestResult{ExitCode: 0, Output: "done"}
	ft := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (codecTestResult, error) {
			return res, nil
		},
		function.WithName("bash"),
	)
	choices := runCodecToolCall(t, p, "bash", ft)
	require.Len(t, choices, 1)
	// Without a codec, the model-visible content must remain byte-identical to
	// the legacy json.Marshal output for inputs without <, >, &.
	want, err := json.Marshal(res)
	require.NoError(t, err)
	assert.Equal(t, string(want), choices[0].Message.Content)
	assert.Equal(t, model.RoleTool, choices[0].Message.Role)
	assert.Equal(t, "bash", choices[0].Message.ToolName)
	assert.Equal(t, "c1", choices[0].Message.ToolID)
}

func TestExecuteToolCall_XMLCodec(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	res := codecTestResult{ExitCode: 0, Output: "<ok>"}
	ft := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (codecTestResult, error) {
			return res, nil
		},
		function.WithName("bash"),
		function.WithResultCodec(resultcodec.XML()),
	)
	choices := runCodecToolCall(t, p, "bash", ft)
	require.Len(t, choices, 1)
	want, err := resultcodec.XML().Encode(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, want, choices[0].Message.Content)
	assert.Equal(t, model.RoleTool, choices[0].Message.Role)
	assert.Equal(t, "bash", choices[0].Message.ToolName)
	assert.Equal(t, "c1", choices[0].Message.ToolID)
}

func TestExecuteToolCall_MultipleToolsDifferentCodecs(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	res := codecTestResult{ExitCode: 2, Output: "x"}

	jsonTool := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (codecTestResult, error) { return res, nil },
		function.WithName("j"), function.WithResultCodec(resultcodec.JSON()),
	)
	xmlTool := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (codecTestResult, error) { return res, nil },
		function.WithName("x"), function.WithResultCodec(resultcodec.XML()),
	)
	textTool := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (string, error) { return "plain <text>", nil },
		function.WithName("t"), function.WithResultCodec(resultcodec.Text()),
	)
	customTool := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (codecTestResult, error) { return res, nil },
		function.WithName("c"),
		function.WithResultCodec(resultcodec.Custom(
			func(_ context.Context, r codecTestResult) (string, error) {
				return fmt.Sprintf("exit=%d output=%s", r.ExitCode, r.Output), nil
			},
		)),
	)
	tools := map[string]tool.Tool{
		"j": jsonTool, "x": xmlTool, "t": textTool, "c": customTool,
	}

	wantJSON, err := resultcodec.JSON().Encode(ctx, res)
	require.NoError(t, err)
	wantXML, err := resultcodec.XML().Encode(ctx, res)
	require.NoError(t, err)

	jChoices := runCodecToolCallWithTools(t, p, "j", tools)
	require.Len(t, jChoices, 1)
	assert.Equal(t, wantJSON, jChoices[0].Message.Content)

	xChoices := runCodecToolCallWithTools(t, p, "x", tools)
	require.Len(t, xChoices, 1)
	assert.Equal(t, wantXML, xChoices[0].Message.Content)

	tChoices := runCodecToolCallWithTools(t, p, "t", tools)
	require.Len(t, tChoices, 1)
	assert.Equal(t, "plain <text>", tChoices[0].Message.Content)

	cChoices := runCodecToolCallWithTools(t, p, "c", tools)
	require.Len(t, cChoices, 1)
	assert.Equal(t, "exit=2 output=x", cChoices[0].Message.Content)
}

func TestExecuteToolCall_AfterToolReplacement_CodecEncodesReplacement(t *testing.T) {
	ctx := context.Background()
	replacement := codecTestResult{ExitCode: 9, Output: "replaced"}
	callbacks := tool.NewCallbacks()
	callbacks.RegisterAfterTool(func(
		_ context.Context,
		_ *tool.AfterToolArgs,
	) (*tool.AfterToolResult, error) {
		return &tool.AfterToolResult{CustomResult: replacement}, nil
	})
	p := NewFunctionCallResponseProcessor(false, callbacks)
	ft := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (codecTestResult, error) {
			return codecTestResult{ExitCode: 0, Output: "original"}, nil
		},
		function.WithName("bash"),
		function.WithResultCodec(resultcodec.XML()),
	)
	choices := runCodecToolCall(t, p, "bash", ft)
	require.Len(t, choices, 1)
	want, err := resultcodec.XML().Encode(ctx, replacement)
	require.NoError(t, err)
	assert.Equal(t, want, choices[0].Message.Content)
}

func TestExecuteToolCall_CodecFailure_NoJSONFallback(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	base := &mockCallableTool{
		declaration: &tool.Declaration{Name: "f"},
		callFn: func(_ context.Context, _ []byte) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}
	wrapped := resultcodec.Wrap(base, failingCodec{err: errors.New("boom")})
	tools := map[string]tool.Tool{"f": wrapped}
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "f", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, ignorable, _, err := p.executeToolCall(ctx, inv, pc, tools, 0, ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrorEncodeResult)
	assert.True(t, ignorable)
	// No silent fallback to JSON: a failed encode does not produce a choice.
	assert.Nil(t, choices)
}

func TestExecuteToolCall_CodecPanic_BecomesObservableError(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	base := &mockCallableTool{
		declaration: &tool.Declaration{Name: "p"},
		callFn: func(_ context.Context, _ []byte) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}
	wrapped := resultcodec.Wrap(base, panickingCodec{})
	tools := map[string]tool.Tool{"p": wrapped}
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "p", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, ignorable, _, err := p.executeToolCall(ctx, inv, pc, tools, 0, ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrorEncodeResult)
	assert.Contains(t, err.Error(), "panic")
	assert.True(t, ignorable)
	assert.Nil(t, choices)
}

func TestExecuteToolCall_StreamableFinalResultCodec(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	res := codecTestResult{ExitCode: 0, Output: "<ok>"}
	sfn := func(_ context.Context, _ struct{}) (*tool.StreamReader, error) {
		s := tool.NewStream(2)
		go func() {
			defer s.Writer.Close()
			_ = s.Writer.Send(tool.StreamChunk{Content: "partial-text"}, nil)
			_ = s.Writer.Send(
				tool.StreamChunk{Content: tool.FinalResultChunk{Result: res}},
				nil,
			)
		}()
		return s.Reader, nil
	}
	st := function.NewStreamableFunctionTool[struct{}, codecTestResult](
		sfn,
		function.WithName("st"),
		function.WithResultCodec(resultcodec.XML()),
	)
	tools := map[string]tool.Tool{"st": st}
	inv := &agent.Invocation{InvocationID: "inv", AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "st", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 64)
	_, choices, _, _, _, err := p.executeToolCall(ctx, inv, pc, tools, 0, ch)
	require.NoError(t, err)
	require.Len(t, choices, 1)

	// Final result is encoded with the codec.
	want, err := resultcodec.XML().Encode(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, want, choices[0].Message.Content)

	// Intermediate stream events keep their existing (unencoded) behavior.
	close(ch)
	var sawPartial bool
	for e := range ch {
		if e == nil || !e.IsPartial || e.Response == nil {
			continue
		}
		for _, c := range e.Choices {
			if c.Delta.Content == "partial-text" {
				sawPartial = true
			}
		}
	}
	assert.True(t, sawPartial, "intermediate stream chunk should be unaffected by codec")
}

func TestExecuteToolCall_ToolResultMessagesOverridesCodecDefault(t *testing.T) {
	ctx := context.Background()
	res := codecTestResult{ExitCode: 0, Output: "<ok>"}
	var defaultContent string
	callbacks := tool.NewCallbacks()
	callbacks.RegisterToolResultMessages(func(
		_ context.Context,
		in *tool.ToolResultMessagesInput,
	) (any, error) {
		if dm, ok := in.DefaultToolMessage.(model.Message); ok {
			defaultContent = dm.Content
		}
		return model.Message{
			Role:    model.RoleTool,
			ToolID:  in.ToolCallID,
			Content: "OVERRIDDEN",
		}, nil
	})
	p := NewFunctionCallResponseProcessor(false, callbacks)
	ft := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (codecTestResult, error) { return res, nil },
		function.WithName("bash"),
		function.WithResultCodec(resultcodec.XML()),
	)
	choices := runCodecToolCall(t, p, "bash", ft)
	require.Len(t, choices, 1)
	// Override semantics unchanged: the callback replaces the message.
	assert.Equal(t, "OVERRIDDEN", choices[0].Message.Content)
	// The DefaultToolMessage handed to the callback was produced by the codec.
	want, err := resultcodec.XML().Encode(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, want, defaultContent)
}

// denyingCodecTool denies permission and would return a codecTestResult if it
// ran, so we can assert the codec is not applied to the permission result.
type denyingCodecTool struct {
	declaration *tool.Declaration
}

func (d *denyingCodecTool) Declaration() *tool.Declaration { return d.declaration }
func (d *denyingCodecTool) Call(_ context.Context, _ []byte) (any, error) {
	return codecTestResult{ExitCode: 0, Output: "ran"}, nil
}
func (d *denyingCodecTool) CheckPermission(
	_ context.Context,
	_ *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return tool.DenyPermission("not allowed"), nil
}

func TestExecuteToolCall_PermissionResult_BypassesCodec(t *testing.T) {
	p := NewFunctionCallResponseProcessor(false, nil)
	base := &denyingCodecTool{declaration: &tool.Declaration{Name: "danger"}}
	// Typed codec that would error if handed a tool.PermissionResult.
	codec := resultcodec.Custom(
		func(_ context.Context, r codecTestResult) (string, error) {
			return "CODEC:" + r.Output, nil
		},
	)
	wrapped := resultcodec.Wrap(base, codec)
	tools := map[string]tool.Tool{"danger": wrapped}
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "danger", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, _, _, err := p.executeToolCall(context.Background(), inv, pc, tools, 0, ch)

	// Permission denial is not an error and must not be masked by an encode error.
	require.NoError(t, err)
	require.Len(t, choices, 1)
	content := choices[0].Message.Content
	require.NotContains(t, content, "CODEC:", "codec must not run on permission results")

	// The message is the default-encoded permission result.
	var pr tool.PermissionResult
	require.NoError(t, json.Unmarshal([]byte(content), &pr))
	assert.Equal(t, tool.PermissionResultStatusDenied, pr.Status)
	assert.Equal(t, "danger", pr.Tool)
}

// recordingStateDeltaTool records the content bytes it receives for state
// deltas, so a test can assert the codec does not leak into the state-delta path.
type recordingStateDeltaTool struct {
	declaration *tool.Declaration
	result      any
	gotContent  []byte
}

func (r *recordingStateDeltaTool) Declaration() *tool.Declaration { return r.declaration }
func (r *recordingStateDeltaTool) Call(_ context.Context, _ []byte) (any, error) {
	return r.result, nil
}
func (r *recordingStateDeltaTool) StateDelta(_ string, _ []byte, content []byte) map[string][]byte {
	r.gotContent = append([]byte(nil), content...)
	return map[string][]byte{"k": []byte("v")}
}

func TestExecuteToolCall_StateDeltaUsesJSONNotCodec(t *testing.T) {
	ctx := context.Background()
	res := codecTestResult{ExitCode: 0, Output: "<ok>"}
	base := &recordingStateDeltaTool{
		declaration: &tool.Declaration{Name: "stateful"},
		result:      res,
	}
	// Bind an XML codec; the state-delta path must still receive JSON.
	wrapped := resultcodec.Wrap(base, resultcodec.XML())
	tools := map[string]tool.Tool{"stateful": wrapped}
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{
		AgentName: "a",
		Model:     &mockModel{},
		Session:   &session.Session{},
	}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "stateful", Arguments: []byte(`{}`)},
	}
	ev, err := p.executeSingleToolCallSequential(
		ctx, inv, &model.Response{}, tools, make(chan *event.Event, 8), 0, pc,
	)
	require.NoError(t, err)
	require.NotNil(t, ev)
	require.NotEmptyf(t, ev.Choices, "expected a tool result choice")

	// The model-visible message is XML.
	wantXML, err := resultcodec.XML().Encode(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, wantXML, ev.Choices[0].Message.Content)

	// But the stateful tool received JSON, not the codec output.
	wantJSON, err := resultcodec.JSON().Encode(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, wantJSON, string(base.gotContent))
	assert.NotContains(t, string(base.gotContent), "<result>")
}

func TestExecuteToolCall_WrapBindsCodec(t *testing.T) {
	ctx := context.Background()
	p := NewFunctionCallResponseProcessor(false, nil)
	res := codecTestResult{ExitCode: 3, Output: "wrapped"}
	base := &mockCallableTool{
		declaration: &tool.Declaration{Name: "w"},
		callFn: func(_ context.Context, _ []byte) (any, error) {
			return res, nil
		},
	}
	wrapped := resultcodec.Wrap(base, resultcodec.XML())
	choices := runCodecToolCall(t, p, "w", wrapped)
	require.Len(t, choices, 1)
	want, err := resultcodec.XML().Encode(ctx, res)
	require.NoError(t, err)
	assert.Equal(t, want, choices[0].Message.Content)
}
