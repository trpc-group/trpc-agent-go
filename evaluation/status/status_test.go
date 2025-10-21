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
