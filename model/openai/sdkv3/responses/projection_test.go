//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestProjectProviderDataPreservesNativeRecord(t *testing.T) {
	adapter := New("model", WithAPIKey("test"))
	const raw = `[
		{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"thinking"}],"encrypted_content":"opaque"},
		{"type":"message","id":"msg_1","status":"completed","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"edited","annotations":[]}]},
		{"type":"function_call","call_id":"call_1","name":"calculator","arguments":"{\"a\":9}","extension":{"key":1}}
	]`
	data := &model.ProviderData{Protocol: responseProvider, Model: "model", Payload: json.RawMessage(raw)}
	message, err := adapter.ProjectProviderData(data)
	require.NoError(t, err)
	require.Equal(t, "edited", message.Content)
	require.Equal(t, "thinking", message.ReasoningContent)
	require.Equal(t, "call_1", message.ToolCalls[0].ID)
	require.JSONEq(t, `{"a":9}`, string(message.ToolCalls[0].Function.Arguments))
	require.Equal(t, raw, string(data.Payload))
	require.Same(t, data, message.ProviderData)

	data.Payload = json.RawMessage(`[]`)
	message, err = adapter.ProjectProviderData(data)
	require.NoError(t, err)
	require.Empty(t, message.ToolCalls)
	require.Empty(t, message.Content)
	_, err = adapter.ProjectProviderData(nil)
	require.Error(t, err)
	data.Payload = json.RawMessage(`[{"type":"function_call","call_id":"bad","name":"f","arguments":"{"}]`)
	_, err = adapter.ProjectProviderData(data)
	require.Error(t, err)
}
