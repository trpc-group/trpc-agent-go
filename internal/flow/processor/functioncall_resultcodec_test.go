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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
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
	calls       int
}

func (r *recordingStateDeltaTool) Declaration() *tool.Declaration { return r.declaration }
func (r *recordingStateDeltaTool) Call(_ context.Context, _ []byte) (any, error) {
	r.calls++
	return r.result, nil
}
func (r *recordingStateDeltaTool) StateDelta(_ string, _ []byte, content []byte) map[string][]byte {
	r.gotContent = append([]byte(nil), content...)
	return map[string][]byte{"k": []byte("v")}
}

// transparentWrapper exposes TransparentUnwrap()/Call() only, used to build deep
// transparent chains.
type transparentWrapper struct {
	inner tool.Tool
}

func (w *transparentWrapper) Declaration() *tool.Declaration { return w.inner.Declaration() }
func (w *transparentWrapper) TransparentUnwrap() tool.Tool   { return w.inner }
func (w *transparentWrapper) Call(ctx context.Context, args []byte) (any, error) {
	return w.inner.(tool.CallableTool).Call(ctx, args)
}

// cyclicCallableWrapper's TransparentUnwrap returns itself, forming a cycle; Call
// delegates to the base so an incorrect fail-open would execute it.
type cyclicCallableWrapper struct {
	base tool.Tool
}

func (w *cyclicCallableWrapper) Declaration() *tool.Declaration { return w.base.Declaration() }
func (w *cyclicCallableWrapper) TransparentUnwrap() tool.Tool   { return w }
func (w *cyclicCallableWrapper) Call(ctx context.Context, args []byte) (any, error) {
	return w.base.(tool.CallableTool).Call(ctx, args)
}

// nonComparableCyclicTool is a slice-backed Tool whose TransparentUnwrap
// returns itself. Comparing two interface values holding it would panic.
type nonComparableCyclicTool []int

func (t nonComparableCyclicTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "non_comparable_cycle"}
}

func (t nonComparableCyclicTool) TransparentUnwrap() tool.Tool { return t }

func TestExecuteToolCall_DeepWrapperChainDenyNotBypassed(t *testing.T) {
	// A deny hidden past the traversal depth bound must fail closed, not allow.
	base := &recordingCallableTool{declaration: &tool.Declaration{Name: "danger"}}
	var inner tool.Tool = &permissionWrapper{base: base}
	for i := 0; i < 130; i++ {
		inner = &transparentWrapper{inner: inner}
	}
	wrapped := resultcodec.Wrap(inner, resultcodec.Text())
	tools := map[string]tool.Tool{"danger": wrapped}
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "danger", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, _, _, err := p.executeToolCall(context.Background(), inv, pc, tools, 0, ch)
	require.NoError(t, err)
	require.Len(t, choices, 1)
	assert.False(t, base.called, "base tool must not run when a deny is hidden past the bound")
	var pr tool.PermissionResult
	require.NoError(t, json.Unmarshal([]byte(choices[0].Message.Content), &pr))
	assert.Equal(t, tool.PermissionResultStatusDenied, pr.Status)
}

func TestExecuteToolCall_NamedToolDoesNotHideDeepDeny(t *testing.T) {
	// codecTool -> NamedTool -> transparent wrapper -> deny -> base.
	// NamedTool must not report allow by only checking its direct original; the
	// deeper deny must be honored and the base tool must not execute.
	base := &recordingCallableTool{declaration: &tool.Declaration{Name: "danger"}}
	deny := &permissionWrapper{base: base}
	transparent := &transparentWrapper{inner: deny}
	named := itool.NewUnprefixedNamedTool(transparent)
	wrapped := resultcodec.Wrap(named, resultcodec.Text())
	tools := map[string]tool.Tool{"danger": wrapped}
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "danger", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, _, _, err := p.executeToolCall(context.Background(), inv, pc, tools, 0, ch)
	require.NoError(t, err)
	require.Len(t, choices, 1)
	assert.False(t, base.called, "base tool must not run when a deny is hidden behind a NamedTool")
	var pr tool.PermissionResult
	require.NoError(t, json.Unmarshal([]byte(choices[0].Message.Content), &pr))
	assert.Equal(t, tool.PermissionResultStatusDenied, pr.Status)
}

func TestExecuteToolCall_CyclicWrapperFailsClosed(t *testing.T) {
	// A cyclic transparent chain must not hang and must fail closed (no execution).
	base := &recordingCallableTool{declaration: &tool.Declaration{Name: "danger"}}
	wrapped := resultcodec.Wrap(&cyclicCallableWrapper{base: base}, resultcodec.Text())
	tools := map[string]tool.Tool{"danger": wrapped}
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "danger", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	var (
		choices []model.Choice
		err     error
	)
	// Bound the call so a regression in cycle protection fails fast instead of
	// hanging go test until its global timeout.
	runWithinTimeout(t, 5*time.Second, func() {
		_, choices, _, _, _, err = p.executeToolCall(context.Background(), inv, pc, tools, 0, ch)
	})
	require.NoError(t, err)
	require.Len(t, choices, 1)
	assert.False(t, base.called, "base tool must not run for a cyclic wrapper chain")
	var pr tool.PermissionResult
	require.NoError(t, json.Unmarshal([]byte(choices[0].Message.Content), &pr))
	assert.Equal(t, tool.PermissionResultStatusDenied, pr.Status)
}

