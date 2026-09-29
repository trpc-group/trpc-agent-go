//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonutils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeLeadingJSON(t *testing.T) {
	t.Run("decodes a single JSON object", func(t *testing.T) {
		var parsed struct {
			Action string `json:"action"`
		}
		err := DecodeLeadingJSON(`{"action":"GO_BACK"}`, &parsed)
		require.NoError(t, err)
		require.Equal(t, "GO_BACK", parsed.Action)
	})

	t.Run("ignores trailing markdown and footers", func(t *testing.T) {
		var parsed struct {
			Action string `json:"action"`
		}
		raw := `{"action":"GO_BACK","target_stage":"implement-module"}

---
` + "```json workflow_notes_snapshot\n{\"type\":\"workflow_notes_snapshot\"}\n```\n"
		err := DecodeLeadingJSON(raw, &parsed)
		require.NoError(t, err)
		require.Equal(t, "GO_BACK", parsed.Action)
	})

	t.Run("returns error for empty input", func(t *testing.T) {
		var parsed map[string]any
		err := DecodeLeadingJSON("   ", &parsed)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty JSON input")
	})
}

func TestDecodeFlexibleJSON_EmptyInput(t *testing.T) {
	var parsed map[string]any
	err := DecodeFlexibleJSON("   ", &parsed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty JSON input")
}

func TestDecodeFlexibleJSON(t *testing.T) {
	t.Run("decodes JSON embedded after leading prose", func(t *testing.T) {
		var parsed struct {
			Status string `json:"status"`
		}
		err := DecodeFlexibleJSON(`Summary: {"status":"PASS"}`, &parsed)
		require.NoError(t, err)
		require.Equal(t, "PASS", parsed.Status)
	})

	t.Run("ignores trailing content after embedded JSON", func(t *testing.T) {
		var parsed struct {
			Matched bool `json:"matched"`
		}
		err := DecodeFlexibleJSON(`Here you go: {"matched":true} — done.`, &parsed)
		require.NoError(t, err)
		require.True(t, parsed.Matched)
	})

	t.Run("returns error when no JSON value is present", func(t *testing.T) {
		var parsed map[string]any
		err := DecodeFlexibleJSON("no structured data here", &parsed)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no JSON value found")
	})

	t.Run("preserves original error when input already starts with JSON", func(t *testing.T) {
		var parsed map[string]any
		err := DecodeFlexibleJSON(`{invalid json}`, &parsed)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "no JSON value found")
	})

	t.Run("repairs malformed JSON with single-quoted keys", func(t *testing.T) {
		var parsed struct {
			Name string `json:"name"`
		}
		err := DecodeFlexibleJSON(`{name:'John'}`, &parsed)
		require.NoError(t, err)
		require.Equal(t, "John", parsed.Name)
	})

	t.Run("decodes markdown-fenced JSON", func(t *testing.T) {
		var parsed struct {
			Action string `json:"action"`
		}
		raw := "```json\n{\"action\":\"FINISH\"}\n```"
		err := DecodeFlexibleJSON(raw, &parsed)
		require.NoError(t, err)
		require.Equal(t, "FINISH", parsed.Action)
	})
}

func TestUnmarshalUseNumber(t *testing.T) {
	var data map[string]any
	require.NoError(t, UnmarshalUseNumber([]byte(`{"id":9007199254740993,"values":[1e400]}`), &data))
	require.Equal(t, json.Number("9007199254740993"), data["id"])
	require.Equal(t, []any{json.Number("1e400")}, data["values"])

	for _, raw := range []string{`{"id":1} {"id":2}`, `{"id":1} trailing`, `{invalid}`, ``} {
		t.Run(raw, func(t *testing.T) {
			dest := map[string]any{"original": true}
			require.Error(t, UnmarshalUseNumber([]byte(raw), &dest))
			require.Equal(t, map[string]any{"original": true}, dest)
		})
	}
	var typed struct{ ID int64 }
	require.NoError(t, UnmarshalUseNumber([]byte("{\"ID\":9007199254740993} \n\t"), &typed))
	require.Equal(t, int64(9007199254740993), typed.ID)
}
