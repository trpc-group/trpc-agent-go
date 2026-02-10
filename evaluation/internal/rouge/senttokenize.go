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
	"strings"
	"sync"

	"github.com/neurosnap/sentences"
	sentencesdata "github.com/neurosnap/sentences/data"
)

var (
	// englishSentenceTokenizerOnce ensures the Punkt model is loaded once.
	englishSentenceTokenizerOnce sync.Once
	// englishSentenceTokenizer holds the initialized sentence tokenizer instance.
	englishSentenceTokenizer *sentences.DefaultSentenceTokenizer
	// englishSentenceTokenizerErr caches any initialization error.
	englishSentenceTokenizerErr error
)

// nltkSentTokenizeEnglish splits English text into sentences using Punkt training data.
// This function aims to match NLTK's sent_tokenize behavior for rougeLsum sentence splitting.
func nltkSentTokenizeEnglish(text string) ([]string, error) {
	englishSentenceTokenizerOnce.Do(func() {
		b, err := sentencesdata.Asset("data/english.json")
		if err != nil {
			englishSentenceTokenizerErr = fmt.Errorf("load english punkt data: %w", err)
			return
		}
		training, err := sentences.LoadTraining(b)
		if err != nil {
			englishSentenceTokenizerErr = fmt.Errorf("parse english punkt data: %w", err)
			return
		}
		englishSentenceTokenizer = sentences.NewSentenceTokenizer(training)
	})
	if englishSentenceTokenizerErr != nil {
		return nil, englishSentenceTokenizerErr
	}
	if englishSentenceTokenizer == nil {
		return nil, fmt.Errorf("english sentence tokenizer is nil")
	}

	raw := englishSentenceTokenizer.Tokenize(text)
	out := make([]string, 0, len(raw))
	for _, sent := range raw {
		for _, s := range splitLeadingStandalonePeriodsLikeNLTK(strings.TrimSpace(sent.Text)) {
			if s == "" {
				continue
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// splitLeadingStandalonePeriodsLikeNLTK splits leading standalone periods into separate sentences.
// NLTK's PunktSentenceTokenizer treats ". ." patterns as standalone sentences in several edge cases.
func splitLeadingStandalonePeriodsLikeNLTK(s string) []string {
	if s == "" {
		return nil
	}
	out := make([]string, 0, 4)
	for {
		s = strings.TrimLeftFunc(s, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
		})
		if s == "" {
			break
		}
		if s[0] != '.' {
			break
		}
		if len(s) == 1 || (len(s) > 1 && isWhitespaceASCII(s[1])) {
			out = append(out, ".")
			s = strings.TrimLeftFunc(s[1:], func(r rune) bool {
				return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
			})
			continue
		}
		break
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// isWhitespaceASCII reports whether the byte is an ASCII whitespace character.
func isWhitespaceASCII(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}
