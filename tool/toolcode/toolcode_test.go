package toolcode

import (
	"context"
	"encoding/json"
	"os/exec"
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
	require.Contains(t, toolValue.Declaration().Description, "direct HTTP clients")
	require.Contains(t, toolValue.Declaration().Description, "Host capabilities available inside Python")
	require.Contains(t, toolValue.Declaration().Description, "Input JSON Schema")
	require.Contains(t, toolValue.Declaration().Description, "\"a\"")
	result, err := toolValue.Call(context.Background(), []byte(`{"code":"return await call_tool('add', a=1, b=41)"}`))
	require.NoError(t, err)
	got := result.(bridge.Result)
	require.JSONEq(t, "42", string(got.Value))
}
