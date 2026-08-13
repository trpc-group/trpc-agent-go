//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package knowledge

import (
	"context"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/resourcestore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestLoadPersistsResourcesBeforeVectorDocuments(t *testing.T) {
	events := &resourceLoadEvents{}
	exact := &document.Document{ID: "exact", Content: "line two"}
	ast := &document.Document{ID: "ast", Content: "structured entity", Metadata: map[string]any{
		"trpc_ast_line_start": 3,
		"trpc_ast_line_end":   3,
	}}
	src := &resourceLoadSource{
		name:   "docs",
		events: events,
		resources: []*source.Resource{{
			Path:      "guide.txt",
			Content:   "line one\nline two\nline three\n",
			Documents: []*document.Document{exact, ast},
		}},
	}
	resourceStore := newResourceLoadStore(events)
	vectorStore := &resourceLoadVectorStore{events: events}
	kb := New(
		WithSources([]source.Source{src}),
		WithStores(Stores{Vector: vectorStore, Resource: resourceStore}),
	)

	err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if src.readDocumentsCalls != 0 || src.readResourcesCalls != 1 {
		t.Fatalf(
			"source calls = ReadDocuments:%d ReadResources:%d, want 0 and 1",
			src.readDocumentsCalls,
			src.readResourcesCalls,
		)
	}
	wantPrefix := []string{"read_resources", "put_source:docs", "put_resource:guide.txt"}
	gotEvents := events.snapshot()
	if len(gotEvents) < len(wantPrefix)+2 {
		t.Fatalf("events = %v, want resource writes followed by vector writes", gotEvents)
	}
	for i, want := range wantPrefix {
		if gotEvents[i] != want {
			t.Fatalf("events[%d] = %q, want %q; all events: %v", i, gotEvents[i], want, gotEvents)
		}
	}
	for _, event := range gotEvents[len(wantPrefix):] {
		if !strings.HasPrefix(event, "vector_add:") {
			t.Fatalf("event after resource persistence = %q, want vector_add; all events: %v", event, gotEvents)
		}
	}
	if got := resourceStore.content("docs", "guide.txt"); got != "line one\nline two\nline three\n" {
		t.Fatalf("persisted resource content = %q", got)
	}
	assertResourceDocumentMetadata(t, vectorStore.document("exact"), "docs", "guide.txt", 2, 2)
	assertResourceDocumentMetadata(t, vectorStore.document("ast"), "docs", "guide.txt", 3, 3)
	if _, mutated := exact.Metadata[source.MetaSourceID]; mutated {
		t.Fatalf("source document was mutated: %+v", exact.Metadata)
	}
}

func TestLoadKeepsLegacySourcesVectorOnly(t *testing.T) {
	events := &resourceLoadEvents{}
	legacy := &resourceLegacySource{doc: &document.Document{ID: "legacy", Content: "legacy"}}
	resourceStore := newResourceLoadStore(events)
	vectorStore := &resourceLoadVectorStore{events: events}
	kb := New(
		WithSources([]source.Source{legacy}),
		WithStores(Stores{Vector: vectorStore, Resource: resourceStore}),
	)
	if err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if legacy.calls != 1 {
		t.Fatalf("ReadDocuments() calls = %d, want 1", legacy.calls)
	}
	for _, event := range events.snapshot() {
		if strings.HasPrefix(event, "put_") {
			t.Fatalf("legacy source caused resource write: %v", events.snapshot())
		}
	}
}

func TestLoadWithoutResourceStoreUsesLegacyRead(t *testing.T) {
	events := &resourceLoadEvents{}
	src := &resourceLoadSource{
		name:      "docs",
		events:    events,
		legacyDoc: &document.Document{ID: "legacy", Content: "legacy"},
		resources: []*source.Resource{{Path: "guide.txt", Content: "full"}},
	}
	kb := New(
		WithSources([]source.Source{src}),
		WithVectorStore(&resourceLoadVectorStore{events: events}),
	)
	if err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if src.readDocumentsCalls != 1 || src.readResourcesCalls != 0 {
		t.Fatalf(
			"source calls = ReadDocuments:%d ReadResources:%d, want 1 and 0",
			src.readDocumentsCalls,
			src.readResourcesCalls,
		)
	}
}

func TestLoadValidatesAllResourcesBeforeWriting(t *testing.T) {
	tests := []struct {
		name      string
		resources []*source.Resource
	}{
		{
			name:      "unsafe path",
			resources: []*source.Resource{{Path: "../secret", Content: "secret"}},
		},
		{
			name: "duplicate path",
			resources: []*source.Resource{
				{Path: "same.txt", Content: "one"},
				{Path: "./same.txt", Content: "two"},
			},
		},
		{
			name: "shared document",
			resources: func() []*source.Resource {
				doc := &document.Document{Content: "shared"}
				return []*source.Resource{
					{Path: "one.txt", Content: "shared", Documents: []*document.Document{doc}},
					{Path: "two.txt", Content: "shared", Documents: []*document.Document{doc}},
				}
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := &resourceLoadEvents{}
			src := &resourceLoadSource{name: "docs", events: events, resources: tt.resources}
			kb := New(
				WithSources([]source.Source{src}),
				WithStores(Stores{
					Vector:   &resourceLoadVectorStore{events: events},
					Resource: newResourceLoadStore(events),
				}),
			)
			err := kb.Load(
				context.Background(),
				WithSourceConcurrency(1),
				WithDocConcurrency(1),
				WithShowProgress(false),
				WithShowStats(false),
			)
			if err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
			for _, event := range events.snapshot() {
				if strings.HasPrefix(event, "put_") || strings.HasPrefix(event, "vector_add:") {
					t.Fatalf("validation failure wrote a store: %v", events.snapshot())
				}
			}
		})
	}
}

func TestLoadResourceWriteFailurePreventsVectorWrite(t *testing.T) {
	events := &resourceLoadEvents{}
	src := &resourceLoadSource{
		name:   "docs",
		events: events,
		resources: []*source.Resource{{
			Path:      "guide.txt",
			Content:   "full",
			Documents: []*document.Document{{ID: "chunk", Content: "full"}},
		}},
	}
	store := newResourceLoadStore(events)
	store.putResourceErr = errors.New("private backend failure")
	kb := New(
		WithSources([]source.Source{src}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: store,
		}),
	)
	err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	)
	if !errors.Is(err, ErrResourceStoreUnavailable) {
		t.Fatalf("Load() error = %v, want %v", err, ErrResourceStoreUnavailable)
	}
	for _, event := range events.snapshot() {
		if strings.HasPrefix(event, "vector_add:") {
			t.Fatalf("resource write failure was followed by vector write: %v", events.snapshot())
		}
	}
}

