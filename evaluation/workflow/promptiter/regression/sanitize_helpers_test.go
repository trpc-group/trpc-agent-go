//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSanitizeSchemaRecursivelyRedactsStructuredValues(t *testing.T) {
	schema := &tool.Schema{
		Description: "api_key=top-secret",
		Properties: map[string]*tool.Schema{
			"access_token": {Default: "top-secret", Enum: []any{"top-secret"}},
			"payload": {
				Default: map[string]any{"password": "top-secret"},
				Enum:    []any{map[string]any{"secret": "top-secret"}},
			},
		},
		Defs:                 map[string]*tool.Schema{"child": {Description: "Bearer token-value"}},
		Items:                &tool.Schema{Default: map[string]any{"token": "top-secret"}},
		AdditionalProperties: map[string]any{"authorization": "top-secret"},
	}
	sanitizeSchema(schema, AuditPolicy{MaxContentBytes: 64})
	assert.NotContains(t, schema.Description, "top-secret")
	assert.Equal(t, redactedValue, schema.Properties["access_token"].Default)
	assert.Equal(t, []any{redactedValue}, schema.Properties["access_token"].Enum)
	assert.Equal(t, redactedValue, schema.Properties["payload"].Default.(map[string]any)["password"])
	assert.Equal(t, redactedValue, schema.Properties["payload"].Enum[0].(map[string]any)["secret"])
	assert.NotContains(t, schema.Defs["child"].Description, "token-value")
	assert.Equal(t, redactedValue, schema.Items.Default.(map[string]any)["token"])
	assert.Equal(t, redactedValue, schema.AdditionalProperties.(map[string]any)["authorization"])
}

func TestSanitizeMetadataAndStructuredContent(t *testing.T) {
	assert.Nil(t, sanitizeMetadata(nil, AuditPolicy{}))
	metadata := sanitizeMetadata(map[string]string{
		"authorization": "Bearer secret-token",
		"label":         "api_key=secret-value",
	}, AuditPolicy{MaxContentBytes: 64})
	assert.Equal(t, redactedValue, metadata["authorization"])
	assert.NotContains(t, metadata["label"], "secret-value")

	decoded := sanitizeStructuredContent(AuditPolicy{MaxContentBytes: 64}, `{"token":"secret-value","nested":{"password":"secret-value"}}`)
	assert.NotContains(t, decoded, "secret-value")
	assert.Contains(t, decoded, redactedValue)
	assert.NotContains(t, sanitizeStructuredContent(AuditPolicy{MaxContentBytes: 64}, "api_key=secret-value"), "secret-value")
}

func TestHasExecutionErrorChecksEveryObservation(t *testing.T) {
	assert.False(t, hasExecutionError(&CaseResult{Runs: []Observation{{}}}))
	assert.True(t, hasExecutionError(&CaseResult{Runs: []Observation{{Tools: []ToolObservation{{Error: "tool failed"}}}}}))
	assert.True(t, hasExecutionError(&CaseResult{Runs: []Observation{{Trace: []TraceStep{{Error: "step failed"}}}}}))

	_, err := SanitizeRunResult(nil)
	require.ErrorContains(t, err, "run result is nil")
}
