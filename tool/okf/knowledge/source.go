//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package okfknowledge adapts an okf.Store into the knowledge module as a
// semantic *locator*: it indexes one non-chunked Document per concept, using
// the curated title/description/tags as the embedding text and stashing the
// frontmatter in metadata for filtering. It deliberately does NOT store the
// body for retrieval — the agent reads full content via the okf_read tool.
//
// The intended split of labor:
//
//	knowledge (this source) : fuzzy query  -> relevant concept ids   (locator)
//	okf tools   (okf_read)  : concept id   -> full body + links      (content)
//
// so retrieval augments navigation instead of shredding OKF structure. The
// dependency edge is one-way (this package -> tool/okf + knowledge/*); the
// knowledge core never imports tool/*.
package okfknowledge

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	ksource "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

// Metadata keys carried on every indexed concept Document. Filter on them with
// ScopeFilter or knowledge/tool.WithConditionedFilter.
const (
	MetaConcept  = "okf_concept"  // Concept id (also mirrored to source.MetaURI).
	MetaType     = "okf_type"     // Frontmatter type.
	MetaTitle    = "okf_title"    // Frontmatter title.
	MetaResource = "okf_resource" // Frontmatter resource URI (provenance).
	MetaTags     = "okf_tags"     // Frontmatter tags.
)

// Source adapts an okf.Store into a knowledge source.Source.
type Source struct {
	store    okf.Store
	name     string
	metadata map[string]any
}

var _ ksource.Source = (*Source)(nil)

// Option configures a Source.
type Option func(*Source)

// WithName sets the source name (default "okf").
func WithName(name string) Option { return func(s *Source) { s.name = name } }

// WithMetadata attaches static metadata to every indexed concept.
func WithMetadata(m map[string]any) Option {
	return func(s *Source) {
		for k, v := range m {
			s.metadata[k] = v
		}
	}
}

// New builds a knowledge source over store.
func New(store okf.Store, opts ...Option) *Source {
	s := &Source{store: store, name: "okf", metadata: map[string]any{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Name implements source.Source.
func (s *Source) Name() string { return s.name }

// Type implements source.Source.
func (s *Source) Type() string { return "okf" }

// GetMetadata implements source.Source.
func (s *Source) GetMetadata() map[string]any { return s.metadata }

// ReadDocuments walks the bundle and emits one non-chunked Document per concept.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	ids, err := s.walk(ctx, "")
	if err != nil {
		return nil, err
	}
	docs := make([]*document.Document, 0, len(ids))
	for _, id := range ids {
		c, err := s.store.Read(ctx, id)
		if err != nil {
			continue // tolerate an unreadable concept.
		}
		docs = append(docs, s.toDocument(c))
	}
	return docs, nil
}

// walk collects every concept id under dir, depth-first.
func (s *Source) walk(ctx context.Context, dir string) ([]string, error) {
	listing, err := s.store.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range listing.Concepts {
		ids = append(ids, c.ID)
	}
	for _, sub := range listing.Subdirs {
		child := sub
		if dir != "" {
			child = dir + "/" + sub
		}
		more, err := s.walk(ctx, child)
		if err != nil {
			continue
		}
		ids = append(ids, more...)
	}
	return ids, nil
}

func (s *Source) toDocument(c okf.Concept) *document.Document {
	fm := c.Frontmatter
	md := map[string]any{
		MetaConcept:            c.ID,
		MetaType:               fm.Type,
		MetaTitle:              fm.Title,
		MetaResource:           fm.Resource,
		ksource.MetaURI:        c.ID, // stable identity: Document.ID is recomputed by Load.
		ksource.MetaSource:     s.Type(),
		ksource.MetaSourceName: s.name,
	}
	if len(fm.Tags) > 0 {
		md[MetaTags] = fm.Tags
	}
	for k, v := range s.metadata {
		md[k] = v
	}
	return &document.Document{
		ID:            c.ID,
		Name:          fm.Title,
		Content:       fm.Description, // summary/pointer; full body served by okf_read.
		EmbeddingText: embeddingText(fm),
		Metadata:      md,
	}
}

// embeddingText is the curated retrieval signal: title + description + tags.
func embeddingText(fm okf.Frontmatter) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{fm.Title, fm.Description} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(fm.Tags) > 0 {
		parts = append(parts, strings.Join(fm.Tags, " "))
	}
	return strings.Join(parts, "\n")
}

// ScopeFilter builds a filter for knowledge/tool.WithConditionedFilter, scoping
// retrieval to a frontmatter type and/or tags — e.g. a support agent limited to
// type "Runbook".
//
// Caveat: tag matching on the list-valued okf_tags metadata is Equal-based here;
// whether that means "array contains" depends on the target vector store's
// condition converter. If your store lacks array-contains semantics, index a
// flattened tag string instead and switch to searchfilter.Like.
func ScopeFilter(conceptType string, tags ...string) *searchfilter.UniversalFilterCondition {
	var conds []*searchfilter.UniversalFilterCondition
	if conceptType != "" {
		conds = append(conds, searchfilter.Equal(ksource.MetadataFieldPrefix+MetaType, conceptType))
	}
	for _, tag := range tags {
		conds = append(conds, searchfilter.Equal(ksource.MetadataFieldPrefix+MetaTags, tag))
	}
	switch len(conds) {
	case 0:
		return nil
	case 1:
		return conds[0]
	default:
		return searchfilter.And(conds...)
	}
}

// Upgrade path (not implemented): to add graph-aware retrieval, Source can also
// implement source.GraphSource by walking concept Links into a graph.Data, then
// register a graphstore so BuiltinKnowledge can do neighbor expansion. Worth it
// only once cross-links are dense and cross-concept reasoning is needed.
