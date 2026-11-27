//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responseshistory

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadCompletedPreservesNativeRecords(t *testing.T) {
	const (
		reasoning = `{"type":"reasoning","id":"r","encrypted_content":"opaque","future":9007199254740993}`
		call      = `{"type":"function_call","call_id":"c","name":"lookup","arguments":"{}"}`
		message   = `{"type":"message","id":"m","status":"completed","role":"assistant","content":[{"type":"output_text","text":"source","annotations":[{"type":"url_citation"}]}]}`
	)
	// Both annotated text and a message following a call are valid native
	// records, even for a target that cannot represent them in this order.
	input := json.RawMessage("[" + reasoning + "," + call + "," + message + "]")
	items, err := ReadCompleted(input)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, reasoning, string(items[0].Raw))
	require.Equal(t, call, string(items[1].Raw))
	require.Empty(t, items[1].ID)
	require.Equal(t, "c", items[1].CallID)
	require.Equal(t, message, string(items[2].Raw))
	require.Len(t, items[2].Content, 1)
	require.Equal(t, "source", items[2].Content[0].Text)
	require.JSONEq(t, `[{"type":"url_citation"}]`, string(items[2].Content[0].Annotations))
}

func TestReadCompletedRejectsInvalidHistory(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"missing payload", "", "payload is empty"},
		{"null array", "null", "must be an item array"},
		{"object instead of array", "{}", "must be an item array"},
		{"invalid second item", `[{"type":"reasoning","id":"r"},42]`, "item 1: decode:"},
		{"unknown type", `[{"type":"other"}]`, "unsupported native item type"},
		{"missing reasoning id", `[{"type":"reasoning"}]`, "empty id"},
		{"unfinished call", `[{"type":"function_call","status":"in_progress"}]`, "want completed"},
		{"missing message status", `[{"type":"message","id":"m","role":"assistant"}]`, "want completed"},
		{"wrong role", `[{"type":"message","id":"m","status":"completed","role":"user"}]`, "want assistant"},
		{"unsupported content", `[{"type":"message","id":"m","status":"completed","role":"assistant","content":[{"type":"other"}]}]`, "unsupported content type"},
		{"missing call id", `[{"type":"function_call","id":"item","name":"f","arguments":"{}"}]`, "empty call_id"},
		{"missing function name", `[{"type":"function_call","call_id":"c","arguments":"{}"}]`, "empty name"},
		{"non-object arguments", `[{"type":"function_call","call_id":"c","name":"f","arguments":"[]"}]`, "invalid JSON arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := ReadCompleted(json.RawMessage(tc.input))
			require.ErrorContains(t, err, tc.want)
			require.Nil(t, items)
		})
	}
}

func TestReadToolSearchRequiresConsumerSupport(t *testing.T) {
	const call = `{"type":"tool_search_call","execution":"client","call_id":"s","arguments":{"query":"invoice"}}`
	const result = `{"type":"tool_search_output","execution":"client","call_id":"s","tools":[],"future":9007199254740993}`
	raw := json.RawMessage("[" + call + "," + result + "]")
	_, err := ReadCompleted(raw)
	require.ErrorContains(t, err, "unsupported native item type")
	items, err := ReadCompletedWithToolSearch(raw)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, call, string(items[0].Raw))
	require.JSONEq(t, `{"query":"invoice"}`, string(items[0].SearchArguments()))
	require.Equal(t, result, string(items[1].Raw))
}
