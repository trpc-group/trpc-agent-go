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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvalStatusString(t *testing.T) {
	tests := map[EvalStatus]string{
		EvalStatusUnknown:      "unknown",
		EvalStatusPassed:       "passed",
		EvalStatusFailed:       "failed",
		EvalStatusNotEvaluated: "not_evaluated",
		EvalStatus(99):         "unknown",
	}

	for input, expected := range tests {
		assert.Equal(t, expected, input.String())
	}
}
