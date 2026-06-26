//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeact

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRuntimeAbstractionDoesNotRequireStdio(t *testing.T) {
	add := testTool{
		declaration: &tool.Declaration{
			Name: "add",
			InputSchema: &tool.Schema{
				Type:                 "object",
				Required:             []string{"a", "b"},
				AdditionalProperties: false,
				Properties: map[string]*tool.Schema{
					"a": {Type: "integer"},
					"b": {Type: "integer"},
				},
			},
		},
		call: func(raw []byte) (any, error) {
			var in struct{ A, B int }
			require.NoError(t, json.Unmarshal(raw, &in))
			return map[string]int{"sum": in.A + in.B}, nil
		},
	}
	gateway, err := NewGateway(add)
	require.NoError(t, err)

	result, err := Execute(context.Background(), fakeRemoteRuntime{}, gateway, "ignored by fake")
	require.NoError(t, err)
	require.JSONEq(t, `{"sum":3}`, string(result.Value))
}

func TestExecuteValidatesRequiredInputs(t *testing.T) {
	tests := []struct {
		name    string
		runtime Runtime
		handler ToolCallHandler
		code    string
		want    string
	}{
		{name: "runtime", handler: fakeToolCallHandler{}, code: "return 1", want: "runtime is required"},
		{name: "handler", runtime: fakeRemoteRuntime{}, code: "return 1", want: "tool call handler is required"},
		{name: "code", runtime: fakeRemoteRuntime{}, handler: fakeToolCallHandler{}, want: "code is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Execute(context.Background(), tt.runtime, tt.handler, tt.code)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

type fakeRemoteRuntime struct{}

func (fakeRemoteRuntime) ExecuteCodeAct(ctx context.Context, _ Request, handler ToolCallHandler) (Result, error) {
	value, err := handler.HandleToolCall(ctx, ToolCall{
		ID:   "remote-call-1",
		Name: "add",
		Args: json.RawMessage(`{"a":1,"b":2}`),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Value: value}, nil
}

var _ Runtime = fakeRemoteRuntime{}
