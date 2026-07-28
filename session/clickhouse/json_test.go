//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestUnmarshalStoredJSONNormalizesClickHouseNumericFields(t *testing.T) {
	data := `{"author":"agent","choices":[{"index":"0","message":{"role":"assistant","content":"ok"}}],"created":"1","id":"event-id","invocationId":"invocation-id","timestamp":"2026-07-28T00:00:00Z","version":"1"}`

	var got event.Event
	require.NoError(t, unmarshalStoredJSON(data, &got))
	require.NotNil(t, got.Response)
	require.Equal(t, int64(1), got.Created)
	require.Equal(t, 0, got.Choices[0].Index)
	require.Equal(t, 1, got.Version)
}

func TestUnmarshalStoredJSONAcceptsQuotedDocument(t *testing.T) {
	data := `"{\"id\":\"event-id\",\"invocationId\":\"invocation-id\",\"timestamp\":\"2026-07-28T00:00:00Z\",\"version\":1}"`

	var got event.Event
	require.NoError(t, unmarshalStoredJSON(data, &got))
	require.Equal(t, "event-id", got.ID)
	require.Equal(t, 1, got.Version)
}

// EmbeddedJSONFields exercises anonymous exported JSON fields.
type EmbeddedJSONFields struct {
	Enabled bool `json:"enabled"`
}

type storedJSONFields struct {
	EmbeddedJSONFields
	Count    int             `json:"count"`
	Unsigned uint            `json:"unsigned"`
	Ratio    float64         `json:"ratio"`
	Items    []int           `json:"items"`
	Array    [1]bool         `json:"array"`
	Values   map[string]int  `json:"values"`
	Raw      json.RawMessage `json:"raw"`
	Ignored  string          `json:"-"`
	Default  int
	private  string
}

func TestNormalizeStoredJSONCoversTypedShapes(t *testing.T) {
	input := map[string]any{
		"enabled":  "true",
		"count":    "12",
		"unsigned": "13",
		"ratio":    "1.5",
		"items":    []any{"1", "2"},
		"array":    []any{"false"},
		"values":   map[string]any{"one": "1"},
		"raw":      map[string]any{"kept": "as-is"},
		"Default":  "14",
		"unknown":  "ignored",
	}
	normalized := normalizeStoredJSON(input, reflect.TypeFor[*storedJSONFields](), false)
	encoded, err := json.Marshal(normalized)
	require.NoError(t, err)
	var got storedJSONFields
	require.NoError(t, json.Unmarshal(encoded, &got))
	require.True(t, got.Enabled)
	require.Equal(t, 12, got.Count)
	require.Equal(t, uint(13), got.Unsigned)
	require.Equal(t, 1.5, got.Ratio)
	require.Equal(t, []int{1, 2}, got.Items)
	require.Equal(t, [1]bool{false}, got.Array)
	require.Equal(t, map[string]int{"one": 1}, got.Values)
	require.JSONEq(t, `{"kept":"as-is"}`, string(got.Raw))
	require.Equal(t, 14, got.Default)

	require.Equal(t, "value", normalizeStoredJSON("value", nil, false))
	require.Equal(t, "value", normalizeStoredJSON("value", reflect.TypeFor[storedJSONFields](), false))
	require.Equal(t, "value", normalizeStoredJSON("value", reflect.TypeFor[[]int](), false))
	require.Equal(t, "value", normalizeStoredJSON("value", reflect.TypeFor[map[string]int](), false))
	require.Equal(t, "bad", normalizeStoredJSON("bad", reflect.TypeFor[int8](), false))
	require.Equal(t, "-1", normalizeStoredJSON("-1", reflect.TypeFor[uint8](), false))
	require.Equal(t, "bad", normalizeStoredJSON("bad", reflect.TypeFor[float32](), false))
	require.Equal(t, "bad", normalizeStoredJSON("bad", reflect.TypeFor[bool](), false))
	require.Equal(t, map[int]string{1: "one"},
		normalizeStoredJSON(map[int]string{1: "one"}, reflect.TypeFor[map[int]string](), false))
}

func TestUnmarshalStoredJSONRejectsInvalidDocuments(t *testing.T) {
	var got storedJSONFields
	require.Error(t, unmarshalStoredJSON("{", &got))

	var channel chan int
	require.Error(t, unmarshalStoredJSON("1", &channel))
}
