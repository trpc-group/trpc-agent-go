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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testTool struct {
	declaration *tool.Declaration
	call        func([]byte) (any, error)
}

func (t testTool) Declaration() *tool.Declaration                   { return t.declaration }
func (t testTool) Call(_ context.Context, args []byte) (any, error) { return t.call(args) }

func TestGatewayValidatesAndOnlyCallsAllowlistedTools(t *testing.T) {
	called := false
	add := testTool{declaration: &tool.Declaration{Name: "add", InputSchema: &tool.Schema{Type: "object", Required: []string{"a", "b"}, Properties: map[string]*tool.Schema{"a": {Type: "integer"}, "b": {Type: "integer"}}, AdditionalProperties: false}, OutputSchema: &tool.Schema{Type: "object", Required: []string{"sum"}, Properties: map[string]*tool.Schema{"sum": {Type: "integer"}}}}, call: func(raw []byte) (any, error) {
		called = true
		var in struct{ A, B int }
		require.NoError(t, json.Unmarshal(raw, &in))
		return map[string]int{"sum": in.A + in.B}, nil
	}}
	g, err := NewGateway(add)
	require.NoError(t, err)
	_, err = g.Call(context.Background(), "missing", json.RawMessage(`{}`))
	require.Error(t, err)
	_, err = g.Call(context.Background(), "add", json.RawMessage(`{"a":1}`))
	require.Error(t, err)
	require.False(t, called)
	raw, err := g.Call(context.Background(), "add", json.RawMessage(`{"a":2,"b":3}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"sum":5}`, string(raw))
	require.True(t, called)
}

func TestGatewayValidatesCompleteJSONSchema(t *testing.T) {
	called := false
	validate := testTool{
		declaration: &tool.Declaration{
			Name: "validate",
			InputSchema: &tool.Schema{
				Type:                 "object",
				Required:             []string{"item", "labels", "mode"},
				AdditionalProperties: false,
				Properties: map[string]*tool.Schema{
					"item":   {Ref: "#/$defs/item"},
					"labels": {Type: "object", AdditionalProperties: &tool.Schema{Type: "string"}},
					"mode":   {Enum: []any{1}},
				},
				Defs: map[string]*tool.Schema{
					"item": {
						Type:                 "object",
						Required:             []string{"code"},
						AdditionalProperties: false,
						Properties: map[string]*tool.Schema{
							"code": {Type: "string", Pattern: "^[A-Z]{2}-[0-9]{2}$"},
						},
					},
				},
			},
		},
		call: func([]byte) (any, error) {
			called = true
			return map[string]any{"ok": true}, nil
		},
	}
	g, err := NewGateway(validate)
	require.NoError(t, err)

	for _, args := range []string{
		`{"item":{"code":"bad"},"labels":{"env":"prod"},"mode":1}`,
		`{"item":{"code":"AB-12","extra":true},"labels":{"env":"prod"},"mode":1}`,
		`{"item":{"code":"AB-12"},"labels":{"env":1},"mode":1}`,
		`{"item":{"code":"AB-12"},"labels":{"env":"prod"},"mode":"1"}`,
		`{"item":{"code":"AB-12"},"labels":{"env":"prod"},"mode":1,"extra":true}`,
	} {
		_, err := g.Call(context.Background(), "validate", json.RawMessage(args))
		require.Error(t, err, args)
		require.False(t, called)
	}

	_, err = g.Call(context.Background(), "validate", json.RawMessage(`{"item":{"code":"AB-12"},"labels":{"env":"prod"},"mode":1}`))
	require.NoError(t, err)
	require.True(t, called)
}

func TestGatewayRejectsInvalidSchemaAtConstruction(t *testing.T) {
	_, err := NewGateway(testTool{
		declaration: &tool.Declaration{
			Name:        "invalid",
			InputSchema: &tool.Schema{Type: "string", Pattern: "("},
		},
		call: func([]byte) (any, error) { return nil, nil },
	})
	require.Error(t, err)
}

func TestGatewayRejectsExternalSchemaReferenceAtConstruction(t *testing.T) {
	_, err := NewGateway(testTool{
		declaration: &tool.Declaration{
			Name:        "external-ref",
			InputSchema: &tool.Schema{Ref: "https://example.invalid/schema.json"},
		},
		call: func([]byte) (any, error) { return nil, nil },
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "external schema reference")
}

func TestGatewayValidatesOutputJSONSchema(t *testing.T) {
	g, err := NewGateway(testTool{
		declaration: &tool.Declaration{
			Name: "bad-output",
			OutputSchema: &tool.Schema{
				Type:                 "object",
				Required:             []string{"code"},
				AdditionalProperties: false,
				Properties: map[string]*tool.Schema{
					"code": {Type: "string", Pattern: "^[A-Z]{2}-[0-9]{2}$"},
				},
			},
		},
		call: func([]byte) (any, error) { return map[string]string{"code": "bad"}, nil },
	})
	require.NoError(t, err)
	_, err = g.Call(context.Background(), "bad-output", json.RawMessage(`{}`))
	require.ErrorContains(t, err, "invalid output")
}

func TestNewGatewayRejectsInvalidTools(t *testing.T) {
	valid := testTool{declaration: &tool.Declaration{Name: "valid"}}
	tests := []struct {
		name  string
		tools []tool.CallableTool
		want  string
	}{
		{name: "nil tool", tools: []tool.CallableTool{nil}, want: "declaration is required"},
		{name: "nil declaration", tools: []tool.CallableTool{testTool{}}, want: "declaration is required"},
		{name: "blank name", tools: []tool.CallableTool{testTool{declaration: &tool.Declaration{}}}, want: "tool name is required"},
		{name: "duplicate name", tools: []tool.CallableTool{valid, valid}, want: "duplicate tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewGateway(tt.tools...)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestGatewayNamesAndCallFailures(t *testing.T) {
	toolError := errors.New("tool failed")
	g, err := NewGateway(
		testTool{declaration: &tool.Declaration{Name: "zeta", InputSchema: &tool.Schema{Type: "object"}}, call: func([]byte) (any, error) {
			return nil, toolError
		}},
		testTool{declaration: &tool.Declaration{Name: "alpha"}, call: func([]byte) (any, error) {
			return make(chan int), nil
		}},
	)
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "zeta"}, g.Names())

	var nilGateway *Gateway
	_, err = nilGateway.Call(context.Background(), "alpha", json.RawMessage(`{}`))
	require.ErrorContains(t, err, "nil gateway")

	_, err = g.Call(context.Background(), "zeta", json.RawMessage(`not-json`))
	require.ErrorContains(t, err, "invalid input JSON")
	_, err = g.Call(context.Background(), "zeta", json.RawMessage(`{}`))
	require.ErrorIs(t, err, toolError)
	_, err = g.Call(context.Background(), "alpha", json.RawMessage(`{}`))
	require.ErrorContains(t, err, "encode result")
}
