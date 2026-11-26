//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolsearch provides deterministic lexical ranking over tool metadata.
package toolsearch

import (
	"regexp"
	"sort"
	"strings"
)

const exactNameScore = 1000

// Document contains precomputed searchable metadata for one candidate. Its zero
// value has no searchable content. Documents are immutable and may be reused by
// concurrent searches.
type Document struct {
	parts              []string
	full               string
	nameLower          string
	descLower          string
	namespace          string
	namespaceDescLower string
}

// NewDocument prepares a candidate for lexical ranking. Namespace descriptions
// add a small bias to existing matches; a namespace name alone does not make a
// candidate match. An empty namespace disables the bias.
func NewDocument(name, description, namespace, namespaceDescription string) Document {
	parsed := parseToolName(name)
	return Document{
		parts:              parsed.Parts,
		full:               parsed.Full,
		nameLower:          strings.ToLower(name),
		descLower:          strings.ToLower(description),
		namespace:          namespace,
		namespaceDescLower: strings.ToLower(namespaceDescription),
	}
}

// Match identifies a matching candidate by its original position in the input.
// Score includes lexical relevance and any namespace-description bias.
type Match struct {
	Index int
	Score int
	// MatchedTerms counts distinct terms from the best query matching the name
	// or description.
	MatchedTerms int
	// NameMatches counts those distinct terms that matched the name.
	NameMatches int
}

// Rank scores queries independently and keeps each candidate's highest score.
// A whole-query, case-insensitive name match scores 1000 and excludes other
// names for that query. Other queries use name parts, descriptions, and optional
// '+' required terms. Only positive matches are returned, ordered by descending
// score, ascending name-part count, and original candidate index. MatchedTerms
// and NameMatches describe the first query attaining that candidate's highest
// lexical score; an exact whole-name query contributes one to both counts.
// Namespace bias does not contribute to either count.
// Rank never modifies the queries or candidates and returns nil for no matches.
func Rank(queries []string, candidates []Document) []Match {
	if len(queries) == 0 || len(candidates) == 0 {
		return nil
	}
	scorer := prepareScorer(queries, func(lower string) (string, bool) {
		for i := range candidates {
			if candidates[i].nameLower == lower {
				return lower, true
			}
		}
		return "", false
	})
	matches := make([]Match, 0, len(candidates))
	for i := range candidates {
		doc := &candidates[i]
		score := scorer.best(doc.nameLower, doc)
		if score.score > 0 {
			matches = append(matches, Match{
				Index: i, Score: score.score,
				MatchedTerms: score.terms, NameMatches: score.names,
			})
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		da, db := &candidates[a.Index], &candidates[b.Index]
		if len(da.parts) != len(db.parts) {
			return len(da.parts) < len(db.parts)
		}
		return a.Index < b.Index
	})
	return matches
}

// RankNames ranks a named catalog by score, then by ascending name-part count
// and name. canonical maps lowercase names to the catalog's chosen spelling.
// lookup returns a borrowed document or nil for an ineligible name. A canonical
// name outside names or excluded by lookup falls back to lexical matching.
// Inputs must remain stable during the call. No matches yields nil.
func RankNames(
	queries []string, names map[string]struct{}, canonical map[string]string,
	lookup func(string) *Document,
) []string {
	if len(queries) == 0 || len(names) == 0 {
		return nil
	}
	resolveExact := func(lower string) (string, bool) {
		name, ok := canonical[lower]
		_, inScope := names[name]
		return name, ok && inScope && lookup(name) != nil
	}
	// A single exact hit needs no lexical scoring or catalog traversal.
	if len(queries) == 1 {
		lower := strings.ToLower(strings.TrimSpace(queries[0]))
		if lower != "" {
			if name, ok := resolveExact(lower); ok {
				return []string{name}
			}
		}
	}
	scorer := prepareScorer(queries, resolveExact)
	// Names with the same score and part count share a rank. Sorting the
	// names in each group avoids storing those two values for every hit.
	type rank struct{ score, parts int }
	groups := make(map[rank][]string)
	var ranks []rank
	total := 0
	for name := range names {
		doc := lookup(name)
		if doc == nil {
			continue
		}
		score := scorer.best(name, doc).score
		if score == 0 {
			continue
		}
		key := rank{score: score, parts: len(doc.parts)}
		group := groups[key]
		if len(group) == 0 {
			ranks = append(ranks, key)
		}
		groups[key] = append(group, name)
		total++
	}
	if len(ranks) == 0 {
		return nil
	}
	if len(ranks) == 1 {
		group := groups[ranks[0]]
		sort.Strings(group)
		return group
	}
	sort.Slice(ranks, func(i, j int) bool {
		if ranks[i].score != ranks[j].score {
			return ranks[i].score > ranks[j].score
		}
		return ranks[i].parts < ranks[j].parts
	})
	ranked := make([]string, 0, total)
	for _, key := range ranks {
		group := groups[key]
		sort.Strings(group)
		ranked = append(ranked, group...)
	}
	return ranked
}

