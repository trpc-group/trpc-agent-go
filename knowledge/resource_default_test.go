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
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/resourcestore"
)

const resourceTestSourceID = "source-a"

type resourceTestStore struct {
	mu sync.Mutex

	sources        []*resourcestore.SourceInfo
	listResults    map[string][]*resourcestore.ResourceInfo
	contents       map[string]string
	listSourcesErr error
	listErr        error
	openErr        error
	listRequests   []string
	openPaths      []string
	contentCloses  int
	closeErr       error
	closeCalls     int
}

func (*resourceTestStore) PutSource(context.Context, *resourcestore.SourceInfo) error {
	return nil
}

func (*resourceTestStore) PutResource(context.Context, *resourcestore.ResourceInfo, io.Reader) error {
	return nil
}

func (*resourceTestStore) DeleteResource(context.Context, string, string) error {
	return nil
}

func (*resourceTestStore) DeleteSource(context.Context, string) error {
	return nil
}

func (s *resourceTestStore) ListSources(context.Context) ([]*resourcestore.SourceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sources, s.listSourcesErr
}

func (s *resourceTestStore) ListResources(
	_ context.Context,
	sourceID string,
	parentPath string,
) ([]*resourcestore.ResourceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listRequests = append(s.listRequests, sourceID+":"+parentPath)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.listResults[parentPath], nil
}

func (s *resourceTestStore) OpenResource(
	_ context.Context,
	sourceID string,
	resourcePath string,
) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openPaths = append(s.openPaths, sourceID+":"+resourcePath)
	if s.openErr != nil {
		return nil, s.openErr
	}
	content, ok := s.contents[resourcePath]
	if !ok {
		return nil, resourcestore.ErrNotFound
	}
	return &resourceTestReadCloser{
		Reader: strings.NewReader(content),
		onClose: func() {
			s.mu.Lock()
			s.contentCloses++
			s.mu.Unlock()
		},
	}, nil
}

func (s *resourceTestStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeCalls++
	return s.closeErr
}

func (s *resourceTestStore) snapshot() ([]string, []string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.listRequests...), append([]string(nil), s.openPaths...), s.contentCloses
}

type resourceTestReadCloser struct {
	io.Reader
	once    sync.Once
	onClose func()
}

func (r *resourceTestReadCloser) Close() error {
	r.once.Do(r.onClose)
	return nil
}

func TestBuiltinKnowledgeResourcesUseStoreWithoutSourcesOrVectorStore(t *testing.T) {
	store := &resourceTestStore{
		sources: []*resourcestore.SourceInfo{{
			ID: resourceTestSourceID, Name: "product docs", Type: "dir",
		}},
		listResults: map[string][]*resourcestore.ResourceInfo{
			"":     {{Path: "docs", Name: "docs", IsDir: true, Size: -1}},
			"docs": {{Path: "docs/config.md", Size: 42}},
		},
	}
	kb := New(WithStores(Stores{Resource: store}))

	sources, err := kb.ListSources(context.Background(), &ListSourcesRequest{})
	if err != nil {
		t.Fatalf("ListSources() error = %v", err)
	}
	if len(sources.Sources) != 1 {
		t.Fatalf("ListSources() sources = %d, want 1", len(sources.Sources))
	}
	gotSource := sources.Sources[0]
	if gotSource.ID != resourceTestSourceID || gotSource.Name != "product docs" || gotSource.Type != "dir" {
		t.Fatalf("ListSources() source = %+v", gotSource)
	}

	root, err := kb.ListResources(context.Background(), &ListResourcesRequest{SourceID: resourceTestSourceID})
	if err != nil {
		t.Fatalf("ListResources(root) error = %v", err)
	}
	if len(root.Resources) != 1 || root.Resources[0].SourceID != resourceTestSourceID ||
		root.Resources[0].Path != "docs" || !root.Resources[0].IsDir {
		t.Fatalf("ListResources(root) = %+v", root)
	}
	docs, err := kb.ListResources(context.Background(), &ListResourcesRequest{
		SourceID: resourceTestSourceID, ParentPath: "docs",
	})
	if err != nil {
		t.Fatalf("ListResources(docs) error = %v", err)
	}
	if len(docs.Resources) != 1 || docs.Resources[0].Path != "docs/config.md" ||
		docs.Resources[0].Name != "config.md" {
		t.Fatalf("ListResources(docs) = %+v", docs)
	}
	requests, _, _ := store.snapshot()
	if len(requests) != 2 || requests[0] != resourceTestSourceID+":" ||
		requests[1] != resourceTestSourceID+":docs" {
		t.Fatalf("ListResources() forwarded requests = %+v", requests)
	}
}

