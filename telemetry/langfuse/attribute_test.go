//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstants(t *testing.T) {
	// Test trace attribute constants
	assert.Equal(t, "langfuse.trace.name", traceName)
	assert.Equal(t, "langfuse.user.id", traceUserID)
	assert.Equal(t, "langfuse.session.id", traceSessionID)
	assert.Equal(t, "langfuse.trace.tags", traceTags)
	assert.Equal(t, "langfuse.trace.public", tracePublic)
	assert.Equal(t, "langfuse.trace.metadata", traceMetadata)
	assert.Equal(t, "langfuse.trace.input", traceInput)
	assert.Equal(t, "langfuse.trace.output", traceOutput)

	// Test observation attribute constants
	assert.Equal(t, "langfuse.observation.type", observationType)
	assert.Equal(t, "langfuse.observation.metadata", observationMetadata)
	assert.Equal(t, "langfuse.observation.level", observationLevel)
	assert.Equal(t, "langfuse.observation.status_message", observationStatusMessage)
	assert.Equal(t, "langfuse.observation.input", observationInput)
	assert.Equal(t, "langfuse.observation.output", observationOutput)

	// Test generation attribute constants
	assert.Equal(t, "langfuse.observation.completion_start_time", observationCompletionStartTime)
	assert.Equal(t, "langfuse.observation.model.name", observationModel)
	assert.Equal(t, "langfuse.observation.model.parameters", observationModelParameters)
	assert.Equal(t, "langfuse.observation.usage_details", observationUsageDetails)
	assert.Equal(t, "langfuse.observation.cost_details", observationCostDetails)
	assert.Equal(t, "langfuse.observation.prompt.name", observationPromptName)
	assert.Equal(t, "langfuse.observation.prompt.version", observationPromptVersion)

	// Test general attribute constants
	assert.Equal(t, "langfuse.environment", environment)
	assert.Equal(t, "langfuse.release", release)
	assert.Equal(t, "langfuse.version", version)

}

func TestConstantTypes(t *testing.T) {
	// Ensure all constants are strings
	constants := []any{
		traceName, traceUserID, traceSessionID, traceTags, tracePublic,
		traceMetadata, traceInput, traceOutput,
		observationType, observationMetadata, observationLevel, observationStatusMessage,
		observationInput, observationOutput,
		observationCompletionStartTime, observationModel, observationModelParameters,
		observationUsageDetails, observationCostDetails, observationPromptName, observationPromptVersion,
		environment, release, version,
	}

	for _, constant := range constants {
		assert.IsType(t, "", constant, "All constants should be strings")
	}
}

func TestConstantUniqueness(t *testing.T) {
	// Collect all constants to check for duplicates
	constants := []string{
		traceName, traceUserID, traceSessionID, traceTags, tracePublic,
		traceMetadata, traceInput, traceOutput,
		observationType, observationMetadata, observationLevel, observationStatusMessage,
		observationInput, observationOutput,
		observationCompletionStartTime, observationModel, observationModelParameters,
		observationUsageDetails, observationCostDetails, observationPromptName, observationPromptVersion,
		environment, release, version,
	}

	// Check for duplicates
	seen := make(map[string]bool)
	for _, constant := range constants {
		assert.False(t, seen[constant], "Constant %s should be unique", constant)
		seen[constant] = true
	}
}

func TestConstantNamingConvention(t *testing.T) {
	// Test that all constants follow the expected naming convention
	// Langfuse constants should start with "langfuse."
	langfuseConstants := []string{
		traceName, traceTags, tracePublic, traceMetadata, traceInput, traceOutput,
		observationType, observationMetadata, observationLevel, observationStatusMessage,
		observationInput, observationOutput, observationCompletionStartTime,
		observationModel, observationModelParameters, observationUsageDetails,
		observationCostDetails, observationPromptName, observationPromptVersion,
		environment, release, version,
	}

	for _, constant := range langfuseConstants {
		assert.Contains(t, constant, "langfuse.", "Langfuse constant %s should contain 'langfuse.'", constant)
	}

	// Test standard OpenTelemetry attributes
	assert.Equal(t, "langfuse.user.id", traceUserID)
	assert.Equal(t, "langfuse.session.id", traceSessionID)
}

func TestUsageDetailsNormalized(t *testing.T) {
	tests := []struct {
		name  string
		usage usageDetails
		want  usageDetails
	}{
		{
			name:  "without cache",
			usage: usageDetails{Input: 100, Output: 50},
			want:  usageDetails{Input: 100, Output: 50},
		},
		{
			name:  "inclusive cached input",
			usage: usageDetails{Input: 100, Output: 50, InputCached: 30},
			want:  usageDetails{Input: 70, Output: 50, InputCached: 30},
		},
		{
			name:  "all input cached",
			usage: usageDetails{Input: 30, Output: 10, InputCached: 30},
			want:  usageDetails{Output: 10, InputCached: 30},
		},
		{
			name:  "cached input exceeds input",
			usage: usageDetails{Input: 20, Output: 10, InputCached: 30},
			want:  usageDetails{Output: 10, InputCached: 30},
		},
		{
			name: "provider cache read replaces compatibility alias",
			usage: usageDetails{
				Input:          20,
				Output:         10,
				InputCached:    80,
				InputCacheRead: 80,
			},
			want: usageDetails{
				Input:          20,
				Output:         10,
				InputCacheRead: 80,
			},
		},
		{
			name: "provider cache creation preserves exclusive input",
			usage: usageDetails{
				Input:              20,
				Output:             10,
				InputCacheCreation: 80,
			},
			want: usageDetails{
				Input:              20,
				Output:             10,
				InputCacheCreation: 80,
			},
		},
		{
			name: "provider cache details take precedence",
			usage: usageDetails{
				Input:              20,
				Output:             10,
				InputCached:        70,
				InputCacheRead:     70,
				InputCacheCreation: 10,
			},
			want: usageDetails{
				Input:              20,
				Output:             10,
				InputCacheRead:     70,
				InputCacheCreation: 10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.usage.normalized())
		})
	}
}
