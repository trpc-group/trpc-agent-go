//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package okfknowledge

import (
	"context"
	"strings"
	"testing"

	ksource "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

// fakeStore is a tiny in-memory okf.Store: a root dir with one subdir, each
// holding one concept.
type fakeStore struct{}

func (fakeStore) List(_ context.Context, dir string) (okf.Listing, error) {
	switch dir {
	case "":
		return okf.Listing{
			Concepts: []okf.ConceptMeta{{ID: "overview"}},
			Subdirs:  []string{"research"},
		}, nil
	case "research":
		return okf.Listing{Concepts: []okf.ConceptMeta{{ID: "research/x402"}}}, nil
	default:
		return okf.Listing{}, nil
	}
}

func (fakeStore) Read(_ context.Context, id string) (okf.Concept, error) {
	switch id {
	case "overview":
		return okf.Concept{ID: id, Frontmatter: okf.Frontmatter{Type: "Overview", Title: "Overview", Description: "Top level."}}, nil
	default:
		return okf.Concept{
			ID: id,
			Frontmatter: okf.Frontmatter{
				Type: "Protocol", Title: "x402", Description: "Agent payment protocol.",
				Resource: "https://iwiki/x402", Tags: []string{"protocol", "x402"},
			},
			Body: "full body not indexed",
		}, nil
	}
}

func (fakeStore) Find(context.Context, okf.Query) ([]okf.Hit, error) { return nil, nil }

func TestReadDocuments_OneNonChunkedDocPerConcept(t *testing.T) {
	src := New(fakeStore{}, WithName("paydocs"))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	if len(docs) != 2 { // recursed into subdir; one doc per concept, no chunking.
		t.Fatalf("want 2 docs, got %d", len(docs))
	}

	var foundX402 bool
	for _, d := range docs {
		if d.Metadata[MetaConcept] != "research/x402" {
			continue
		}
		foundX402 = true

		// Embedding text = curated title/description/tags, not the body.
		if !strings.Contains(d.EmbeddingText, "x402") || !strings.Contains(d.EmbeddingText, "Agent payment") {
			t.Errorf("embedding text not curated: %q", d.EmbeddingText)
		}
		if strings.Contains(d.EmbeddingText, "full body") || strings.Contains(d.Content, "full body") {
			t.Errorf("body must not be indexed: content=%q emb=%q", d.Content, d.EmbeddingText)
		}
		// Content must carry the concept id so the model can pass it to okf_read.
		if !strings.Contains(d.Content, "research/x402") {
			t.Errorf("Content should lead with the concept id, got %q", d.Content)
		}
		// Frontmatter lands in metadata; stable identity via MetaURI.
		if d.Metadata[MetaType] != "Protocol" || d.Metadata[ksource.MetaURI] != "research/x402" {
			t.Errorf("metadata wrong: %+v", d.Metadata)
		}
		if d.Metadata[ksource.MetaSourceName] != "paydocs" {
			t.Errorf("source name metadata = %v", d.Metadata[ksource.MetaSourceName])
		}
	}
	if !foundX402 {
		t.Fatal("research/x402 concept not indexed")
	}
}

func TestScopeFilter(t *testing.T) {
	if ScopeFilter("") != nil {
		t.Error("empty scope should be nil")
	}
	if ScopeFilter("Runbook") == nil {
		t.Error("type scope should be non-nil")
	}
	if ScopeFilter("Runbook", "x402", "core") == nil {
		t.Error("type+tags scope should be non-nil")
	}
}
