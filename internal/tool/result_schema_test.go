//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestGenerateJSONSchemaToolResultContentMatchesJSON(t *testing.T) {
	schema := GenerateJSONSchema(toolResultContentType)
	schemaJSON, err := json.Marshal(schema)
	require.NoError(t, err)
	var schemaDocument any
	require.NoError(t, json.Unmarshal(schemaJSON, &schemaDocument))

	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource(
		"tool-result-content.json",
		schemaDocument,
	))
	compiled, err := compiler.Compile("tool-result-content.json")
	require.NoError(t, err)

	text := "description"
	cases := []struct {
		name   string
		result model.ToolResultContent
	}{
		{
			name: "text",
			result: model.ToolResultContent{{
				Type: model.ContentTypeText,
				Text: &text,
			}},
		},
		{
			name: "raw image bytes",
			result: model.ToolResultContent{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{Data: []byte{0, 1, 255}, Format: "png"},
			}},
		},
		{
			name: "image url and nil bytes",
			result: model.ToolResultContent{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.test/image.png"},
			}},
		},
		{
			name:   "empty content marker",
			result: model.ToolResultContent{{Type: model.ContentTypeText}},
		},
		{name: "empty slice", result: model.ToolResultContent{}},
		{name: "nil slice", result: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.result)
			require.NoError(t, err)

			var instance any
			require.NoError(t, json.Unmarshal(data, &instance))
			require.NoError(t, compiled.Validate(instance), string(data))
		})
	}
}

func TestToolResultContentSchemaAdjustmentsAreScoped(t *testing.T) {
	ordinaryBytes := GenerateJSONSchema(byteSliceType)
	require.Equal(t, "array", ordinaryBytes.Type)
	require.Equal(t, "integer", ordinaryBytes.Items.Type)
	require.Empty(t, ordinaryBytes.AnyOf)

	ordinaryParts := GenerateJSONSchema(
		reflect.TypeOf([]model.ContentPart(nil)),
	)
	require.Equal(t, "array", ordinaryParts.Type)
	require.Empty(t, ordinaryParts.AnyOf)
	imageSchema := ordinaryParts.Items.Properties["image"]
	require.NotNil(t, imageSchema)
	ordinaryImageBytes := imageSchema.Properties["data"]
	require.Equal(t, "array", ordinaryImageBytes.Type)
	require.Equal(t, "integer", ordinaryImageBytes.Items.Type)
	require.Empty(t, ordinaryImageBytes.AnyOf)
}