type preparedQuery struct {
	exact    string
	required []string
	terms    []string
	patterns map[string]*regexp.Regexp
}

type queryScorer struct {
	queries      []preparedQuery
	raw          []string
	bias         map[string]int
	biasTerms    []string
	biasPatterns map[string]*regexp.Regexp
}

func prepareScorer(raw []string, resolveExact func(string) (string, bool)) queryScorer {
	scorer := queryScorer{raw: raw}
	for _, query := range raw {
		lower := strings.ToLower(strings.TrimSpace(query))
		if lower == "" {
			continue
		}
		if name, ok := resolveExact(lower); ok {
			scorer.queries = append(scorer.queries, preparedQuery{exact: name})
			continue
		}
		required, optional := parseQueryTerms(tokenizeQuery(lower))
		// Required compound terms match their individual parts. Split once
		// per query so walking a large catalog does not allocate per document.
		var parts []string
		for _, term := range required {
			parts = append(parts, splitRequiredTerm(term)...)
		}
		terms := optional
		if len(parts) > 0 {
			terms = append(parts, optional...)
		}
		scorer.queries = append(scorer.queries, preparedQuery{
			required: parts, terms: terms, patterns: compileTermPatterns(terms),
		})
	}
	return scorer
}

func (s *queryScorer) best(name string, doc *Document) queryScore {
	var best queryScore
	for i := range s.queries {
		q := &s.queries[i]
		var score queryScore
		if q.exact != "" {
			if q.exact == name {
				score = queryScore{score: exactNameScore, terms: 1, names: 1}
			}
		} else if matchesAllRequired(doc, q.required, q.patterns) {
			score = scoreToolForQuery(doc, q.terms, q.patterns)
		}
		if score.score > best.score {
			best = score
		}
	}
	if best.score > 0 && doc.namespace != "" && doc.namespaceDescLower != "" {
		best.score += s.namespaceBias(doc.namespaceDescLower)
	}
	return best
}

func (s *queryScorer) namespaceBias(description string) int {
	if s.bias == nil {
		s.biasTerms = namespaceTerms(s.raw)
		s.biasPatterns = compileTermPatterns(s.biasTerms)
		s.bias = make(map[string]int)
	}
	if bias, ok := s.bias[description]; ok {
		return bias
	}
	var bias int
	for _, term := range s.biasTerms {
		if matchTermInText(term, description, s.biasPatterns) {
			bias++
		}
	}
	s.bias[description] = bias
	return bias
}

// namespaceTerms keeps distinct query tokens, including required terms without
// their '+' prefix. Compound required terms stay intact for namespace bias.
func namespaceTerms(queries []string) []string {
	seen := make(map[string]struct{})
	var terms []string
	for _, query := range queries {
		for _, term := range tokenizeQuery(query) {
			if strings.HasPrefix(term, "+") && len(term) > 1 {
				term = term[1:]
			}
			if _, ok := seen[term]; ok {
				continue
			}
			seen[term] = struct{}{}
			terms = append(terms, term)
		}
	}
	return terms
}

// parsedName holds the result of parsing a tool name into searchable parts.
type parsedName struct {
	Parts []string // lowercase name parts
	Full  string   // full name joined by spaces (lowercase)
}

// parseToolName parses a tool name into searchable parts. It handles both MCP
// tools (mcp__server__action) and regular tools (CamelCase + underscores).
func parseToolName(name string) parsedName {
	if name == "" {
		return parsedName{Parts: []string{}, Full: ""}
	}

	// Split by CamelCase and underscores: insert a space before an uppercase
	// letter that follows a lowercase letter, then replace underscores.
	var sb strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := rune(name[i-1])
			if prev >= 'a' && prev <= 'z' {
				sb.WriteRune(' ')
			}
		}
		sb.WriteRune(r)
	}
	expanded := strings.ToLower(strings.ReplaceAll(sb.String(), "_", " "))

	parts := make([]string, 0)
	for _, p := range strings.Fields(expanded) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parsedName{Parts: parts, Full: strings.Join(parts, " ")}
}

// tokenizeQueryPattern treats every character that is NOT a Unicode letter or
// digit, underscore, hyphen, or plus as a separator. This naturally handles
// spaces, commas (English and Chinese), semicolons, pipes, and other
// punctuation.
var tokenizeQueryPattern = regexp.MustCompile(`[^\p{L}\p{N}_\-+]+`)

