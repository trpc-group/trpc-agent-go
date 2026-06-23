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
