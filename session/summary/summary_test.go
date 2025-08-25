package summary

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionSummary_CompressionRatio(t *testing.T) {
	ss := &SessionSummary{OriginalCount: 10, CompressedCount: 3}
	assert.InDelta(t, 70.0, ss.CompressionRatio(), 0.001)

	ss = &SessionSummary{OriginalCount: 0, CompressedCount: 0}
	assert.Equal(t, 0.0, ss.CompressionRatio())

	ss = &SessionSummary{OriginalCount: 5, CompressedCount: 5}
	assert.Equal(t, 0.0, ss.CompressionRatio())
}
