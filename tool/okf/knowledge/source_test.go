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
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	ksource "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool/okf"
)

// fakeStore is a tiny in-memory okf.Store: a root dir with one subdir, each
// holding one concept.
type fakeStore struct {
	listErrors map[string]error
	readErrors map[string]error
}

func (f fakeStore) List(_ context.Context, dir string) (okf.Listing, error) {
	if err := f.listErrors[dir]; err != nil {
		return okf.Listing{}, err
	}
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

func (f fakeStore) Read(_ context.Context, id string) (okf.Concept, error) {
	if err := f.readErrors[id]; err != nil {
		return okf.Concept{}, err
	}
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

type testEmbedder struct{}

func (testEmbedder) GetEmbedding(context.Context, string) ([]float64, error) {
	return []float64{1, 0, 0}, nil
}

func (testEmbedder) GetEmbeddingWithUsage(context.Context, string) ([]float64, map[string]any, error) {
	return []float64{1, 0, 0}, nil, nil
}

func (testEmbedder) GetDimensions() int { return 3 }

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
		if d.Metadata[ksource.MetaChunkIndex] != 0 {
			t.Errorf("chunk index metadata = %v, want 0", d.Metadata[ksource.MetaChunkIndex])
		}
		tags, ok := d.Metadata[MetaTags].([]string)
		if !ok || !slices.Equal(tags, []string{"protocol", "x402"}) {
			t.Errorf("tags metadata = %#v, want original []string", d.Metadata[MetaTags])
		}
		if d.ID != "7:paydocs:research/x402" {
			t.Errorf("document ID = %q, want source-namespaced ID", d.ID)
		}
	}
	if !foundX402 {
		t.Fatal("research/x402 concept not indexed")
	}
}

func TestDocumentID_NamespaceIsUnambiguous(t *testing.T) {
	left := New(fakeStore{}, WithName("a")).toDocument(okf.Concept{ID: "b:c"})
	right := New(fakeStore{}, WithName("a:b")).toDocument(okf.Concept{ID: "c"})
	if left.ID == right.ID {
		t.Fatalf("ambiguous source/concept IDs collided: %q", left.ID)
	}
}

func TestReadDocuments_PropagatesErrors(t *testing.T) {
	backendErr := errors.New("backend unavailable")
	tests := []struct {
		name  string
		store fakeStore
		ctx   context.Context
		want  error
	}{
		{
			name:  "root list",
			store: fakeStore{listErrors: map[string]error{"": backendErr}},
			ctx:   context.Background(),
			want:  backendErr,
		},
		{
			name:  "nested list",
			store: fakeStore{listErrors: map[string]error{"research": backendErr}},
			ctx:   context.Background(),
			want:  backendErr,
		},
		{
			name:  "concept read",
			store: fakeStore{readErrors: map[string]error{"overview": backendErr}},
			ctx:   context.Background(),
			want:  backendErr,
		},
		{
			name:  "backend cancellation",
			store: fakeStore{readErrors: map[string]error{"overview": context.Canceled}},
			ctx:   context.Background(),
			want:  context.Canceled,
		},
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name  string
		store fakeStore
		ctx   context.Context
		want  error
	}{name: "canceled context", store: fakeStore{}, ctx: canceled, want: context.Canceled})

	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancelDeadline()
	tests = append(tests, struct {
		name  string
		store fakeStore
		ctx   context.Context
		want  error
	}{name: "expired deadline", store: fakeStore{}, ctx: deadline, want: context.DeadlineExceeded})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.store).ReadDocuments(tt.ctx)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ReadDocuments error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}

func TestWithMetadata_CannotOverrideGeneratedIdentity(t *testing.T) {
	src := New(fakeStore{}, WithName("paydocs"), WithMetadata(map[string]any{
		"tenant":               "payments",
		tagFilterKey("x402"):   false,
		MetaConcept:            "wrong",
		MetaType:               "wrong",
		ksource.MetaURI:        "wrong",
		ksource.MetaSource:     "wrong",
		ksource.MetaSourceName: "wrong",
		ksource.MetaChunkIndex: 99,
	}))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments: %v", err)
	}
	d := docs[0]
	if d.Metadata["tenant"] != "payments" {
		t.Errorf("custom metadata missing: %+v", d.Metadata)
	}
	if d.Metadata[MetaConcept] != "overview" || d.Metadata[MetaType] != "Overview" ||
		d.Metadata[ksource.MetaURI] != "overview" || d.Metadata[ksource.MetaSource] != "okf" ||
		d.Metadata[ksource.MetaSourceName] != "paydocs" || d.Metadata[ksource.MetaChunkIndex] != 0 {
		t.Errorf("generated metadata was overridden: %+v", d.Metadata)
	}
	for _, doc := range docs {
		if doc.Metadata[MetaConcept] == "research/x402" && doc.Metadata[tagFilterKey("x402")] != true {
			t.Errorf("generated tag filter metadata was overridden: %+v", doc.Metadata)
		}
	}
}

func TestBuiltinKnowledge_InMemoryIntegration(t *testing.T) {
	for _, enableSync := range []bool{false, true} {
		t.Run(fmt.Sprintf("source_sync_%t", enableSync), func(t *testing.T) {
			store := inmemory.New()
			kb := knowledge.New(
				knowledge.WithVectorStore(store),
				knowledge.WithEmbedder(testEmbedder{}),
				knowledge.WithEnableSourceSync(enableSync),
			)
			for _, name := range []string{"bundle-a", "bundle-b"} {
				if err := kb.AddSource(context.Background(), New(fakeStore{}, WithName(name))); err != nil {
					t.Fatalf("AddSource(%q): %v", name, err)
				}
			}

			count, err := store.Count(context.Background())
			if err != nil {
				t.Fatalf("Count: %v", err)
			}
			if count != 4 {
				t.Fatalf("document count = %d, want 4; same concept IDs from two sources collided", count)
			}

			result, err := kb.Search(context.Background(), &knowledge.SearchRequest{
				MaxResults: 10,
				SearchFilter: &knowledge.SearchFilter{
					FilterCondition: ScopeFilter("Protocol", "x402", "protocol"),
				},
			})
			if err != nil {
				t.Fatalf("Search with tag filter: %v", err)
			}
			if len(result.Documents) != 2 {
				t.Fatalf("filtered document count = %d, want 2", len(result.Documents))
			}
			for _, got := range result.Documents {
				if got.Document.Metadata[MetaConcept] != "research/x402" {
					t.Errorf("unexpected filtered concept: %+v", got.Document.Metadata)
				}
			}

			if enableSync {
				reloadErr := errors.New("bundle temporarily unavailable")
				err = kb.ReloadSource(context.Background(), New(fakeStore{
					listErrors: map[string]error{"": reloadErr},
				}, WithName("bundle-a")))
				if !errors.Is(err, reloadErr) {
					t.Fatalf("ReloadSource error = %v, want backend error", err)
				}
				count, err = store.Count(context.Background())
				if err != nil {
					t.Fatalf("Count after failed reload: %v", err)
				}
				if count != 4 {
					t.Fatalf("failed reload changed stored documents: got %d, want 4", count)
				}
			}
		})
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
