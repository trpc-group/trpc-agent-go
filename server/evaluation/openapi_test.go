//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluation

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestOpenAPISpecIsValid(t *testing.T) {
	loader := &openapi3.Loader{Context: context.Background(), IsExternalRefsAllowed: false}
	doc, err := loader.LoadFromFile(filepath.Join(".", "openapi.yaml"))
	require.NoError(t, err)
	require.NoError(t, doc.Validate(context.Background()))
}

func TestOpenAPISpecDocumentsInferenceStats(t *testing.T) {
	loader := &openapi3.Loader{Context: context.Background(), IsExternalRefsAllowed: false}
	doc, err := loader.LoadFromFile(filepath.Join(".", "openapi.yaml"))
	require.NoError(t, err)

	for _, schemaName := range []string{"EvaluationResult", "EvaluationCaseResult", "EvalCaseResult"} {
		schemaRef, ok := doc.Components.Schemas[schemaName]
		require.Truef(t, ok, "schema %s is missing", schemaName)
		require.NotNil(t, schemaRef.Value)
		property, ok := schemaRef.Value.Properties["inferenceStats"]
		require.Truef(t, ok, "schema %s does not document inferenceStats", schemaName)
		require.NotNil(t, property.Value)
		require.Equal(t, "#/components/schemas/InferenceStats", property.Ref)
	}

	evalSetResult := doc.Components.Schemas["EvalSetResult"]
	require.NotNil(t, evalSetResult)
	require.NotNil(t, evalSetResult.Value)
	_, hasSetStats := evalSetResult.Value.Properties["inferenceStats"]
	require.False(t, hasSetStats, "EvalSetResult should not expose a persisted set-level inferenceStats")

	statsSchema := doc.Components.Schemas["InferenceStats"]
	require.NotNil(t, statsSchema)
	require.NotNil(t, statsSchema.Value)
	duration, ok := statsSchema.Value.Properties["duration"]
	require.True(t, ok)
	require.NotNil(t, duration.Value)
	require.True(t, duration.Value.Type.Is("integer"))
	require.Equal(t, "int64", duration.Value.Format)
	tokenUsage, ok := statsSchema.Value.Properties["tokenUsage"]
	require.True(t, ok)
	require.NotNil(t, tokenUsage.Value)
	require.Equal(t, "#/components/schemas/TokenUsage", tokenUsage.Ref)

	usageSchema := doc.Components.Schemas["TokenUsage"]
	require.NotNil(t, usageSchema)
	require.NotNil(t, usageSchema.Value)
	for _, propertyName := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		property, ok := usageSchema.Value.Properties[propertyName]
		require.Truef(t, ok, "TokenUsage does not document %s", propertyName)
		require.NotNil(t, property.Value)
		require.True(t, property.Value.Type.Is("integer"))
	}
}
