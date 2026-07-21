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
//
// Wire the agent so the model knows retrieval returns pointers, not content —
// each result's Content and Metadata["okf_concept"] carry the concept id. A
// system-prompt snippet that closes the loop:
//
//	Knowledge search over the OKF bundle returns concept summaries and ids, not
//	full content. To read a concept in full, call okf_read with the concept id
//	from a search result, then follow its links.
package okfknowledge

import (
	"context"
	"encoding/hex"
	"fmt"
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

	// metaTagPrefix identifies generated boolean fields used by ScopeFilter.
	// MetaTags stays in its natural []string form for metadata consumers.
	metaTagPrefix = "okf_tag_"
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
			switch k {
			case MetaConcept, MetaType, MetaTitle, MetaResource, MetaTags,
				ksource.MetaURI, ksource.MetaSource, ksource.MetaSourceName,
				ksource.MetaChunkIndex:
				// Identity and OKF-derived metadata are owned by Source.
				continue
			}
			if strings.HasPrefix(k, metaTagPrefix) {
				continue
			}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := s.walk(ctx, "")
	if err != nil {
		return nil, err
	}
	docs := make([]*document.Document, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c, err := s.store.Read(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read OKF concept %q: %w", id, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		docs = append(docs, s.toDocument(c))
	}
	return docs, nil
}

// walk collects every concept id under dir, depth-first.
func (s *Source) walk(ctx context.Context, dir string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	listing, err := s.store.List(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("list OKF directory %q: %w", dir, err)
	}
	if err := ctx.Err(); err != nil {
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
			return nil, err
		}
		ids = append(ids, more...)
	}
	return ids, nil
}

func (s *Source) toDocument(c okf.Concept) *document.Document {
	fm := c.Frontmatter
	md := make(map[string]any, len(s.metadata)+8)
	for k, v := range s.metadata {
		md[k] = v
	}
	md[MetaConcept] = c.ID
	md[MetaType] = fm.Type
	md[MetaTitle] = fm.Title
	md[MetaResource] = fm.Resource
	md[ksource.MetaURI] = c.ID
	md[ksource.MetaSource] = s.Type()
	md[ksource.MetaSourceName] = s.name
	md[ksource.MetaChunkIndex] = 0
	if len(fm.Tags) > 0 {
		md[MetaTags] = append([]string(nil), fm.Tags...)
		for _, tag := range fm.Tags {
			md[tagFilterKey(tag)] = true
		}
	}
	// Content leads with the concept id (not just the description) so the model
	// always sees an id it can pass to okf_read, even if the retrieval tool does
	// not surface metadata. Full body is still served only by okf_read.
	content := c.ID
	if fm.Description != "" {
		content += " — " + fm.Description
	}
	return &document.Document{
		ID:            fmt.Sprintf("%d:%s:%s", len(s.name), s.name, c.ID),
		Name:          fm.Title,
		Content:       content,
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
// Each tag also gets a generated boolean metadata field. Equality on a scalar
// avoids relying on backend-specific array-contains or LIKE semantics.
func ScopeFilter(conceptType string, tags ...string) *searchfilter.UniversalFilterCondition {
	var conds []*searchfilter.UniversalFilterCondition
	if conceptType != "" {
		conds = append(conds, searchfilter.Equal(ksource.MetadataFieldPrefix+MetaType, conceptType))
	}
	for _, tag := range tags {
		conds = append(conds, searchfilter.Equal(
			ksource.MetadataFieldPrefix+tagFilterKey(tag), true))
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

func tagFilterKey(tag string) string {
	return metaTagPrefix + hex.EncodeToString([]byte(tag))
}

// Upgrade path (not implemented): to add graph-aware retrieval, Source can also
// implement source.GraphSource by walking concept Links into a graph.Data, then
// register a graphstore so BuiltinKnowledge can do neighbor expansion. Worth it
// only once cross-links are dense and cross-concept reasoning is needed.