func TestBuiltinKnowledgeResourceLookupDoesNotWaitForDataOperation(t *testing.T) {
	store := &resourceTestStore{sources: []*resourcestore.SourceInfo{{ID: resourceTestSourceID}}}
	kb := New(WithStores(Stores{Resource: store}))
	kb.dataOperationMu.Lock()
	defer kb.dataOperationMu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := kb.ListSources(context.Background(), &ListSourcesRequest{})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListSources() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ListSources() waited for an unrelated data operation")
	}
}

func TestBuiltinKnowledgeReadAndGrepPersistedResource(t *testing.T) {
	store := &resourceTestStore{contents: map[string]string{
		"docs/config.md": "header\nalpha one\nmiddle\nalpha two\nfooter\n",
	}}
	kb := New(WithStores(Stores{Resource: store}))

	read, err := kb.ReadResource(context.Background(), &ReadResourceRequest{
		SourceID: resourceTestSourceID, Path: "docs/config.md", StartLine: 2, EndLine: 3,
	})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if strings.Join(read.Lines, "|") != "alpha one|middle" || read.EOF || read.NextStartLine != 4 {
		t.Fatalf("ReadResource() = %+v", read)
	}

	grep, err := kb.GrepResource(context.Background(), &GrepResourceRequest{
		SourceID: resourceTestSourceID,
		Path:     "docs/config.md", Pattern: "alpha", Before: 1, After: 1, MaxMatches: 1,
	})
	if err != nil {
		t.Fatalf("GrepResource() error = %v", err)
	}
	if !grep.Truncated || len(grep.Blocks) != 1 ||
		strings.Join(grep.Blocks[0].Lines, "|") != "header|alpha one|middle" {
		t.Fatalf("GrepResource() = %+v", grep)
	}
	_, paths, closes := store.snapshot()
	if len(paths) != 2 || paths[0] != resourceTestSourceID+":docs/config.md" ||
		paths[1] != resourceTestSourceID+":docs/config.md" || closes != 2 {
		t.Fatalf("OpenResource() paths = %v, closes = %d", paths, closes)
	}
}

func TestBuiltinKnowledgeReadPastEOFReturnsEmptyRange(t *testing.T) {
	store := &resourceTestStore{contents: map[string]string{"a.txt": "only line\n"}}
	kb := New(WithStores(Stores{Resource: store}))
	result, err := kb.ReadResource(context.Background(), &ReadResourceRequest{
		SourceID: resourceTestSourceID, Path: "a.txt", StartLine: 10, EndLine: 20,
	})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if len(result.Lines) != 0 || !result.EOF || result.StartLine != 10 || result.EndLine != 0 {
		t.Fatalf("ReadResource() = %+v, want an empty EOF range", result)
	}
}

func TestBuiltinKnowledgeRejectsOverlongResourceLines(t *testing.T) {
	store := &resourceTestStore{contents: map[string]string{
		"large.txt": strings.Repeat("x", maxResourceLineBytes+1),
	}}
	kb := New(WithStores(Stores{Resource: store}))

	_, err := kb.ReadResource(context.Background(), &ReadResourceRequest{
		SourceID: resourceTestSourceID, Path: "large.txt",
	})
	if !errors.Is(err, ErrResourceLimitExceeded) {
		t.Fatalf("ReadResource() error = %v, want %v", err, ErrResourceLimitExceeded)
	}
	_, err = kb.GrepResource(context.Background(), &GrepResourceRequest{
		SourceID: resourceTestSourceID, Path: "large.txt", Pattern: "x",
	})
	if !errors.Is(err, ErrResourceLimitExceeded) {
		t.Fatalf("GrepResource() error = %v, want %v", err, ErrResourceLimitExceeded)
	}
}

func TestBuiltinKnowledgeGrepValidatesRegexBeforeOpen(t *testing.T) {
	store := &resourceTestStore{contents: map[string]string{"a.txt": "a"}}
	kb := New(WithStores(Stores{Resource: store}))
	_, err := kb.GrepResource(context.Background(), &GrepResourceRequest{
		SourceID: resourceTestSourceID, Path: "a.txt", Pattern: "[", Regex: true,
	})
	if err == nil {
		t.Fatal("GrepResource() error = nil, want invalid regex")
	}
	_, paths, _ := store.snapshot()
	if len(paths) != 0 {
		t.Fatalf("OpenResource() paths = %v, want none", paths)
	}
}