func TestLoadPartialResourceWriteFailurePreventsVectorWrite(t *testing.T) {
	events := &resourceLoadEvents{}
	src := &resourceLoadSource{
		name:   "docs",
		events: events,
		resources: []*source.Resource{
			{
				Path:      "one.txt",
				Content:   "one",
				Documents: []*document.Document{{ID: "one", Content: "one"}},
			},
			{
				Path:      "two.txt",
				Content:   "two",
				Documents: []*document.Document{{ID: "two", Content: "two"}},
			},
		},
	}
	store := newResourceLoadStore(events)
	store.putResourceErrAt = 2
	kb := New(
		WithSources([]source.Source{src}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: store,
		}),
	)
	err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	)
	if !errors.Is(err, ErrResourceStoreUnavailable) {
		t.Fatalf("Load() error = %v, want %v", err, ErrResourceStoreUnavailable)
	}
	if got := store.content("docs", "one.txt"); got != "one" {
		t.Fatalf("first idempotent resource write = %q, want one", got)
	}
	for _, event := range events.snapshot() {
		if strings.HasPrefix(event, "vector_add:") {
			t.Fatalf("partial resource write failure was followed by vector write: %v", events.snapshot())
		}
	}
}

func TestLoadVectorFailureKeepsRetryableResourceWrite(t *testing.T) {
	events := &resourceLoadEvents{}
	src := &resourceLoadSource{
		name:   "docs",
		events: events,
		resources: []*source.Resource{{
			Path:      "guide.txt",
			Content:   "full",
			Documents: []*document.Document{{ID: "chunk", Content: "full"}},
		}},
	}
	store := newResourceLoadStore(events)
	kb := New(
		WithSources([]source.Source{src}),
		WithStores(Stores{
			Vector: &resourceLoadVectorStore{
				events: events,
				addErr: errors.New("vector failed"),
			},
			Resource: store,
		}),
	)
	if err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	); err == nil {
		t.Fatal("Load() error = nil, want vector failure")
	}
	if got := store.content("docs", "guide.txt"); got != "full" {
		t.Fatalf("retryable resource write = %q, want full", got)
	}
}

