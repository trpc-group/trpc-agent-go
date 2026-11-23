//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"context"
	"strings"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsearch"
)

const defaultKeywordMaxResults = 5

// KeywordSearchOption configures NewKeywordToolSearch.
type KeywordSearchOption func(*keywordSearchOptions)

type keywordSearchOptions struct {
	maxResults int
}

// WithKeywordMaxResults limits the number of tools returned by keyword search.
// Non-positive values use the default of 5. The last option wins.
func WithKeywordMaxResults(limit int) KeywordSearchOption {
	return func(o *keywordSearchOptions) {
		o.maxResults = limit
		if limit <= 0 {
			o.maxResults = defaultKeywordMaxResults
		}
	}
}

// NewKeywordToolSearch returns a local keyword search handler for request
// candidates. It searches the model-visible names, namespaces, and tool
// descriptions, and returns the original entries in relevance order. It makes
// no network or model calls. The default result limit is 5.
//
// Matching is lexical, with common English filler words ignored. An empty
// query, a nil request, or a query with no matches returns no tools. Candidates
// without a tool declaration are skipped. Nil options are ignored. The handler
// can be shared by concurrent runs and returns the context error if canceled.
func NewKeywordToolSearch(opts ...KeywordSearchOption) ToolSearchHandler {
	o := keywordSearchOptions{maxResults: defaultKeywordMaxResults}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return func(ctx context.Context, req *ToolSearchRequest) ([]ToolSearchEntry, error) {
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
		docs := make([]toolsearch.Document, len(req.Candidates))
		exact := false
		for i, candidate := range req.Candidates {
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
			exact = exact || strings.EqualFold(name, query)
			// The namespace is searchable even when the declaration has a
			// different local execution name. Its description remains a bias.
			docs[i] = toolsearch.NewDocument(name,
				decl.Description+" "+candidate.Namespace,
				candidate.Namespace, candidate.NamespaceDescription)
		}
		if !exact {
			query = keywordQuery(query)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if query == "" {
			return nil, nil
		}
		matches := toolsearch.Rank([]string{query}, docs)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		best := 0
		for _, match := range matches {
			best = max(best, keywordEvidence(match))
		}
		margin := max(1, best/3)
		var result []ToolSearchEntry
		for _, match := range matches {
			if best-keywordEvidence(match) > margin {
				continue
			}
			result = append(result, req.Candidates[match.Index])
			if len(result) == o.maxResults {
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result, nil
	}
}

// Compare independent lexical evidence rather than the ranker's larger name
// bonus: a description-only second capability can still be useful in a query
// that asks for several tools. Names receive one additional vote.
func keywordEvidence(match toolsearch.Match) int {
	return match.MatchedTerms + match.NameMatches
}

func keywordQuery(query string) string {
	seen := make(map[string]bool)
	var words []string
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '-' && r != '+'
	}) {
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
