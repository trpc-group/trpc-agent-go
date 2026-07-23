//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnicodeRougeTokenizer(t *testing.T) {
	tokenizer := unicodeRougeTokenizer{}
	assert.Equal(t,
		[]string{"订", "单", "a100", "状", "态", "正", "常"},
		tokenizer.Tokenize("订单 A100 状态正常。"),
	)
	assert.Equal(t,
		[]string{"order", "a100", "is", "normal"},
		tokenizer.Tokenize("Order A100 is NORMAL."),
	)
}

func TestTextSimilarityMultilingualAndOrdered(t *testing.T) {
	tests := []struct {
		name     string
		expected string
		actual   string
		want     float64
	}{
		{name: "exact", expected: "Same answer", actual: " same   ANSWER ", want: 1},
		{name: "punctuation", expected: "Order A100 is normal.", actual: "order A100 is normal!", want: 1},
		{name: "chinese partial", expected: "订单 A100 状态正常", actual: "A100订单正常", want: 0.75},
		{name: "english partial", expected: "order a100 status is normal", actual: "a100 is normal", want: 0.75},
		{name: "ordered", expected: "a b c", actual: "c b a", want: 2.0 / 3.0},
		{name: "one empty", expected: "answer", actual: "", want: 0},
		{name: "both empty", expected: "", actual: "", want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			score, err := textSimilarity(context.Background(), test.expected, test.actual)
			require.NoError(t, err)
			assert.InDelta(t, test.want, score, 1e-9)
			reverse, err := textSimilarity(context.Background(), test.actual, test.expected)
			require.NoError(t, err)
			assert.InDelta(t, score, reverse, 1e-9)
			assert.GreaterOrEqual(t, score, 0.0)
			assert.LessOrEqual(t, score, 1.0)
		})
	}
}

func TestTextSimilarityPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := textSimilarity(ctx, "expected", "actual")
	require.ErrorIs(t, err, context.Canceled)
}
