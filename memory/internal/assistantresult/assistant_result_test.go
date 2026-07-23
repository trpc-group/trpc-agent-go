//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package assistantresult

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIs(t *testing.T) {
	t.Parallel()

	assert.True(t, Is("Assistant result: Recommended Go."))
	assert.True(t, Is(" assistant RESULT: Recommended Go. "))
	assert.False(t, Is("User asked the assistant for a result."))
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"Recommended Go.":                             "Assistant result: Recommended Go.",
		" assistant RESULT: Recommended SQL. ":        "Assistant result: Recommended SQL.",
		"Tokyo: Assistant result: Recommended N'EX.":  "Assistant result: Tokyo: Recommended N'EX.",
		"Assistant result: Assistant result: Use Go.": "Assistant result: Use Go.",
	} {
		assert.Equal(t, want, Normalize(input), input)
	}
}