func TestLoadRecreateCleansStaleResourcesAfterVectorWrite(t *testing.T) {
	events := &resourceLoadEvents{}
	store := newResourceLoadStore(events)
	store.resources["docs"] = map[string]string{"old.txt": "old"}
	store.sources["docs"] = &resourcestore.SourceInfo{ID: "docs", Name: "docs"}
	src := &resourceLoadSource{
		name:   "docs",
		events: events,
		resources: []*source.Resource{{
			Path:      "new.txt",
			Content:   "new",
			Documents: []*document.Document{{ID: "new", Content: "new"}},
		}},
	}
	kb := New(
		WithSources([]source.Source{src}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: store,
		}),
	)
	if err := kb.Load(
		context.Background(),
		WithRecreate(true),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := store.content("docs", "old.txt"); got != "" {
		t.Fatalf("stale resource remained: %q", got)
	}
	if got := store.content("docs", "new.txt"); got != "new" {
		t.Fatalf("new resource = %q, want new", got)
	}
	gotEvents := events.snapshot()
	vectorIndex := eventIndex(gotEvents, "vector_add:new")
	deleteIndex := eventIndex(gotEvents, "delete_resource:old.txt")
	if vectorIndex < 0 || deleteIndex <= vectorIndex {
		t.Fatalf("events = %v, want stale deletion after vector add", gotEvents)
	}
}

func TestReloadFailureKeepsOldSourceRegistered(t *testing.T) {
	events := &resourceLoadEvents{}
	oldSource := &resourceLoadSource{name: "docs", events: events}
	newSource := &resourceLoadSource{
		name:   "docs",
		events: events,
		resources: []*source.Resource{{
			Path:      "new.txt",
			Content:   "new",
			Documents: []*document.Document{{ID: "new", Content: "new"}},
		}},
	}
	store := newResourceLoadStore(events)
	store.putResourceErr = errors.New("write failed")
	kb := New(
		WithSources([]source.Source{oldSource}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: store,
		}),
	)
	if err := kb.ReloadSource(
		context.Background(),
		newSource,
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	); err == nil {
		t.Fatal("ReloadSource() error = nil, want write failure")
	}
	if len(kb.sources) != 1 || kb.sources[0] != oldSource {
		t.Fatalf("sources = %v, want original source retained", kb.sources)
	}
}

func TestLoadRejectsDuplicateResourceSourceNames(t *testing.T) {
	events := &resourceLoadEvents{}
	sources := []source.Source{
		&resourceLoadSource{name: "docs", events: events},
		&resourceLoadSource{name: "docs", events: events},
	}
	kb := New(
		WithSources(sources),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: newResourceLoadStore(events),
		}),
	)
	err := kb.Load(
		context.Background(),
		WithRecreate(true),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate resource source ID") {
		t.Fatalf("Load() error = %v, want duplicate source ID", err)
	}
	if len(events.snapshot()) != 0 {
		t.Fatalf("duplicate source IDs performed I/O: %v", events.snapshot())
	}
}

func TestLoadRejectsResourceAndLegacySourceNameCollision(t *testing.T) {
	events := &resourceLoadEvents{}
	kb := New(
		WithSources([]source.Source{
			&resourceLoadSource{name: "docs", events: events},
			&resourceLegacySource{name: "docs"},
		}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: newResourceLoadStore(events),
		}),
	)
	err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate resource source ID") {
		t.Fatalf("Load() error = %v, want source name collision", err)
	}
	if len(events.snapshot()) != 0 {
		t.Fatalf("source name collision performed I/O: %v", events.snapshot())
	}
}

func TestLoadRejectsNonCanonicalResourceSourceName(t *testing.T) {
	events := &resourceLoadEvents{}
	kb := New(
		WithSources([]source.Source{&resourceLoadSource{name: " docs ", events: events}}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: newResourceLoadStore(events),
		}),
	)
	err := kb.Load(
		context.Background(),
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	)
	if err == nil || !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("Load() error = %v, want canonical source name error", err)
	}
	if len(events.snapshot()) != 0 {
		t.Fatalf("invalid source name performed I/O: %v", events.snapshot())
	}
}

