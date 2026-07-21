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
	"errors"
	"strings"
	"unicode"

	rougecriterion "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
)

var (
	responseRouge1 = rougecriterion.New(
		rougecriterion.WithRougeType("rouge1"),
		rougecriterion.WithMeasure(rougecriterion.RougeMeasureF1),
		rougecriterion.WithTokenizer(unicodeRougeTokenizer{}),
	)
	responseRougeL = rougecriterion.New(
		rougecriterion.WithRougeType("rougeL"),
		rougecriterion.WithMeasure(rougecriterion.RougeMeasureF1),
		rougecriterion.WithTokenizer(unicodeRougeTokenizer{}),
	)
)

// unicodeRougeTokenizer keeps Latin words and numbers together while
// tokenizing Han text by rune. This avoids the repository's default ASCII-only
// ROUGE tokenizer dropping Chinese responses entirely.
type unicodeRougeTokenizer struct{}

func (unicodeRougeTokenizer) Tokenize(value string) []string {
	tokens := make([]string, 0)
	var word strings.Builder
	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		tokens = append(tokens, word.String())
		word.Reset()
	}
	for _, current := range value {
		current = unicode.ToLower(current)
		switch {
		case unicode.Is(unicode.Han, current):
			flushWord()
			tokens = append(tokens, string(current))
		case unicode.IsLetter(current) || unicode.IsDigit(current) ||
			(unicode.IsMark(current) && word.Len() > 0):
			word.WriteRune(current)
		default:
			flushWord()
		}
	}
	flushWord()
	return tokens
}

// textSimilarity returns a deterministic multilingual lexical-semantic score.
// It blends ROUGE-1 coverage and ROUGE-L ordering so paraphrases can receive
// partial credit without treating a bag-of-words permutation as an exact match.
func textSimilarity(ctx context.Context, expected, actual string) (float64, error) {
	if ctx == nil {
		return 0, errors.New("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if normalizeText(expected) == normalizeText(actual) {
		return 1, nil
	}
	if strings.TrimSpace(expected) == "" || strings.TrimSpace(actual) == "" {
		return 0, nil
	}
	rouge1, err := responseRouge1.Match(ctx, expected, actual)
	if err != nil {
		return 0, err
	}
	rougeL, err := responseRougeL.Match(ctx, expected, actual)
	if err != nil {
		return 0, err
	}
	return clampScore(0.5*rouge1.Value + 0.5*rougeL.Value), nil
}