// runWithinTimeout runs fn and fails the test if it does not return within d, so
// a regression in cycle protection fails fast instead of hanging go test.
func runWithinTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("tool call did not terminate; cycle protection may have regressed")
	}
}

func TestExecuteToolCall_StateDeltaMarshalPanicSurfacesError(t *testing.T) {
	// Stateful tool + a result whose MarshalJSON panics: the call must surface an
	// error rather than succeed with the codec message while silently dropping
	// the state delta. The invocation must not crash, and the tool runs once.
	base := &recordingStateDeltaTool{
		declaration: &tool.Declaration{Name: "stateful"},
		result:      panicMarshalResult{},
	}
	codec := resultcodec.Custom(func(_ context.Context, _ panicMarshalResult) (string, error) {
		return "custom-out", nil
	})
	wrapped := resultcodec.Wrap(base, codec)
	tools := map[string]tool.Tool{"stateful": wrapped}
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "stateful", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, ignorable, _, err := p.executeToolCall(context.Background(), inv, pc, tools, 0, ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrorMarshalResult)
	assert.True(t, ignorable)
	assert.Nil(t, choices, "must not hide the dropped state delta behind a success message")
	assert.Equal(t, 1, base.calls, "tool must execute exactly once, not be rerun")
	assert.Nil(t, base.gotContent, "state delta must never be fed codec text")
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

// recordingCallableTool records whether Call executed.
type recordingCallableTool struct {
	declaration *tool.Declaration
	called      bool
}

func (r *recordingCallableTool) Declaration() *tool.Declaration { return r.declaration }
func (r *recordingCallableTool) Call(_ context.Context, _ []byte) (any, error) {
	r.called = true
	return "base-result", nil
}

// permissionWrapper is a transparent wrapper that denies permission and exposes
// TransparentUnwrap()/Call(). It mirrors a third-party wrapper resultcodec.Wrap
// wraps.
type permissionWrapper struct {
	base tool.Tool
}

func (w *permissionWrapper) Declaration() *tool.Declaration { return w.base.Declaration() }
func (w *permissionWrapper) TransparentUnwrap() tool.Tool   { return w.base }
func (w *permissionWrapper) Call(ctx context.Context, args []byte) (any, error) {
	return w.base.(tool.CallableTool).Call(ctx, args)
}
func (w *permissionWrapper) CheckPermission(
	context.Context,
	*tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return tool.DenyPermission("blocked"), nil
}

func TestExecuteToolCall_PermissionWrapperNotBypassedByCodecWrap(t *testing.T) {
	// codecTool -> permissionWrapper (deny) -> base. Unwrapping past the
	// permission wrapper must not bypass its deny.
	base := &recordingCallableTool{declaration: &tool.Declaration{Name: "danger"}}
	wrapped := resultcodec.Wrap(&permissionWrapper{base: base}, resultcodec.Text())
	tools := map[string]tool.Tool{"danger": wrapped}
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "danger", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, _, _, err := p.executeToolCall(context.Background(), inv, pc, tools, 0, ch)
	require.NoError(t, err)
	require.Len(t, choices, 1)

	// The base tool must not execute when the wrapper denies.
	assert.False(t, base.called, "base tool must not run when a wrapper denies permission")
	var pr tool.PermissionResult
	require.NoError(t, json.Unmarshal([]byte(choices[0].Message.Content), &pr))
	assert.Equal(t, tool.PermissionResultStatusDenied, pr.Status)
}

// panicMarshalResult panics if JSON-marshaled, standing in for a result a Custom
// codec is meant to handle without JSON.
type panicMarshalResult struct{}

func (panicMarshalResult) MarshalJSON() ([]byte, error) { panic("marshal boom") }

func TestExecuteToolCall_CodecSkipsStateMarshalForNonStatefulTool(t *testing.T) {
	// For a tool without a state delta, configuring a codec must not trigger an
	// extra JSON marshal of the result (which here would panic).
	p := NewFunctionCallResponseProcessor(false, nil)
	codec := resultcodec.Custom(func(_ context.Context, _ panicMarshalResult) (string, error) {
		return "ok-custom", nil
	})
	ft := function.NewFunctionTool(
		func(_ context.Context, _ struct{}) (panicMarshalResult, error) {
			return panicMarshalResult{}, nil
		},
		function.WithName("p"),
		function.WithResultCodec(codec),
	)
	choices := runCodecToolCall(t, p, "p", ft)
	require.Len(t, choices, 1)
	assert.Equal(t, "ok-custom", choices[0].Message.Content)
}

// jsonFailResult fails (does not panic) JSON marshaling because of the channel.
type jsonFailResult struct {
	Ch chan int `json:"ch"`
}

func TestExecuteToolCall_StateDeltaMarshalErrorSurfaces(t *testing.T) {
	// When the state-delta JSON serialization fails (a channel is not
	// serializable), the call must fail rather than succeed with the codec
	// message while dropping the state delta.
	base := &recordingStateDeltaTool{
		declaration: &tool.Declaration{Name: "stateful"},
		result:      jsonFailResult{Ch: make(chan int)},
	}
	codec := resultcodec.Custom(func(_ context.Context, _ jsonFailResult) (string, error) {
		return "custom-out", nil
	})
	wrapped := resultcodec.Wrap(base, codec)
	tools := map[string]tool.Tool{"stateful": wrapped}
	p := NewFunctionCallResponseProcessor(false, nil)
	inv := &agent.Invocation{AgentName: "a", Model: &mockModel{}}
	pc := model.ToolCall{
		ID:       "c1",
		Function: model.FunctionDefinitionParam{Name: "stateful", Arguments: []byte(`{}`)},
	}
	ch := make(chan *event.Event, 8)
	_, choices, _, ignorable, _, err := p.executeToolCall(context.Background(), inv, pc, tools, 0, ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrorMarshalResult)
	assert.True(t, ignorable)
	assert.Nil(t, choices)
	assert.Equal(t, 1, base.calls, "tool must execute exactly once")
	assert.Nil(t, base.gotContent, "state delta must never be fed codec text")
}

// pollutingTool marks the session as auto-memory polluted, like a knowledge tool.
type pollutingTool struct {
	declaration *tool.Declaration
}

func (p *pollutingTool) Declaration() *tool.Declaration            { return p.declaration }
func (p *pollutingTool) Call(context.Context, []byte) (any, error) { return "ok", nil }
func (p *pollutingTool) PollutesAutoMemory() bool                  { return true }

func TestToolCapabilityPollutesAutoMemory_ThroughWrap(t *testing.T) {
	// resultcodec.Wrap must not hide the PollutesAutoMemory capability, even when
	// the tool uses a custom name that the name-based fallback does not match.
	base := &pollutingTool{declaration: &tool.Declaration{Name: "custom_search"}}
	wrapped := resultcodec.Wrap(base, resultcodec.XML())

	if toolNamePollutesAutoMemory("custom_search") {
		t.Fatal("precondition: a custom name should not match the name fallback")
	}
	if !toolCapabilityPollutesAutoMemory(wrapped) {
		t.Fatal("resultcodec.Wrap must preserve the PollutesAutoMemory capability")
	}
}

func TestToolCapabilityPollutesAutoMemory_ShortChainNotPolluting(t *testing.T) {
	// A fully traversable chain with no pollution source must not be marked
	// polluting; fail-closed must only trigger on cycles/exhaustion.
	base := &recordingCallableTool{declaration: &tool.Declaration{Name: "plain"}}
	chain := &transparentWrapper{inner: &transparentWrapper{inner: base}}
	if toolCapabilityPollutesAutoMemory(chain) {
		t.Fatal("a finite non-polluting chain must not be treated as polluting")
	}
}

func TestToolCapabilityPollutesAutoMemory_CyclicFailsClosed(t *testing.T) {
	// A self-cyclic transparent chain cannot be fully traversed, so a hidden
	// pollution source cannot be ruled out: fail closed (treat as polluting).
	base := &recordingCallableTool{declaration: &tool.Declaration{Name: "x"}}
	cyclic := &cyclicCallableWrapper{base: base}
	var got bool
	runWithinTimeout(t, 5*time.Second, func() {
		got = toolCapabilityPollutesAutoMemory(cyclic)
	})
	if !got {
		t.Fatal("cyclic chain must fail closed (treated as polluting)")
	}
}

func TestToolCapabilityPollutesAutoMemory_NonComparableCycleFailsClosed(t *testing.T) {
	// Wrapper implementations are not required to have comparable dynamic
	// types. Cycle protection must rely on the depth bound rather than comparing
	// interface values, which panics for slice-backed tools.
	cyclic := nonComparableCyclicTool{1}
	var got bool
	require.NotPanics(t, func() {
		got = toolCapabilityPollutesAutoMemory(cyclic)
	})
	if !got {
		t.Fatal("cyclic chain must fail closed (treated as polluting)")
	}
}

func TestToolCapabilityPollutesAutoMemory_DeepChainFailsClosed(t *testing.T) {
	// A chain deeper than the traversal bound cannot be fully inspected, so a
	// pollution source hidden past the bound cannot be ruled out: fail closed.
	var tl tool.Tool = &recordingCallableTool{declaration: &tool.Declaration{Name: "x"}}
	for i := 0; i < maxToolWrapperTraversalDepth+2; i++ {
		tl = &transparentWrapper{inner: tl}
	}
	if !toolCapabilityPollutesAutoMemory(tl) {
		t.Fatal("a chain deeper than the bound must fail closed (treated as polluting)")
	}
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