func TestBuiltinKnowledgeRejectsUnsafeResourcePaths(t *testing.T) {
	store := &resourceTestStore{}
	kb := New(WithStores(Stores{Resource: store}))
	unsafePaths := []string{
		"", "/etc/passwd", "../secret", "docs/../secret", `C:\\secret.txt`,
		"hdfs://cluster/secret", "file:/etc/passwd", "./hdfs://cluster/secret",
		"./file:/etc/passwd", "./C:/secret", "bad\x00path",
	}
	for _, resourcePath := range unsafePaths {
		t.Run(resourcePath, func(t *testing.T) {
			_, err := kb.ReadResource(context.Background(), &ReadResourceRequest{
				SourceID: resourceTestSourceID, Path: resourcePath,
			})
			if err == nil {
				t.Fatal("ReadResource() error = nil, want invalid path")
			}
		})
	}
	_, paths, _ := store.snapshot()
	if len(paths) != 0 {
		t.Fatalf("OpenResource() paths = %v, want none", paths)
	}
}

func TestBuiltinKnowledgeResourceStoreResolution(t *testing.T) {
	kb := New()
	if _, err := kb.ListSources(context.Background(), &ListSourcesRequest{}); !errors.Is(err, ErrResourceCapabilityUnavailable) {
		t.Fatalf("ListSources() error = %v, want %v", err, ErrResourceCapabilityUnavailable)
	}
	if _, err := kb.ReadResource(context.Background(), &ReadResourceRequest{
		SourceID: resourceTestSourceID, Path: "a.txt",
	}); !errors.Is(err, ErrResourceCapabilityUnavailable) {
		t.Fatalf("ReadResource() error = %v, want %v", err, ErrResourceCapabilityUnavailable)
	}
}

func TestBuiltinKnowledgeResourceBackendErrorsAreRedacted(t *testing.T) {
	backendErr := errors.New("dial hdfs://private-host with secret token")
	store := &resourceTestStore{openErr: backendErr}
	kb := New(WithStores(Stores{Resource: store}))
	_, err := kb.ReadResource(context.Background(), &ReadResourceRequest{
		SourceID: resourceTestSourceID, Path: "a.txt",
	})
	if !errors.Is(err, ErrResourceStoreUnavailable) {
		t.Fatalf("ReadResource() error = %v, want %v", err, ErrResourceStoreUnavailable)
	}
	if strings.Contains(err.Error(), "private-host") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("ReadResource() exposed backend details: %v", err)
	}
}

func TestBuiltinKnowledgeRejectsUnsafeStoreListing(t *testing.T) {
	store := &resourceTestStore{listResults: map[string][]*resourcestore.ResourceInfo{
		"": {{Path: "/private/root/a.txt"}},
	}}
	kb := New(WithStores(Stores{Resource: store}))
	_, err := kb.ListResources(context.Background(), &ListResourcesRequest{SourceID: resourceTestSourceID})
	if !errors.Is(err, ErrResourceStoreUnavailable) {
		t.Fatalf("ListResources() error = %v, want %v", err, ErrResourceStoreUnavailable)
	}
}

func TestBuiltinKnowledgeRejectsCrossSourceStoreListing(t *testing.T) {
	store := &resourceTestStore{listResults: map[string][]*resourcestore.ResourceInfo{
		"": {{SourceID: "another-source", Path: "a.txt"}},
	}}
	kb := New(WithStores(Stores{Resource: store}))
	_, err := kb.ListResources(context.Background(), &ListResourcesRequest{SourceID: resourceTestSourceID})
	if !errors.Is(err, ErrResourceStoreUnavailable) {
		t.Fatalf("ListResources() error = %v, want %v", err, ErrResourceStoreUnavailable)
	}
}

func TestBuiltinKnowledgeRejectsUnboundedStoreListing(t *testing.T) {
	store := &resourceTestStore{listResults: map[string][]*resourcestore.ResourceInfo{
		"": make([]*resourcestore.ResourceInfo, maxResourceListEntries+1),
	}}
	kb := New(WithStores(Stores{Resource: store}))
	_, err := kb.ListResources(context.Background(), &ListResourcesRequest{SourceID: resourceTestSourceID})
	if !errors.Is(err, ErrResourceLimitExceeded) {
		t.Fatalf("ListResources() error = %v, want %v", err, ErrResourceLimitExceeded)
	}
}

func TestBuiltinKnowledgeRejectsInvalidStoreSources(t *testing.T) {
	for _, sources := range [][]*resourcestore.SourceInfo{
		{nil},
		{{ID: " "}},
		{{ID: resourceTestSourceID}, {ID: " " + resourceTestSourceID + " "}},
	} {
		store := &resourceTestStore{sources: sources}
		kb := New(WithStores(Stores{Resource: store}))
		_, err := kb.ListSources(context.Background(), &ListSourcesRequest{})
		if !errors.Is(err, ErrResourceStoreUnavailable) {
			t.Fatalf("ListSources(%+v) error = %v, want %v", sources, err, ErrResourceStoreUnavailable)
		}
	}
}
