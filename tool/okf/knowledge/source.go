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
// the concept id and curated frontmatter as the embedding text and stashing
// selected frontmatter in metadata for filtering. It deliberately does NOT
// store the full body for retrieval — the agent reads full content via the
// okf_read tool. A bounded body fallback keeps minimal, type-only concepts
// discoverable.
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
// each result's Content and source URI metadata carry the concept id. A prompt
// such as the following closes the loop:
//
//	Knowledge search over the OKF bundle returns concept summaries and ids, not
//	full content. To read a concept in full, call okf_read with the concept id
//	from a search result, then follow its links.
package okfknowledge

import (
	"context"
	"encoding/hex"
	"fmt"
	"maps"
	"path"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	ksource "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

// Internal metadata keys carried on indexed concept Documents. ScopeFilter
// intentionally hides these storage details from callers.
const (
	metaType     = "okf_type"
	metaTitle    = "okf_title"
	metaResource = "okf_resource"
	metaTags     = "okf_tags"

	// metaTagPrefix identifies generated boolean fields used by ScopeFilter.
	// metaTags stays in its natural []string form for metadata consumers.
	metaTagPrefix = "okf_tag_"

	maxFallbackBodyBytes = 512
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

// WithMetadata attaches static metadata to every indexed concept. Source-owned
// identity, OKF-derived, and okf_tag_-prefixed keys are ignored so callers
// cannot override generated metadata or ScopeFilter fields.
func WithMetadata(m map[string]any) Option {
	return func(s *Source) {
		for k, v := range m {
			switch k {
			case metaType, metaTitle, metaResource, metaTags,
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

// New builds a knowledge source over store with the default name "okf". A nil
// store is accepted at construction time, but ReadDocuments returns an error.
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
func (s *Source) GetMetadata() map[string]any { return maps.Clone(s.metadata) }

// ReadDocuments walks the bundle and emits one non-chunked Document per concept.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if s.store == nil {
		return nil, fmt.Errorf("okfknowledge: nil Store")
	}
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
	title := fm.Title
	if title == "" {
		title = path.Base(c.ID)
	}
	md := make(map[string]any, len(s.metadata)+8)
	for k, v := range s.metadata {
		md[k] = v
	}
	md[metaType] = fm.Type
	md[metaTitle] = title
	md[metaResource] = fm.Resource
	md[ksource.MetaURI] = c.ID
	md[ksource.MetaSource] = s.Type()
	md[ksource.MetaSourceName] = s.name
	md[ksource.MetaChunkIndex] = 0
	if len(fm.Tags) > 0 {
		md[metaTags] = append([]string(nil), fm.Tags...)
		for _, tag := range fm.Tags {
			md[tagFilterKey(tag)] = true
		}
	}
	// Content leads with the concept id (not just the description) so the model
	// always sees an id it can pass to okf_read, even if the retrieval tool does
	// not surface metadata. Full body is still served only by okf_read.
	content := c.ID
	if title != "" && title != c.ID {
		content += " — " + title
	}
	if fm.Description != "" {
		content += " — " + fm.Description
	}
	return &document.Document{
		ID:            fmt.Sprintf("%d:%s:%s", len(s.name), s.name, c.ID),
		Name:          title,
		Content:       content,
		EmbeddingText: embeddingText(c, title),
		Metadata:      md,
	}
}

// embeddingText is the locator retrieval signal. Identity and type are always
// present; a bounded body fallback covers conformant concepts that omit all
// recommended descriptive fields.
func embeddingText(c okf.Concept, title string) string {
	fm := c.Frontmatter
	parts := []string{"id: " + c.ID}
	if fm.Type != "" {
		parts = append(parts, "type: "+fm.Type)
	}
	if title != "" {
		parts = append(parts, "title: "+title)
	}
	if fm.Description != "" {
		parts = append(parts, "description: "+fm.Description)
	}
	if len(fm.Tags) > 0 {
		parts = append(parts, "tags: "+strings.Join(fm.Tags, " "))
	}
	if fm.Title == "" && fm.Description == "" && len(fm.Tags) == 0 {
		body := strings.Join(strings.Fields(c.Body), " ")
		if body != "" {
			parts = append(parts, "body: "+truncateUTF8(body, maxFallbackBodyBytes))
		}
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
		conds = append(conds, searchfilter.Equal(ksource.MetadataFieldPrefix+metaType, conceptType))
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
	return metaTagPrefix + hex.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(tag))))
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Upgrade path (not implemented): to add graph-aware retrieval, Source can also
// implement source.GraphSource by walking concept Links into a graph.Data, then
// register a graphstore so BuiltinKnowledge can do neighbor expansion. Worth it
// only once cross-links are dense and cross-concept reasoning is needed.
