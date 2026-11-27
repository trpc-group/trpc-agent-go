//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package lexical prepares tool metadata and scores individual keyword matches.
package lexical

import (
	"regexp"
	"strings"
)

// Document holds the searchable name and description of one tool.
// It is immutable and can be reused by concurrent searches. Its zero value
// represents an empty name and description.
type Document struct {
	parts     []string
	fullName  string
	nameLower string
	descLower string
}

// NewDocument prepares the name and description of one tool for keyword search.
// The name is split on underscores, whitespace, and lowercase-to-uppercase
// ASCII transitions.
func NewDocument(name, description string) Document {
	var expanded strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' && name[i-1] >= 'a' && name[i-1] <= 'z' {
			expanded.WriteByte(' ')
		}
		if r == '_' {
			r = ' '
		}
		expanded.WriteRune(r)
	}
	parts := strings.Fields(strings.ToLower(expanded.String()))
	return Document{
		parts:     parts,
		fullName:  strings.Join(parts, " "),
		nameLower: strings.ToLower(name),
		descLower: strings.ToLower(description),
	}
}

// NamePartCount returns the number of parsed name parts.
func (d Document) NamePartCount() int { return len(d.parts) }

// Tokenize splits a raw query into lowercase tokens, preserving underscores,
// hyphens, and plus signs alongside Unicode letters and numbers.
func Tokenize(query string) []string {
	parts := querySeparators.Split(strings.ToLower(query), -1)
	tokens := parts[:0]
	for _, part := range parts {
		if part != "" {
			tokens = append(tokens, part)
		}
	}
	return tokens
}

var querySeparators = regexp.MustCompile(`[^\p{L}\p{N}_\-+]+`)

// Term is an immutable text matcher that can be reused concurrently. A zero
// Term matches nothing.
type Term struct {
	text     string
	pattern  *regexp.Regexp
	compound bool
}

// CompileTerm prepares one normalized token for text matching. The term must
// already be lowercase. ASCII terms use word boundaries and non-ASCII terms
// use substring matching.
func CompileTerm(term string) Term {
	compiled := Term{text: term, compound: strings.ContainsAny(term, "_-")}
	for i := 0; i < len(term); i++ {
		if term[i] >= 0x80 {
			return compiled
		}
	}
	if term != "" {
		compiled.pattern, _ = regexp.Compile(`(?i)\b` + regexp.QuoteMeta(term) + `\b`)
	}
	return compiled
}

// Matches reports whether the term matches lowercase text.
func (t Term) Matches(text string) bool {
	if t.pattern != nil {
		return t.pattern.MatchString(text)
	}
	return t.text != "" && strings.Contains(text, t.text)
}

// Query holds the compiled terms for a single keyword query. It is immutable
// and can score multiple documents concurrently. A zero Query scores zero.
type Query struct {
	terms    []Term
	required int
}

// ParseQuery prepares one query for scoring. It returns false when the input
// has no tokens. A leading '+' requires every part of that token, splitting
// compound required terms on underscores and hyphens.
func ParseQuery(raw string) (Query, bool) {
	tokens := Tokenize(raw)
	if len(tokens) == 0 {
		return Query{}, false
	}

	terms := make([]Term, 0, len(tokens))
	compiled := make(map[string]Term)
	add := func(text string) {
		term, ok := compiled[text]
		if !ok {
			term = CompileTerm(text)
			compiled[text] = term
		}
		terms = append(terms, term)
	}

	optional := tokens[:0]
	for _, token := range tokens {
		if len(token) > 1 && token[0] == '+' {
			for _, part := range splitRequiredTerm(token[1:]) {
				add(part)
			}
		} else {
			optional = append(optional, token)
		}
	}
	required := len(terms)
	for _, token := range optional {
		add(token)
	}
	return Query{terms: terms, required: required}, true
}

func splitRequiredTerm(term string) []string {
	if strings.ContainsAny(term, "_-") {
		parts := strings.FieldsFunc(term, func(r rune) bool {
			return r == '_' || r == '-'
		})
		if len(parts) != 0 {
			return parts
		}
	}
	return []string{term}
}

// Score returns the relevance of a document to the query. Every required term
// must match a name part or the description. Name part and compound name
// matches each contribute 10 points per occurrence, and description matches
// contribute 3. The space-joined name is checked for 3 points per term only
// when no term has scored. A nil document scores zero.
func (q Query) Score(doc *Document) int {
	if doc == nil {
		return 0
	}
	score := 0
	for i, term := range q.terms {
		termScore := 0
		for _, part := range doc.parts {
			if part == term.text {
				termScore += 10
				break
			}
		}
		if doc.descLower != "" && term.Matches(doc.descLower) {
			termScore += 3
		}
		if i < q.required && termScore == 0 {
			return 0
		}
		if term.compound && strings.Contains(doc.nameLower, term.text) {
			termScore += 10
		}
		score += termScore
	}
	if score == 0 {
		for _, term := range q.terms {
			if term.Matches(doc.fullName) {
				score += 3
			}
		}
	}
	return score
}
