//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsearch

import (
	"context"
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsearch/lexical"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
)

const defaultKeywordMaxResults = 5

// KeywordOption configures NewKeyword.
type KeywordOption func(*keywordSearchOptions)

type keywordSearchOptions struct {
	maxResults int
}

// WithKeywordMaxResults limits the number of tools returned by keyword search.
// The default is 5. Non-positive values are ignored, and the last positive
// value wins.
func WithKeywordMaxResults(limit int) KeywordOption {
	return func(o *keywordSearchOptions) {
		if limit > 0 {
			o.maxResults = limit
		}
	}
}

// NewKeyword returns a local keyword search handler for request
// candidates. It searches the model-visible names, namespaces, and tool
// descriptions, and returns the original entries in relevance order. It makes
// no network or model calls. An exact name match takes precedence. Otherwise,
// the handler returns positive-scoring matches up to the result limit, which
// defaults to 5.
//
// Matching is lexical, with common English filler words ignored. An empty
// query, a nil request, or a query with no matches returns no tools. Candidates
// without a tool declaration are skipped. Nil options are ignored. The handler
// can be shared by concurrent runs and returns the context error if canceled.
func NewKeyword(opts ...KeywordOption) responses.ToolSearchHandler {
	o := keywordSearchOptions{maxResults: defaultKeywordMaxResults}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return func(ctx context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if req == nil {
			return nil, nil
		}
		query := strings.TrimSpace(req.Query)
		if query == "" {
			return nil, nil
		}
		return searchKeywordCandidates(ctx, query, req.Candidates, o.maxResults)
	}
}

func searchKeywordCandidates(
	ctx context.Context, query string, candidates []responses.ToolSearchEntry, maxResults int,
) ([]responses.ToolSearchEntry, error) {
	type document struct {
		index             int
		name, description string
		exact             bool
	}
	docs := make([]document, 0, len(candidates))
	hasExact := false
	for i, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if candidate.Tool == nil {
			continue
		}
		decl := candidate.Tool.Declaration()
		if decl == nil {
			continue
		}
		name := candidate.Name
		if name == "" {
			name = decl.Name
		}
		exact := strings.EqualFold(name, query)
		hasExact = hasExact || exact
		docs = append(docs, document{i, name, decl.Description, exact})
	}

	var prepared lexical.Query
	if !hasExact {
		query = keywordQuery(query)
		var ok bool
		prepared, ok = lexical.ParseQuery(query)
		if !ok {
			return nil, ctx.Err()
		}
	}
	namespaceTerms := keywordNamespaceTerms(query)
	type match struct{ index, score, parts int }
	var matches []match
	for _, item := range docs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if hasExact && !item.exact {
			continue
		}
		candidate := candidates[item.index]
		// Namespace names can match directly. Their descriptions only boost matches.
		doc := lexical.NewDocument(item.name, item.description+" "+candidate.Namespace)
		score := 1 // All exact matches start with the same positive score.
		if !hasExact {
			score = prepared.Score(&doc)
			if score == 0 {
				continue
			}
		}
		score += keywordNamespaceScore(candidate, namespaceTerms)
		matches = append(matches, match{item.index, score, doc.NamePartCount()})
	}

	slices.SortStableFunc(matches, func(a, b match) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.parts - b.parts
	})
	var result []responses.ToolSearchEntry
	for _, hit := range matches[:min(maxResults, len(matches))] {
		result = append(result, candidates[hit.index])
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func keywordNamespaceScore(candidate responses.ToolSearchEntry, terms []lexical.Term) int {
	if candidate.Namespace == "" || candidate.NamespaceDescription == "" {
		return 0
	}
	description := strings.ToLower(candidate.NamespaceDescription)
	score := 0
	for _, term := range terms {
		if term.Matches(description) {
			score++
		}
	}
	return score
}

func keywordNamespaceTerms(query string) []lexical.Term {
	seen := make(map[string]bool)
	var terms []lexical.Term
	for _, token := range lexical.Tokenize(query) {
		if len(token) > 1 {
			token = strings.TrimPrefix(token, "+")
		}
		if !seen[token] {
			seen[token] = true
			terms = append(terms, lexical.CompileTerm(token))
		}
	}
	return terms
}

func keywordQuery(query string) string {
	seen := make(map[string]bool)
	var words []string
	for _, word := range lexical.Tokenize(query) {
		if seen[word] || strings.Trim(word, "+_-") == "" {
			continue
		}
		// An explicit required term retains its meaning, including a filler
		// word or a compound name understood by the shared ranker.
		if !strings.HasPrefix(word, "+") && keywordFiller[word] {
			continue
		}
		seen[word] = true
		words = append(words, word)
	}
	return strings.Join(words, " ")
}

var keywordFiller = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "been": true, "being": true, "by": true,
	"can": true, "could": true, "do": true, "does": true, "for": true,
	"from": true, "get": true, "had": true, "has": true, "have": true,
	"how": true, "i": true, "in": true,
	"including": true, "into": true, "is": true, "it": true, "its": true,
	"look": true, "me": true, "my": true, "of": true, "on": true,
	"or": true, "our": true, "please": true, "return": true, "should": true,
	"show": true, "that": true, "the": true, "their": true, "them": true,
	"there": true, "these": true, "they": true, "this": true, "those": true,
	"to": true, "tool": true, "tools": true, "up": true, "us": true,
	"use": true, "used": true, "using": true, "want": true, "was": true,
	"we": true, "were": true, "what": true, "when": true, "where": true,
	"which": true, "who": true, "will": true, "with": true, "would": true,
	"you": true, "your": true,
}
