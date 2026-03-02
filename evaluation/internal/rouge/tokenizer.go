//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rouge

import (
	"regexp"
	"strings"
)

var (
	// nonAlphaNumRE matches one or more non-alphanumeric characters for normalization.
	nonAlphaNumRE = regexp.MustCompile(`[^a-z0-9]+`)
	// spacesRE matches one or more whitespace characters for token splitting.
	spacesRE = regexp.MustCompile(`\s+`)
	// validTokenRE matches a token consisting only of lowercase ASCII letters and digits.
	validTokenRE = regexp.MustCompile(`^[a-z0-9]+$`)
)

// Tokenizer tokenizes text into a list of tokens.
type Tokenizer interface {
	// Tokenize splits input text into tokens.
	Tokenize(text string) []string
}

// tokenizer replicates the tokenization used by google-research/rouge.
type tokenizer struct {
	// useStemmer enables Porter stemming for tokens longer than 3 characters.
	useStemmer bool
}

// newTokenizer creates a tokenizer configured with optional stemming.
func newTokenizer(useStemmer bool) *tokenizer {
	return &tokenizer{useStemmer: useStemmer}
}

// Tokenize lowercases, normalizes punctuation, splits on whitespace, and optionally stems tokens.
func (t *tokenizer) Tokenize(text string) []string {
	text = strings.ToLower(text)
	text = nonAlphaNumRE.ReplaceAllString(text, " ")

	parts := spacesRE.Split(text, -1)
	tokens := make([]string, 0, len(parts))
	for _, token := range parts {
		if token == "" || !validTokenRE.MatchString(token) {
			continue
		}
		if t.useStemmer && len(token) > 3 {
			token = stem(token)
		}
		if token == "" || !validTokenRE.MatchString(token) {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

// stem applies the NLTK_EXTENSIONS Porter stemming algorithm to an ASCII word.
func stem(word string) string {
	word = strings.ToLower(word)
	if len(word) <= 2 {
		return word
	}
	if base, ok := nltkPorterIrregularPool[word]; ok {
		return base
	}

	stemmer := nltkPorterStemmer{}
	word = stemmer.step1a(word)
	word = stemmer.step1b(word)
	word = stemmer.step1c(word)
	word = stemmer.step2(word)
	word = stemmer.step3(word)
	word = stemmer.step4(word)
	word = stemmer.step5a(word)
	word = stemmer.step5b(word)
	return word
}

// nltkPorterIrregularPool defines irregular forms used by NLTK_EXTENSIONS.
var nltkPorterIrregularPool = map[string]string{
	"sky":      "sky",
	"skies":    "sky",
	"dying":    "die",
	"lying":    "lie",
	"tying":    "tie",
	"news":     "news",
	"inning":   "inning",
	"innings":  "inning",
	"outing":   "outing",
	"outings":  "outing",
	"canning":  "canning",
	"cannings": "canning",
	"howe":     "howe",
	"proceed":  "proceed",
	"exceed":   "exceed",
	"succeed":  "succeed",
}

// nltkPorterStemmer implements the NLTK_EXTENSIONS Porter stemming rules.
type nltkPorterStemmer struct{}

// isConsonant reports whether the character at i is a consonant under the Porter rules.
func (s nltkPorterStemmer) isConsonant(word string, i int) bool {
	if i < 0 || i >= len(word) {
		return false
	}
	switch word[i] {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	case 'y':
		if i == 0 {
			return true
		}
		return !s.isConsonant(word, i-1)
	default:
		return true
	}
}

// containsVowel reports whether the string contains a vowel under the Porter rules.
func (s nltkPorterStemmer) containsVowel(stem string) bool {
	for i := 0; i < len(stem); i++ {
		if !s.isConsonant(stem, i) {
			return true
		}
	}
	return false
}

// measure computes the Porter "m" measure for the string.
func (s nltkPorterStemmer) measure(stem string) int {
	if stem == "" {
		return 0
	}
	m := 0
	prevWasVowel := false
	for i := 0; i < len(stem); i++ {
		isConsonant := s.isConsonant(stem, i)
		if isConsonant {
			if prevWasVowel {
				m++
			}
			prevWasVowel = false
			continue
		}
		prevWasVowel = true
	}
	return m
}

// hasPositiveMeasure reports whether the string has a Porter measure greater than zero.
func (s nltkPorterStemmer) hasPositiveMeasure(stem string) bool {
	return s.measure(stem) > 0
}

// endsDoubleConsonant reports whether the string ends with a double consonant.
func (s nltkPorterStemmer) endsDoubleConsonant(word string) bool {
	if len(word) < 2 {
		return false
	}
	last := word[len(word)-1]
	if last != word[len(word)-2] {
		return false
	}
	return s.isConsonant(word, len(word)-1)
}

// endsCVC reports whether the string ends with a consonant-vowel-consonant pattern.
func (s nltkPorterStemmer) endsCVC(word string) bool {
	if len(word) >= 3 {
		last := word[len(word)-1]
		if s.isConsonant(word, len(word)-3) &&
			!s.isConsonant(word, len(word)-2) &&
			s.isConsonant(word, len(word)-1) &&
			last != 'w' && last != 'x' && last != 'y' {
			return true
		}
	}
	if len(word) == 2 && !s.isConsonant(word, 0) && s.isConsonant(word, 1) {
		return true
	}
	return false
}

// replaceSuffix replaces a suffix with a replacement and returns the updated string.
func (s nltkPorterStemmer) replaceSuffix(word, suffix, replacement string) string {
	if suffix == "" {
		return word + replacement
	}
	if !strings.HasSuffix(word, suffix) {
		return word
	}
	return word[:len(word)-len(suffix)] + replacement
}

// porterRule represents a suffix replacement rule with an optional stem condition.
type porterRule struct {
	// suffix is the matched suffix.
	suffix string
	// replacement is appended after removing suffix.
	replacement string
	// condition is checked against the stem before replacement.
	condition func(stem string) bool
}

// applyRuleList applies the first matching rule and returns the transformed word.
func (s nltkPorterStemmer) applyRuleList(word string, rules []porterRule) string {
	for _, rule := range rules {
		if rule.suffix == "*d" {
			if !s.endsDoubleConsonant(word) {
				continue
			}
			stem := word[:len(word)-2]
			if rule.condition == nil || rule.condition(stem) {
				return stem + rule.replacement
			}
			return word
		}

		if !strings.HasSuffix(word, rule.suffix) {
			continue
		}
		stem := s.replaceSuffix(word, rule.suffix, "")
		if rule.condition == nil || rule.condition(stem) {
			return stem + rule.replacement
		}
		return word
	}
	return word
}

// step1a applies Porter step 1a rules.
func (s nltkPorterStemmer) step1a(word string) string {
	if strings.HasSuffix(word, "ies") && len(word) == 4 {
		return s.replaceSuffix(word, "ies", "ie")
	}
	return s.applyRuleList(word, []porterRule{
		{suffix: "sses", replacement: "ss"},
		{suffix: "ies", replacement: "i"},
		{suffix: "ss", replacement: "ss"},
		{suffix: "s", replacement: ""},
	})
}

// step1b applies Porter step 1b rules.
func (s nltkPorterStemmer) step1b(word string) string {
	if strings.HasSuffix(word, "ied") {
		if len(word) == 4 {
			return s.replaceSuffix(word, "ied", "ie")
		}
		return s.replaceSuffix(word, "ied", "i")
	}

	if strings.HasSuffix(word, "eed") {
		stem := s.replaceSuffix(word, "eed", "")
		if s.measure(stem) > 0 {
			return stem + "ee"
		}
		return word
	}

	rule2Or3Succeeded := false
	intermediateStem := ""
	for _, suffix := range []string{"ed", "ing"} {
		if !strings.HasSuffix(word, suffix) {
			continue
		}
		candidateStem := s.replaceSuffix(word, suffix, "")
		if s.containsVowel(candidateStem) {
			intermediateStem = candidateStem
			rule2Or3Succeeded = true
			break
		}
	}
	if !rule2Or3Succeeded {
		return word
	}

	last := intermediateStem[len(intermediateStem)-1:]
	return s.applyRuleList(intermediateStem, []porterRule{
		{suffix: "at", replacement: "ate"},
		{suffix: "bl", replacement: "ble"},
		{suffix: "iz", replacement: "ize"},
		{
			suffix:      "*d",
			replacement: last,
			condition: func(stem string) bool {
				ch := intermediateStem[len(intermediateStem)-1]
				return ch != 'l' && ch != 's' && ch != 'z'
			},
		},
		{
			suffix:      "",
			replacement: "e",
			condition: func(stem string) bool {
				return s.measure(stem) == 1 && s.endsCVC(stem)
			},
		},
	})
}

// step1c applies Porter step 1c rules.
func (s nltkPorterStemmer) step1c(word string) string {
	return s.applyRuleList(word, []porterRule{
		{
			suffix:      "y",
			replacement: "i",
			condition: func(stem string) bool {
				return len(stem) > 1 && s.isConsonant(stem, len(stem)-1)
			},
		},
	})
}

// step2 applies Porter step 2 rules.
func (s nltkPorterStemmer) step2(word string) string {
	if strings.HasSuffix(word, "alli") && s.hasPositiveMeasure(s.replaceSuffix(word, "alli", "")) {
		return s.step2(s.replaceSuffix(word, "alli", "al"))
	}

	hasPositive := func(stem string) bool { return s.hasPositiveMeasure(stem) }
	rules := []porterRule{
		{suffix: "ational", replacement: "ate", condition: hasPositive},
		{suffix: "tional", replacement: "tion", condition: hasPositive},
		{suffix: "enci", replacement: "ence", condition: hasPositive},
		{suffix: "anci", replacement: "ance", condition: hasPositive},
		{suffix: "izer", replacement: "ize", condition: hasPositive},
		{suffix: "bli", replacement: "ble", condition: hasPositive},
		{suffix: "alli", replacement: "al", condition: hasPositive},
		{suffix: "entli", replacement: "ent", condition: hasPositive},
		{suffix: "eli", replacement: "e", condition: hasPositive},
		{suffix: "ousli", replacement: "ous", condition: hasPositive},
		{suffix: "ization", replacement: "ize", condition: hasPositive},
		{suffix: "ation", replacement: "ate", condition: hasPositive},
		{suffix: "ator", replacement: "ate", condition: hasPositive},
		{suffix: "alism", replacement: "al", condition: hasPositive},
		{suffix: "iveness", replacement: "ive", condition: hasPositive},
		{suffix: "fulness", replacement: "ful", condition: hasPositive},
		{suffix: "ousness", replacement: "ous", condition: hasPositive},
		{suffix: "aliti", replacement: "al", condition: hasPositive},
		{suffix: "iviti", replacement: "ive", condition: hasPositive},
		{suffix: "biliti", replacement: "ble", condition: hasPositive},
		{suffix: "fulli", replacement: "ful", condition: hasPositive},
		{
			suffix:      "logi",
			replacement: "log",
			condition: func(stem string) bool {
				return s.hasPositiveMeasure(word[:len(word)-3])
			},
		},
	}
	return s.applyRuleList(word, rules)
}

// step3 applies Porter step 3 rules.
func (s nltkPorterStemmer) step3(word string) string {
	hasPositive := func(stem string) bool { return s.hasPositiveMeasure(stem) }
	return s.applyRuleList(word, []porterRule{
		{suffix: "icate", replacement: "ic", condition: hasPositive},
		{suffix: "ative", replacement: "", condition: hasPositive},
		{suffix: "alize", replacement: "al", condition: hasPositive},
		{suffix: "iciti", replacement: "ic", condition: hasPositive},
		{suffix: "ical", replacement: "ic", condition: hasPositive},
		{suffix: "ful", replacement: "", condition: hasPositive},
		{suffix: "ness", replacement: "", condition: hasPositive},
	})
}

// step4 applies Porter step 4 rules.
func (s nltkPorterStemmer) step4(word string) string {
	measureGT1 := func(stem string) bool { return s.measure(stem) > 1 }
	return s.applyRuleList(word, []porterRule{
		{suffix: "al", replacement: "", condition: measureGT1},
		{suffix: "ance", replacement: "", condition: measureGT1},
		{suffix: "ence", replacement: "", condition: measureGT1},
		{suffix: "er", replacement: "", condition: measureGT1},
		{suffix: "ic", replacement: "", condition: measureGT1},
		{suffix: "able", replacement: "", condition: measureGT1},
		{suffix: "ible", replacement: "", condition: measureGT1},
		{suffix: "ant", replacement: "", condition: measureGT1},
		{suffix: "ement", replacement: "", condition: measureGT1},
		{suffix: "ment", replacement: "", condition: measureGT1},
		{suffix: "ent", replacement: "", condition: measureGT1},
		{
			suffix:      "ion",
			replacement: "",
			condition: func(stem string) bool {
				return s.measure(stem) > 1 && len(stem) > 0 && (stem[len(stem)-1] == 's' || stem[len(stem)-1] == 't')
			},
		},
		{suffix: "ou", replacement: "", condition: measureGT1},
		{suffix: "ism", replacement: "", condition: measureGT1},
		{suffix: "ate", replacement: "", condition: measureGT1},
		{suffix: "iti", replacement: "", condition: measureGT1},
		{suffix: "ous", replacement: "", condition: measureGT1},
		{suffix: "ive", replacement: "", condition: measureGT1},
		{suffix: "ize", replacement: "", condition: measureGT1},
	})
}

// step5a applies Porter step 5a rules.
func (s nltkPorterStemmer) step5a(word string) string {
	if strings.HasSuffix(word, "e") {
		stem := s.replaceSuffix(word, "e", "")
		m := s.measure(stem)
		if m > 1 {
			return stem
		}
		if m == 1 && !s.endsCVC(stem) {
			return stem
		}
	}
	return word
}

// step5b applies Porter step 5b rules.
func (s nltkPorterStemmer) step5b(word string) string {
	return s.applyRuleList(word, []porterRule{
		{
			suffix:      "ll",
			replacement: "l",
			condition: func(stem string) bool {
				return s.measure(word[:len(word)-1]) > 1
			},
		},
	})
}
