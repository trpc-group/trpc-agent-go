//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package structuredoutput

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

type testPayload struct {
	Answer   string `json:"answer"`
	Optional *int   `json:"optional,omitempty"`
}

func TestName(t *testing.T) {
	require.Equal(t, "output", Name(""))
	require.Equal(t, "custom", Name("custom"))
}

func TestTypeOf(t *testing.T) {
	require.Nil(t, TypeOf(nil))
	require.Equal(t, reflect.TypeOf((*testPayload)(nil)), TypeOf(new(testPayload)))
	require.Equal(t, reflect.TypeOf((*testPayload)(nil)), TypeOf(testPayload{}))
}

func TestFromType_Strict(t *testing.T) {
	name, schema, outputType := FromType(new(testPayload), true)

	require.Equal(t, "testPayload", name)
	require.Equal(t, reflect.TypeOf((*testPayload)(nil)), outputType)
	require.Equal(t, "object", schema["type"])

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, properties, "answer")
	require.Contains(t, properties, "optional")
	_, hasAnyOf := properties["optional"].(map[string]any)["anyOf"]
	require.True(t, hasAnyOf)

	required, ok := schema["required"].([]string)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"answer", "optional"}, required)
}

func TestFromType_NonStrict(t *testing.T) {
	name, schema, outputType := FromType(testPayload{}, false)

	require.Equal(t, "testPayload", name)
	require.Equal(t, reflect.TypeOf((*testPayload)(nil)), outputType)

	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	_, hasAnyOf := properties["optional"].(map[string]any)["anyOf"]
	require.False(t, hasAnyOf)

	required, ok := schema["required"].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"answer"}, required)
}

func TestFromType_NilAndUnnamed(t *testing.T) {
	name, schema, outputType := FromType(nil, true)
	require.Empty(t, name)
	require.Nil(t, schema)
	require.Nil(t, outputType)

	name, schema, outputType = FromType(struct {
		Value string `json:"value"`
	}{}, true)
	require.Equal(t, "output", name)
	require.NotNil(t, schema)
	require.NotNil(t, outputType)
}