func TestRemoveLegacySourceDoesNotDeleteStoredResources(t *testing.T) {
	events := &resourceLoadEvents{}
	store := newResourceLoadStore(events)
	store.sources["legacy"] = &resourcestore.SourceInfo{ID: "legacy", Name: "legacy"}
	store.resources["legacy"] = map[string]string{"external.txt": "external"}
	kb := New(
		WithSources([]source.Source{&resourceLegacySource{}}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: store,
		}),
	)
	if err := kb.RemoveSource(context.Background(), "legacy"); err != nil {
		t.Fatalf("RemoveSource() error = %v", err)
	}
	if got := store.content("legacy", "external.txt"); got != "external" {
		t.Fatalf("legacy source removed external resource content: %q", got)
	}
	if eventIndex(events.snapshot(), "delete_source:legacy") >= 0 {
		t.Fatalf("legacy source deleted a resource source: %v", events.snapshot())
	}
}

func TestResourceMutationsRequireVectorStore(t *testing.T) {
	events := &resourceLoadEvents{}
	store := newResourceLoadStore(events)
	src := &resourceLoadSource{name: "docs", events: events}

	addKnowledge := New(WithStores(Stores{Resource: store}))
	if err := addKnowledge.AddSource(context.Background(), src); err == nil ||
		err.Error() != "vector store not configured" {
		t.Fatalf("AddSource() error = %v, want vector store not configured", err)
	}

	configuredKnowledge := New(
		WithSources([]source.Source{src}),
		WithStores(Stores{Resource: store}),
	)
	if err := configuredKnowledge.ReloadSource(context.Background(), src); err == nil ||
		err.Error() != "vector store not configured" {
		t.Fatalf("ReloadSource() error = %v, want vector store not configured", err)
	}
	if err := configuredKnowledge.RemoveSource(context.Background(), "docs"); err == nil ||
		err.Error() != "vector store not configured" {
		t.Fatalf("RemoveSource() error = %v, want vector store not configured", err)
	}
	if len(events.snapshot()) != 0 {
		t.Fatalf("resource-only mutation performed I/O: %v", events.snapshot())
	}
}

func TestReloadResourceSourceWithLegacyDeletesStoredResources(t *testing.T) {
	events := &resourceLoadEvents{}
	store := newResourceLoadStore(events)
	store.sources["docs"] = &resourcestore.SourceInfo{ID: "docs", Name: "docs"}
	store.resources["docs"] = map[string]string{"old.txt": "old"}
	oldSource := &resourceLoadSource{name: "docs", events: events}
	newSource := &resourceLegacySource{
		name: "docs",
		doc:  &document.Document{ID: "new", Content: "new"},
	}
	kb := New(
		WithSources([]source.Source{oldSource}),
		WithStores(Stores{
			Vector:   &resourceLoadVectorStore{events: events},
			Resource: store,
		}),
	)
	if err := kb.ReloadSource(
		context.Background(),
		newSource,
		WithSourceConcurrency(1),
		WithDocConcurrency(1),
		WithShowProgress(false),
		WithShowStats(false),
	); err != nil {
		t.Fatalf("ReloadSource() error = %v", err)
	}
	if _, ok := store.sources["docs"]; ok {
		t.Fatal("replaced resource source metadata remained")
	}
	eventList := events.snapshot()
	if vectorIndex, deleteIndex := eventIndex(eventList, "vector_add:new"), eventIndex(eventList, "delete_source:docs"); vectorIndex < 0 || deleteIndex <= vectorIndex {
		t.Fatalf("events = %v, want resource deletion after vector add", eventList)
	}
}

func TestResourceLineCountMatchesScannerLines(t *testing.T) {
	tests := []struct {
		content string
		want    int
	}{
		{content: "", want: 0},
		{content: "one", want: 1},
		{content: "one\n", want: 1},
		{content: "one\ntwo", want: 2},
		{content: "one\ntwo\n", want: 2},
	}
	for _, tt := range tests {
		if got := resourceLineCount(tt.content); got != tt.want {
			t.Fatalf("resourceLineCount(%q) = %d, want %d", tt.content, got, tt.want)
		}
	}
}