// tokenizeQuery splits a query into lowercase terms.
func tokenizeQuery(query string) []string {
	parts := tokenizeQueryPattern.Split(strings.ToLower(query), -1)
	result := parts[:0]
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseQueryTerms splits tokens into required terms (with a "+" prefix) and
// optional terms.
func parseQueryTerms(terms []string) (required, optional []string) {
	for _, term := range terms {
		if strings.HasPrefix(term, "+") && len(term) > 1 {
			required = append(required, term[1:])
		} else {
			optional = append(optional, term)
		}
	}
	return
}

// splitRequiredTerm splits a required term into atomic sub-terms on `_`/`-`.
func splitRequiredTerm(termLower string) []string {
	if !strings.ContainsAny(termLower, "_-") {
		return []string{termLower}
	}
	parts := strings.FieldsFunc(termLower, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) == 0 {
		return []string{termLower}
	}
	return parts
}

// compileTermPatterns pre-compiles `\b` word-boundary regexes for search terms.
// Non-ASCII terms (e.g. CJK) are mapped to nil → matchTermInText falls back to
// strings.Contains since Go's `\b` only fires at ASCII word boundaries.
func compileTermPatterns(terms []string) map[string]*regexp.Regexp {
	patterns := make(map[string]*regexp.Regexp, len(terms))
	for _, term := range terms {
		termLower := strings.ToLower(term)
		if _, exists := patterns[termLower]; exists {
			continue
		}
		if !isASCII(termLower) {
			patterns[termLower] = nil
			continue
		}
		pattern, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(termLower) + `\b`)
		if err != nil {
			patterns[termLower] = nil
			continue
		}
		patterns[termLower] = pattern
	}
	return patterns
}

// isASCII reports whether s contains only ASCII bytes (used to decide `\b` vs substring matching).
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return s != ""
}

// matchTermInText matches a term against text using a word-boundary regex,
// falling back to a substring check for non-ASCII terms.
func matchTermInText(termLower, textLower string, patterns map[string]*regexp.Regexp) bool {
	if pattern, ok := patterns[termLower]; ok && pattern != nil {
		return pattern.MatchString(textLower)
	}
	return strings.Contains(textLower, termLower)
}

// matchesAllRequired checks the atomic required terms prepared for the query.
func matchesAllRequired(meta *Document, requiredTerms []string, patterns map[string]*regexp.Regexp) bool {
	for _, term := range requiredTerms {
		if !nameOrDescHasTerm(meta, term, patterns) {
			return false
		}
	}
	return true
}

// nameOrDescHasTerm checks whether a single atomic term appears in the tool's
// name parts (exact match) or description (word-boundary / substring).
func nameOrDescHasTerm(meta *Document, termLower string, patterns map[string]*regexp.Regexp) bool {
	for _, part := range meta.parts {
		if part == termLower {
			return true
		}
	}
	return matchTermInText(termLower, meta.descLower, patterns)
}

type queryScore struct {
	score int
	terms int
	names int
}

// scoreToolForQuery computes a tool's relevance score for a set of query terms.
//
// Scoring (accumulated per term):
//   - compound term (contains `_`/`-`) substring-matches the tool name:  +10
//   - tool name part exact match:                                        +10
//   - description word-boundary match:                                    +3
//   - full-name substring match (fallback, only when total is still 0):   +3
func scoreToolForQuery(meta *Document, terms []string, patterns map[string]*regexp.Regexp) queryScore {
	var result queryScore
	totalScore := 0
	toolName := meta.nameLower
	for i, term := range terms {
		termLower := strings.ToLower(term)
		termScore := 0
		nameMatched := false

		if strings.ContainsAny(termLower, "_-") && strings.Contains(toolName, termLower) {
			termScore += 10
			nameMatched = true
		}

		for _, part := range meta.parts {
			if part == termLower {
				termScore += 10
				nameMatched = true
				break
			}
		}

		if meta.descLower != "" && matchTermInText(termLower, meta.descLower, patterns) {
			termScore += 3
		}

		if termScore > 0 && firstTermOccurrence(terms, i, termLower) {
			result.terms++
			if nameMatched {
				result.names++
			}
		}
		totalScore += termScore
	}

	// Full-name fallback fires only when no term scored above.
	if totalScore == 0 {
		for i, term := range terms {
			lower := strings.ToLower(term)
			if matchTermInText(lower, meta.full, patterns) {
				totalScore += 3
				if firstTermOccurrence(terms, i, lower) {
					result.terms++
					result.names++
				}
			}
		}
	}
	result.score = totalScore
	return result
}

func firstTermOccurrence(terms []string, index int, lower string) bool {
	for _, term := range terms[:index] {
		if strings.EqualFold(term, lower) {
			return false
		}
	}
	return true
}
