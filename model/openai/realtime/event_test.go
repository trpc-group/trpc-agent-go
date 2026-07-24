//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package realtime

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventRoundTrip(t *testing.T) {
	tests := []string{
		`{"type":"session.update","session":{"modalities":["text","audio"]}}`,
		`{"type":"input_audio_buffer.append","audio":"AAEC"}`,
		`{"type":"conversation.item.input_audio_transcription.completed","transcript":"hello"}`,
		`{"type":"response.done","response":{"id":"resp_1"}}`,
		`{"type":"response.function_call_arguments.done","call_id":"call_1","arguments":"{\"city\":\"Paris\"}"}`,
		`{"type":"conversation.item.create","item":{"type":"function_call_output","call_id":"call_1","output":"sunny"}}`,
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			event, err := ParseEvent([]byte(input))
			require.NoError(t, err)
			assert.NotEmpty(t, event.Type())
			assert.JSONEq(t, input, string(event.Bytes()))

			encoded, err := json.Marshal(event)
			require.NoError(t, err)
			assert.JSONEq(t, input, string(encoded))

			var decoded Event
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			assert.Equal(t, event.Type(), decoded.Type())
		})
	}
}

func TestEventValidation(t *testing.T) {
	_, err := ParseEvent([]byte(`{`))
	assert.ErrorContains(t, err, "decode event")

	_, err = ParseEvent([]byte(`{"event_id":"evt_1"}`))
	assert.ErrorContains(t, err, "event type is required")

	_, err = NewEvent("", nil)
	assert.ErrorContains(t, err, "event type is required")

	event, err := NewEvent("response.cancel", map[string]any{
		"type":        "ignored",
		"response_id": "resp_1",
	})
	require.NoError(t, err)
	assert.Equal(t, "response.cancel", event.Type())
	assert.JSONEq(t, `{"type":"response.cancel","response_id":"resp_1"}`, string(event.Bytes()))

	_, err = json.Marshal(Event{})
	assert.ErrorContains(t, err, "invalid empty event")
}
