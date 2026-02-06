//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rouge

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNLTKSentTokenizeEnglish_Compatibility verifies sentence splitting behavior against NLTK examples.
func TestNLTKSentTokenizeEnglish_Compatibility(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{
			input:    "this is a test. . new sentence.",
			expected: []string{"this is a test.", ".", "new sentence."},
		},
		{
			input:    "This. . . That",
			expected: []string{"This.", ".", ".", "That"},
		},
		{
			input:    "This..... That",
			expected: []string{"This..... That"},
		},
		{
			input:    "This... That",
			expected: []string{"This... That"},
		},
		{
			input:    "This.. . That",
			expected: []string{"This.. .", "That"},
		},
		{
			input:    "This. .. That",
			expected: []string{"This.", ".. That"},
		},
		{
			input:    "This. ,. That",
			expected: []string{"This.", ",.", "That"},
		},
		{
			input:    "This!!! That",
			expected: []string{"This!!!", "That"},
		},
		{
			input:    "This! That",
			expected: []string{"This!", "That"},
		},
		{
			input:    "1. This is R .\n2. This is A .\n3. That's all",
			expected: []string{"1.", "This is R .", "2.", "This is A .", "3.", "That's all"},
		},
		{
			input:    "1. This is R .\t2. This is A .\t3. That's all",
			expected: []string{"1.", "This is R .", "2.", "This is A .", "3.", "That's all"},
		},
		{
			input:    "Hello.\tThere",
			expected: []string{"Hello.", "There"},
		},
	}

	for _, tc := range cases {
		actual, err := nltkSentTokenizeEnglish(tc.input)
		require.NoError(t, err)
		assert.Equal(t, tc.expected, actual)
	}
}

// TestNLTKSentTokenizeEnglish_CachedError verifies that cached initialization errors are returned.
func TestNLTKSentTokenizeEnglish_CachedError(t *testing.T) {
	origOnce := englishSentenceTokenizerOnce
	origTok := englishSentenceTokenizer
	origErr := englishSentenceTokenizerErr
	defer func() {
		englishSentenceTokenizerOnce = origOnce
		englishSentenceTokenizer = origTok
		englishSentenceTokenizerErr = origErr
	}()

	englishSentenceTokenizerOnce = sync.Once{}
	englishSentenceTokenizerOnce.Do(func() {})
	englishSentenceTokenizer = nil
	englishSentenceTokenizerErr = fmt.Errorf("cached error")

	_, err := nltkSentTokenizeEnglish("Hello.")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cached error")
}

// TestNLTKSentTokenizeEnglish_NilTokenizer verifies that a nil tokenizer returns an error.
func TestNLTKSentTokenizeEnglish_NilTokenizer(t *testing.T) {
	origOnce := englishSentenceTokenizerOnce
	origTok := englishSentenceTokenizer
	origErr := englishSentenceTokenizerErr
	defer func() {
		englishSentenceTokenizerOnce = origOnce
		englishSentenceTokenizer = origTok
		englishSentenceTokenizerErr = origErr
	}()

	englishSentenceTokenizerOnce = sync.Once{}
	englishSentenceTokenizerOnce.Do(func() {})
	englishSentenceTokenizer = nil
	englishSentenceTokenizerErr = nil

	_, err := nltkSentTokenizeEnglish("Hello.")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "english sentence tokenizer is nil")
}