func TestResetDocumentIDIncludesResourceLocation(t *testing.T) {
	src := &resourceLoadSource{name: "docs"}
	baseMetadata := map[string]any{
		source.MetaURI:               "file:///guide.txt",
		source.MetaChunkIndex:        1,
		source.MetaResourcePath:      "guide.txt",
		source.MetaResourceStartLine: 10,
		source.MetaResourceEndLine:   12,
	}
	first := &document.Document{Content: "unchanged chunk", Metadata: baseMetadata}
	shifted := first.Clone()
	shifted.Metadata[source.MetaResourceStartLine] = 11
	shifted.Metadata[source.MetaResourceEndLine] = 13

	kb := &BuiltinKnowledge{}
	if err := kb.resetDocumentID(first, src); err != nil {
		t.Fatalf("resetDocumentID(first) error = %v", err)
	}
	if err := kb.resetDocumentID(shifted, src); err != nil {
		t.Fatalf("resetDocumentID(shifted) error = %v", err)
	}
	if first.ID == shifted.ID {
		t.Fatalf("resource line shift kept document ID %q", first.ID)
	}
}

func assertResourceDocumentMetadata(
	t *testing.T,
	doc *document.Document,
	sourceID string,
	resourcePath string,
	startLine int,
	endLine int,
) {
	t.Helper()
	if doc == nil {
		t.Fatal("stored document is nil")
	}
	if doc.Metadata[source.MetaSourceID] != sourceID ||
		doc.Metadata[source.MetaSourceName] != sourceID ||
		doc.Metadata[source.MetaResourcePath] != resourcePath ||
		doc.Metadata[source.MetaResourceStartLine] != startLine ||
		doc.Metadata[source.MetaResourceEndLine] != endLine {
		t.Fatalf("document metadata = %+v", doc.Metadata)
	}
}

func eventIndex(events []string, target string) int {
	for i, event := range events {
		if event == target {
			return i
		}
	}
	return -1
}

type resourceLoadSource struct {
	name               string
	events             *resourceLoadEvents
	legacyDoc          *document.Document
	resources          []*source.Resource
	readDocumentsCalls int
	readResourcesCalls int
}

func (s *resourceLoadSource) ReadDocuments(context.Context) ([]*document.Document, error) {
	s.readDocumentsCalls++
	if s.events != nil {
		s.events.add("read_documents")
	}
	if s.legacyDoc == nil {
		return nil, nil
	}
	return []*document.Document{s.legacyDoc}, nil
}

func (s *resourceLoadSource) ReadResources(context.Context) ([]*source.Resource, error) {
	s.readResourcesCalls++
	if s.events != nil {
		s.events.add("read_resources")
	}
	return s.resources, nil
}

func (s *resourceLoadSource) Name() string { return s.name }

func (*resourceLoadSource) Type() string { return source.TypeFile }

func (*resourceLoadSource) GetMetadata() map[string]any { return nil }

type resourceLegacySource struct {
	name  string
	doc   *document.Document
	calls int
}

func (s *resourceLegacySource) ReadDocuments(context.Context) ([]*document.Document, error) {
	s.calls++
	return []*document.Document{s.doc}, nil
}

func (s *resourceLegacySource) Name() string {
	if s.name != "" {
		return s.name
	}
	return "legacy"
}

func (*resourceLegacySource) Type() string { return source.TypeFile }

func (*resourceLegacySource) GetMetadata() map[string]any { return nil }

type resourceLoadEvents struct {
	mu     sync.Mutex
	events []string
}

