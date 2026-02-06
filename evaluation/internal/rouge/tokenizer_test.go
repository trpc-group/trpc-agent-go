//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rouge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTokenizer_WithStemmer_NLTKExtensionsIrregularForms verifies irregular form handling through the tokenizer.
func TestTokenizer_WithStemmer_NLTKExtensionsIrregularForms(t *testing.T) {
	tok := newTokenizer(true)
	tokens := tok.Tokenize("skies dying lying tying innings outings cannings")
	assert.Equal(t, []string{"sky", "die", "lie", "tie", "inning", "outing", "canning"}, tokens)
}

// TestTokenizer_WithStemmer_NLTKExtensionsRules verifies selected NLTK_EXTENSIONS stemming rule behaviors through the tokenizer.
func TestTokenizer_WithStemmer_NLTKExtensionsRules(t *testing.T) {
	tok := newTokenizer(true)
	tokens := tok.Tokenize("dies died spied enjoy")
	assert.Equal(t, []string{"die", "die", "spi", "enjoy"}, tokens)
}

// TestTokenizer_CaseAndPunctuation verifies lowercasing and punctuation normalization in tokenization.
func TestTokenizer_CaseAndPunctuation(t *testing.T) {
	tok := newTokenizer(false)
	tokens := tok.Tokenize("Hello, WORLD!")
	assert.Equal(t, []string{"hello", "world"}, tokens)
}

// TestTokenizer_WithStemmer verifies stemming behavior in tokenization.
func TestTokenizer_WithStemmer(t *testing.T) {
	tok := newTokenizer(true)
	tokens := tok.Tokenize("the friends had a meeting")
	assert.Equal(t, []string{"the", "friend", "had", "a", "meet"}, tokens)
}

// TestStem_NLTKPorterStemmerExamples verifies Porter stemming results against NLTK_EXTENSIONS behavior.
func TestStem_NLTKPorterStemmerExamples(t *testing.T) {
	cases := []struct {
		word     string
		expected string
	}{
		{word: "hoping", expected: "hope"},
		{word: "hopping", expected: "hop"},
		{word: "falling", expected: "fall"},
		{word: "controlling", expected: "control"},
		{word: "automatically", expected: "automat"},
		{word: "geology", expected: "geolog"},
		{word: "relational", expected: "relat"},
		{word: "triplicate", expected: "triplic"},
		{word: "hopefulness", expected: "hope"},
		{word: "callousness", expected: "callous"},
		{word: "electricity", expected: "electr"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, stem(tc.word))
	}
}
