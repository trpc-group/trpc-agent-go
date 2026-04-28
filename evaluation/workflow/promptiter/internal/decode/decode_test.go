//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package decode

import (
	"testing"

	"github.com/stretchr/testify/assert"

	irunner "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/runner"
)

type samplePayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecodeOutputJSONRejectsNilOutput(t *testing.T) {
	decoded, err := DecodeOutputJSON[samplePayload](nil)

	assert.NoError(t, err)
	assert.Nil(t, decoded)
}

func TestDecodeOutputJSONUsesStructuredOutputPointer(t *testing.T) {
	structured := &samplePayload{
		Name:  "runner",
		Count: 1,
	}

	decoded, err := DecodeOutputJSON[samplePayload](&irunner.Output{
		StructuredOutput: structured,
		FinalContent:     `{"name":"ignored","count":2}`,
	})

	assert.NoError(t, err)
	assert.Equal(t, &samplePayload{
		Name:  "runner",
		Count: 1,
	}, decoded)
	assert.NotSame(t, structured, decoded)
}

func TestDecodeOutputJSONUsesStructuredOutputValue(t *testing.T) {
	decoded, err := DecodeOutputJSON[samplePayload](&irunner.Output{
		StructuredOutput: samplePayload{
			Name:  "runner",
			Count: 2,
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, &samplePayload{
		Name:  "runner",
		Count: 2,
	}, decoded)
}

func TestDecodeOutputJSONFallsBackToFinalContent(t *testing.T) {
	decoded, err := DecodeOutputJSON[samplePayload](&irunner.Output{
		FinalContent: `{"name":"runner","count":3}`,
	})

	assert.NoError(t, err)
	assert.Equal(t, &samplePayload{
		Name:  "runner",
		Count: 3,
	}, decoded)
}

func TestDecodeOutputJSONAcceptsGenericStructuredOutputObject(t *testing.T) {
	decoded, err := DecodeOutputJSON[samplePayload](&irunner.Output{
		StructuredOutput: map[string]any{
			"name":  "runner",
			"count": 4,
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, &samplePayload{
		Name:  "runner",
		Count: 4,
	}, decoded)
}

func TestDecodeOutputJSONRejectsInvalidFinalContent(t *testing.T) {
	decoded, err := DecodeOutputJSON[samplePayload](&irunner.Output{
		FinalContent: `[]`,
	})

	assert.Error(t, err)
	assert.Nil(t, decoded)
}
