//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package status

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvalStatusJSONRoundTrip(t *testing.T) {
	tests := map[EvalStatus]string{
		EvalStatusUnknown:      `"unknown"`,
		EvalStatusPassed:       `"passed"`,
		EvalStatusFailed:       `"failed"`,
		EvalStatusNotEvaluated: `"not_evaluated"`,
	}

	for statusValue, expectedJSON := range tests {
		data, err := json.Marshal(statusValue)
		assert.NoError(t, err)
		assert.Equal(t, expectedJSON, string(data))

		var decoded EvalStatus
		assert.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, statusValue, decoded)
	}
}

func TestEvalStatusUnmarshalRejectsNonString(t *testing.T) {
	var invalid EvalStatus
	err := json.Unmarshal([]byte(`1`), &invalid)
	assert.Error(t, err)
}
