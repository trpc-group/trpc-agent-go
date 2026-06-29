//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolcode

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	bridge "trpc.group/trpc-go/trpc-agent-go/codeexecutor/codeact"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type addTool struct{}

func (addTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "add", InputSchema: &tool.Schema{Type: "object", Required: []string{"a", "b"}, Properties: map[string]*tool.Schema{"a": {Type: "integer"}, "b": {Type: "integer"}}}}
}
func (addTool) Call(_ context.Context, raw []byte) (any, error) {
	var v struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v.A + v.B, nil
}

func TestToolRunsGuestThroughGateway(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	toolValue, err := NewTool(bridge.LocalRunner{}, []tool.CallableTool{addTool{}})
	require.NoError(t, err)
	require.Equal(t, "execute_tool_code", toolValue.Declaration().Name)
	require.Contains(t, toolValue.Declaration().Description, "add")
	require.Contains(t, toolValue.Declaration().Description, "Prefer one execute_tool_code call")
	require.Contains(t, toolValue.Declaration().Description, "Calls execute sequentially")
	require.Contains(t, toolValue.Declaration().Description, "compact JSON-compatible value")
	require.Contains(t, toolValue.Declaration().Description, "Python RuntimeError")
	require.Contains(t, toolValue.Declaration().Description, "direct HTTP clients")
	require.Contains(t, toolValue.Declaration().Description, "Host capabilities available inside Python")
	require.Contains(t, toolValue.Declaration().Description, "Input JSON Schema")
	require.Contains(t, toolValue.Declaration().Description, "\"a\"")
	result, err := toolValue.Call(context.Background(), []byte(`{"code":"return await call_tool('add', a=1, b=41)"}`))
	require.NoError(t, err)
	got := result.(bridge.Result)
	require.JSONEq(t, "42", string(got.Value))
}

func TestNewToolOptionsAndValidation(t *testing.T) {
	toolValue, err := NewTool(nil, nil, WithName("orchestrate"), WithDescription("custom description"))
	require.NoError(t, err)
	decl := toolValue.Declaration()
	require.Equal(t, "orchestrate", decl.Name)
	require.Equal(t, "custom description", decl.Description)

	_, err = NewTool(nil, nil, WithName(" \t"))
	require.ErrorContains(t, err, "tool name is required")
	_, err = NewTool(nil, []tool.CallableTool{nil})
	require.ErrorContains(t, err, "tool declaration is required")
}

func TestToolCallValidationAndRuntime(t *testing.T) {
	runtime := &recordingRuntime{result: bridge.Result{Value: json.RawMessage(`{"ok":true}`)}}
	toolValue, err := NewTool(runtime, nil)
	require.NoError(t, err)

	_, err = toolValue.Call(context.Background(), []byte(`not-json`))
	require.Error(t, err)
	_, err = toolValue.Call(context.Background(), []byte(`{"code":""}`))
	require.ErrorContains(t, err, "code is required")

	result, err := toolValue.Call(context.Background(), []byte(`{"code":"return {'ok': True}"}`))
	require.NoError(t, err)
	require.Equal(t, "return {'ok': True}", runtime.request.Code)
	require.Equal(t, "python", runtime.request.Language)
	require.Equal(t, runtime.result, result)

	nilRuntimeTool, err := NewTool(nil, nil)
	require.NoError(t, err)
	_, err = nilRuntimeTool.Call(context.Background(), []byte(`{"code":"return 1"}`))
	require.ErrorContains(t, err, "runtime is required")
}

func TestBuildToolHelpSortsSchemasAndSkipsInvalidDeclarations(t *testing.T) {
	help := buildToolHelp([]tool.CallableTool{
		nil,
		declaredTool{},
		declaredTool{decl: &tool.Declaration{Name: "zeta", Description: "zeta description", InputSchema: &tool.Schema{Type: "object"}}},
		declaredTool{decl: &tool.Declaration{Name: "alpha", Description: "alpha description", OutputSchema: &tool.Schema{Type: "string"}}},
	})
	require.Less(t, strings.Index(help, "alpha"), strings.Index(help, "zeta"))
	require.Contains(t, help, "Input JSON Schema")
	require.Contains(t, help, "Output JSON Schema")
}

type recordingRuntime struct {
	request bridge.Request
	result  bridge.Result
}

func (r *recordingRuntime) ExecuteCodeAct(_ context.Context, req bridge.Request, _ bridge.ToolCallHandler) (bridge.Result, error) {
	r.request = req
	return r.result, nil
}

type declaredTool struct{ decl *tool.Declaration }

func (t declaredTool) Declaration() *tool.Declaration { return t.decl }
func (declaredTool) Call(context.Context, []byte) (any, error) {
	return nil, nil
}