func (e *resourceLoadEvents) add(event string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *resourceLoadEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

type resourceLoadStore struct {
	mu sync.Mutex

	events           *resourceLoadEvents
	sources          map[string]*resourcestore.SourceInfo
	resources        map[string]map[string]string
	putResourceErr   error
	putResourceErrAt int
	putResourceCalls int
}

func newResourceLoadStore(events *resourceLoadEvents) *resourceLoadStore {
	return &resourceLoadStore{
		events:    events,
		sources:   make(map[string]*resourcestore.SourceInfo),
		resources: make(map[string]map[string]string),
	}
}

func (s *resourceLoadStore) PutSource(_ context.Context, info *resourcestore.SourceInfo) error {
	s.events.add("put_source:" + info.ID)
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *info
	s.sources[info.ID] = &cloned
	return nil
}

func (s *resourceLoadStore) PutResource(
	_ context.Context,
	info *resourcestore.ResourceInfo,
	content io.Reader,
) error {
	s.events.add("put_resource:" + info.Path)
	if s.putResourceErr != nil {
		return s.putResourceErr
	}
	s.mu.Lock()
	s.putResourceCalls++
	shouldFail := s.putResourceErrAt > 0 && s.putResourceCalls == s.putResourceErrAt
	s.mu.Unlock()
	if shouldFail {
		return errors.New("injected resource write failure")
	}
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resources[info.SourceID] == nil {
		s.resources[info.SourceID] = make(map[string]string)
	}
	s.resources[info.SourceID][info.Path] = string(data)
	return nil
}

func (s *resourceLoadStore) DeleteResource(_ context.Context, sourceID, resourcePath string) error {
	s.events.add("delete_resource:" + resourcePath)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.resources[sourceID], resourcePath)
	return nil
}

func (s *resourceLoadStore) DeleteSource(_ context.Context, sourceID string) error {
	s.events.add("delete_source:" + sourceID)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sources, sourceID)
	delete(s.resources, sourceID)
	return nil
}

func (s *resourceLoadStore) ListSources(context.Context) ([]*resourcestore.SourceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*resourcestore.SourceInfo, 0, len(s.sources))
	for _, info := range s.sources {
		cloned := *info
		result = append(result, &cloned)
	}
	return result, nil
}

func (s *resourceLoadStore) ListResources(
	_ context.Context,
	sourceID string,
	parentPath string,
) ([]*resourcestore.ResourceInfo, error) {
	s.events.add("list_resources:" + parentPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	files, ok := s.resources[sourceID]
	if !ok {
		return nil, resourcestore.ErrNotFound
	}
	entries := make(map[string]*resourcestore.ResourceInfo)
	for resourcePath, content := range files {
		remainder := resourcePath
		if parentPath != "" {
			prefix := parentPath + "/"
			if !strings.HasPrefix(resourcePath, prefix) {
				continue
			}
			remainder = strings.TrimPrefix(resourcePath, prefix)
		}
		segment, rest, _ := strings.Cut(remainder, "/")
		entryPath := segment
		if parentPath != "" {
			entryPath = parentPath + "/" + segment
		}
		if rest != "" {
			entries[entryPath] = &resourcestore.ResourceInfo{
				SourceID: sourceID,
				Path:     entryPath,
				Name:     path.Base(entryPath),
				IsDir:    true,
				Size:     -1,
			}
			continue
		}
		entries[entryPath] = &resourcestore.ResourceInfo{
			SourceID: sourceID,
			Path:     entryPath,
			Name:     path.Base(entryPath),
			Size:     int64(len(content)),
		}
	}
	result := make([]*resourcestore.ResourceInfo, 0, len(entries))
	for _, info := range entries {
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func (s *resourceLoadStore) OpenResource(
	_ context.Context,
	sourceID string,
	resourcePath string,
) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.resources[sourceID][resourcePath]
	if !ok {
		return nil, resourcestore.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

func (*resourceLoadStore) Close() error { return nil }

func (s *resourceLoadStore) content(sourceID, resourcePath string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resources[sourceID][resourcePath]
}

type resourceLoadVectorStore struct {
	storesTestVectorStore
	events *resourceLoadEvents
	mu     sync.Mutex
	docs   map[string]*document.Document
	addErr error
}

func (s *resourceLoadVectorStore) Add(
	_ context.Context,
	doc *document.Document,
	_ []float64,
) error {
	s.events.add("vector_add:" + doc.ID)
	if s.addErr != nil {
		return s.addErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.docs == nil {
		s.docs = make(map[string]*document.Document)
	}
	s.docs[doc.ID] = doc.Clone()
	return nil
}

func (s *resourceLoadVectorStore) DeleteByFilter(
	_ context.Context,
	_ ...vectorstore.DeleteOption,
) error {
	s.events.add("vector_delete")
	return nil
}

func (s *resourceLoadVectorStore) document(id string) *document.Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.docs[id]
	if doc == nil {
		return nil
	}
	return doc.Clone()
}
