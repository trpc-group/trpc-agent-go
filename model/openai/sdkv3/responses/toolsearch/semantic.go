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
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultSemanticMinScore   = 0.4
	defaultSemanticMaxResults = 5
	semanticCacheCapacity     = 1024
)

// SemanticOption configures NewSemantic.
type SemanticOption func(*semanticOptions)

type semanticOptions struct {
	maxResults int
	minScore   float64
}

// WithSemanticMaxResults sets the maximum number of entries returned per search.
// The default is 5. Non-positive values are ignored, and the last positive
// value wins.
func WithSemanticMaxResults(limit int) SemanticOption {
	return func(o *semanticOptions) {
		if limit > 0 {
			o.maxResults = limit
		}
	}
}

// WithSemanticMinScore sets the inclusive minimum cosine similarity.
// The score must be within [-1, 1] and defaults to 0.4.
func WithSemanticMinScore(score float64) SemanticOption {
	return func(o *semanticOptions) { o.minScore = score }
}

// NewSemantic returns a client search handler that ranks the current request's
// candidates by cosine similarity to the query. It returns the original entries
// and preserves candidate order on ties. A nil request, a blank query, or a
// request without tool declarations returns no entries.
//
// The embedder must be initialized and safe for concurrent use. Each search with
// candidates embeds the query. The handler embeds candidate names, namespaces,
// descriptions, and top-level parameter information on cache misses. The
// in-memory cache is scoped to the handler, so create a new handler when the
// embedding model changes. Concurrent misses can embed the same candidate.
// The handler honors cancellation and returns embedding errors.
//
// NewSemantic returns an error for a nil embedder or an invalid minimum score.
// Options are applied in order, and nil options are ignored.
func NewSemantic(e embedder.Embedder, opts ...SemanticOption) (responses.ToolSearchHandler, error) {
	if e == nil {
		return nil, errors.New("semantic tool search: nil embedder")
	}
	options := semanticOptions{
		maxResults: defaultSemanticMaxResults,
		minScore:   defaultSemanticMinScore,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if math.IsNaN(options.minScore) || options.minScore < -1 || options.minScore > 1 {
		return nil, errors.New("semantic tool search: min score must be within [-1, 1]")
	}
	s := &semanticSearch{
		embedder: e,
		options:  options,
		vectors:  make(map[[sha256.Size]byte][]float64),
	}
	return s.search, nil
}

type semanticSearch struct {
	embedder embedder.Embedder
	options  semanticOptions

	mu      sync.Mutex
	vectors map[[sha256.Size]byte][]float64
}

func (s *semanticSearch) search(ctx context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, nil
	}
	queryText := strings.TrimSpace(req.Query)
	if queryText == "" {
		return nil, nil
	}

	type hit struct {
		index int
		score float64
	}
	var hits []hit
	var query []float64
	for i, entry := range req.Candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Tool == nil {
			continue
		}
		decl := entry.Tool.Declaration()
		if decl == nil {
			continue
		}
		if query == nil {
			var err error
			query, err = s.embed(ctx, queryText)
			if err != nil {
				return nil, err
			}
		}
		v, err := s.candidateVector(ctx, semanticText(entry, decl), len(query))
		if err != nil {
			return nil, err
		}
		score := cosineScore(query, v)
		if score >= s.options.minScore {
			hits = append(hits, hit{index: i, score: score})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return cmp.Compare(b.score, a.score) })
	var result []responses.ToolSearchEntry
	if n := min(s.options.maxResults, len(hits)); n > 0 {
		result = make([]responses.ToolSearchEntry, n)
		for i, h := range hits[:n] {
			result[i] = req.Candidates[h.index]
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func cosineScore(query, candidate []float64) float64 {
	// A vector's similarity to itself is 1 even if its normalized dot
	// product rounds slightly below 1.
	if slices.Equal(query, candidate) {
		return 1
	}
	var score float64
	for i, x := range query {
		score += x * candidate[i]
	}
	// Rounding can push a normalized dot product outside the cosine range.
	return max(-1, min(1, score))
}

func semanticText(entry responses.ToolSearchEntry, decl *tool.Declaration) string {
	name := entry.Name
	if name == "" {
		name = decl.Name
	}
	var doc strings.Builder
	doc.WriteString("Tool: ")
	doc.WriteString(name)
	if entry.Namespace != "" {
		doc.WriteString("\nNamespace: ")
		doc.WriteString(entry.Namespace)
		if entry.NamespaceDescription != "" {
			doc.WriteByte(' ')
			doc.WriteString(entry.NamespaceDescription)
		}
	}
	doc.WriteString("\nDescription: ")
	doc.WriteString(decl.Description)
	if decl.InputSchema != nil {
		params := decl.InputSchema.Properties
		keys := make([]string, 0, len(params))
		for key, param := range params {
			if param != nil {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for i, key := range keys {
			if i == 0 {
				doc.WriteString("\nParameters: ")
			} else {
				doc.WriteString(", ")
			}
			param := params[key]
			kind := strings.TrimSpace(param.Type)
			if kind == "" && param.Items != nil {
				kind = "array"
			} else if kind == "" && len(param.Properties) > 0 {
				kind = "object"
			}
			doc.WriteString(key)
			doc.WriteString(" (")
			doc.WriteString(kind)
			doc.WriteString("): ")
			doc.WriteString(strings.TrimSpace(param.Description))
		}
	}
	return doc.String()
}

func (s *semanticSearch) candidateVector(ctx context.Context, text string, dimensions int) ([]float64, error) {
	key := sha256.Sum256([]byte(text))
	s.mu.Lock()
	cached := s.vectors[key]
	s.mu.Unlock()
	if len(cached) == dimensions {
		return cached, nil
	}
	v, err := s.embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if len(v) != dimensions {
		return nil, errors.New("semantic tool search: embedding dimensions differ")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing := s.vectors[key]
	if len(existing) == dimensions {
		return existing, nil
	}
	if existing == nil && len(s.vectors) >= semanticCacheCapacity {
		for old := range s.vectors {
			delete(s.vectors, old)
			break
		}
	}
	s.vectors[key] = v
	return v, nil
}

// Normalize a private copy so the cache never retains a provider-owned buffer.
func (s *semanticSearch) embed(ctx context.Context, text string) ([]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := s.embedder.GetEmbedding(ctx, text)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, fmt.Errorf("semantic tool search: %w", err)
	}
	// Scale before squaring to avoid overflow and underflow for finite vectors.
	var scale float64
	for _, v := range raw {
		scale = max(scale, math.Abs(v))
	}
	if scale == 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return nil, errors.New("semantic tool search: invalid embedding")
	}
	out := make([]float64, len(raw))
	var sum float64
	for i, v := range raw {
		out[i] = v / scale
		sum += out[i] * out[i]
	}
	norm := math.Sqrt(sum)
	for i := range out {
		out[i] /= norm
	}
	return out, nil
}
