//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package repo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func TestReadDocumentsFromLocalRepoAlignsMetadata(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "service.go"), `package demo

import "context"

// Service serves requests.
type Service struct{}

// Do runs the service logic.
func (s *Service) Do(ctx context.Context) error { return nil }
`)

	src := New([]string{repoRoot}, WithRepoName("demo-repo"), WithRepoURL("https://example.com/demo.git"), WithBranch("main"))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected repository documents")
	}

	var methodDocFound bool
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_full_name"] == "example.com/demo.Service.Do" {
			methodDocFound = true
			assertEqual(t, doc.Metadata[source.MetaSource], source.TypeRepo)
			assertEqual(t, doc.Metadata[source.MetaRepoName], "demo-repo")
			assertEqual(t, doc.Metadata[source.MetaRepoURL], "https://example.com/demo.git")
			assertEqual(t, doc.Metadata[source.MetaBranch], "main")
			assertEqual(t, doc.Metadata["trpc_ast_file_path"], "service.go")

			var payload map[string]any
			if err := json.Unmarshal([]byte(doc.EmbeddingText), &payload); err != nil {
				t.Fatalf("failed to unmarshal embedding text: %v", err)
			}
			assertEqual(t, payload["id"], "example.com/demo.Service.Do")
		}
	}
	if !methodDocFound {
		t.Fatal("expected method document not found")
	}
}

func TestReadDocumentsSkipsGeneratedFiles(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "keep.go"), "package demo\n\nfunc Keep() {}\n")
	writeRepoFile(t, filepath.Join(repoRoot, "skip.pb.go"), "package demo\n\nfunc Skip() {}\n")
	writeRepoFile(t, filepath.Join(repoRoot, "api.proto"), `syntax = "proto3";
package demo;

message KeepProto { string name = 1; }
`)
	writeRepoFile(t, filepath.Join(repoRoot, "api.pb.proto"), `syntax = "proto3";
package demo;

message SkipProto { string name = 1; }
`)

	src := New([]string{repoRoot}, WithSkipSuffixes([]string{".pb.go", ".pb.proto", ".trpc.go", "_mock.go"}))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_file_path"] == "skip.pb.go" {
			t.Fatal("generated file should have been skipped")
		}
		if doc.Metadata["trpc_ast_file_path"] == "api.pb.proto" {
			t.Fatal("generated proto file should have been skipped")
		}
	}
}

func TestReadDocumentsParserTaskRespectsSubdirFilter(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "service.go"), "package demo\n\ntype Root struct{}\n")
	writeRepoFile(t, filepath.Join(repoRoot, "internal", "api.go"), "package internal\n\ntype Internal struct{}\n")

	src := New([]string{repoRoot}, WithSubdir("internal"))
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	for _, doc := range docs {
		if doc.Metadata["trpc_ast_file_path"] == "service.go" {
			t.Fatal("root-level Go entity should not be included when subdir=internal")
		}
	}
}

func TestReadDocumentsFromModuleParsesCrossFilePackage(t *testing.T) {
	repoRoot := t.TempDir()
	writeRepoFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(repoRoot, "service.go"), `package demo

type Service struct{}
`)
	writeRepoFile(t, filepath.Join(repoRoot, "method.go"), `package demo

func (s *Service) Do() error { return nil }
`)

	src := New([]string{repoRoot})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("ReadDocuments() error = %v", err)
	}

	var foundService, foundMethod bool
	var serviceCount, methodCount int
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_full_name"] == "example.com/demo.Service" {
			foundService = true
			serviceCount++
		}
		if doc.Metadata["trpc_ast_full_name"] == "example.com/demo.Service.Do" {
			foundMethod = true
			methodCount++
			assertEqual(t, doc.Metadata["trpc_ast_file_path"], "method.go")
		}
	}
	if !foundService || !foundMethod {
		t.Fatalf("expected both service and method docs, got service=%v method=%v", foundService, foundMethod)
	}
	assertEqual(t, serviceCount, 1)
	assertEqual(t, methodCount, 1)
}

func TestReadDocumentsRejectsMultipleRepositoriesPerSource(t *testing.T) {
	repoRoot := t.TempDir()
	src := New(nil, WithRepository(
		Repository{Dir: repoRoot},
		Repository{URL: "https://example.com/demo.git"},
	))

	_, err := src.ReadDocuments(context.Background())
	if err == nil {
		t.Fatal("expected error for multiple repositories per source")
	}
}

func TestResolvedInputsUsesStructuredOptions(t *testing.T) {
	src := New(nil, WithRepoURLs("https://example.com/demo.git"), WithDirs("/tmp/demo"))
	inputs := src.resolvedInputs()
	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}
	assertEqual(t, inputs[0], "https://example.com/demo.git")
	assertEqual(t, inputs[1], "/tmp/demo")
}

func TestFirstNonEmpty(t *testing.T) {
	assertEqual(t, firstNonEmpty("commit-sha", "v1.0.0", "main"), "commit-sha")
	assertEqual(t, firstNonEmpty("", "v1.0.0", "main"), "v1.0.0")
	assertEqual(t, firstNonEmpty("", "", "main"), "main")
	assertEqual(t, firstNonEmpty("", "", ""), "")
}

func TestResolvedRepositoriesUsesStructuredRepositories(t *testing.T) {
	src := New(nil, WithRepository(
		Repository{URL: "https://example.com/demo.git", Branch: "main"},
		Repository{Dir: "/tmp/demo", Tag: "v1.0.0"},
	))
	repositories := src.resolvedRepositories()
	if len(repositories) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(repositories))
	}
	assertEqual(t, repositories[0].URL, "https://example.com/demo.git")
	assertEqual(t, repositories[0].Branch, "main")
	assertEqual(t, repositories[1].Dir, "/tmp/demo")
	assertEqual(t, repositories[1].Tag, "v1.0.0")
}

func TestWithFileExtensionsCopiesCallerSlice(t *testing.T) {
	extensions := []string{".go", ".proto"}
	src := New(nil, WithFileExtensions(extensions))

	extensions[0] = ".md"

	if got, want := len(src.fileExtensions), 2; got != want {
		t.Fatalf("fileExtensions length = %d, want %d", got, want)
	}
	assertEqual(t, src.fileExtensions[0], ".go")
	assertEqual(t, src.fileExtensions[1], ".proto")
}

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func writeRepoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
