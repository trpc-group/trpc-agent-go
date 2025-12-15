//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
)

// Regression: when the request type is a pointer, required fields from the
// underlying struct should still be included in the generated JSON schema.
func TestGenerateJSONSchema_PointerRootKeepsRequired(t *testing.T) {
	type QueryLogReq struct {
		Limit  int    `json:"limit" jsonschema:"required"`
		Offset int    `json:"offset" jsonschema:"required"`
		Note   string `json:"note,omitempty"`
	}

	var req *QueryLogReq
	schema := itool.GenerateJSONSchema(reflect.TypeOf(req))

	bts, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatalf("marshal schema failed: %v", err)
	}
	t.Log(string(bts))

	if schema.Type != "object" {
		t.Fatalf("expected object schema, got %s", schema.Type)
	}
	require.ElementsMatch(t, schema.Required, []string{"limit", "offset"})
}

// Nested pointer input should keep required fields on both the parent and child structs.
func TestGenerateJSONSchema_PointerRootNestedRequired(t *testing.T) {
	type Child struct {
		Size int `json:"size" jsonschema:"required"`
	}

	type Parent struct {
		Child    *Child `json:"child" jsonschema:"required"`
		Inline   Child  `json:"inline"`
		Optional *Child `json:"optional,omitempty"`
	}

	var req *Parent
	schema := itool.GenerateJSONSchema(reflect.TypeOf(req))

	if schema.Type != "object" {
		t.Fatalf("expected object schema, got %s", schema.Type)
	}

	require.ElementsMatch(t, schema.Required, []string{"child", "inline"})

	childSchema := schema.Properties["child"]
	require.NotNil(t, childSchema)
	require.ElementsMatch(t, childSchema.Required, []string{"size"})

	inlineSchema := schema.Properties["inline"]
	require.NotNil(t, inlineSchema)
	require.ElementsMatch(t, inlineSchema.Required, []string{"size"})

	optionalSchema := schema.Properties["optional"]
	require.NotNil(t, optionalSchema)
	require.ElementsMatch(t, optionalSchema.Required, []string{"size"})
}
