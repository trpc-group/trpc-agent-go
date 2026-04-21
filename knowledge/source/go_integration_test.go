//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package source_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"

	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/golang"
)

func TestGoFileWithFileSource(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoModuleFiles(t, tmpDir)
	goFile := filepath.Join(tmpDir, "service.go")

	src := file.New([]string{goFile})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("failed to read go file: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	methodDoc := findSourceDocByFullName(t, docs, "example.com/demo.Service.Do")
	if methodDoc.Metadata["trpc_ast_receiver_type"] != "*Service" {
		t.Fatalf("receiver_type = %v, want *Service", methodDoc.Metadata["trpc_ast_receiver_type"])
	}
	if methodDoc.Metadata[source.MetaFilePath] != goFile {
		t.Fatalf("file path = %v, want %s", methodDoc.Metadata[source.MetaFilePath], goFile)
	}
}

func TestGoFileWithDirSource(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoModuleFiles(t, tmpDir)

	src := dir.New([]string{tmpDir})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("failed to read go files from directory: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	funcDoc := findSourceDocByFullName(t, docs, "example.com/demo.NewService")
	if funcDoc.Metadata["trpc_ast_type"] != "Function" {
		t.Fatalf("type = %v, want Function", funcDoc.Metadata["trpc_ast_type"])
	}
}

func TestGoFileWithAutoSource(t *testing.T) {
	tmpDir := t.TempDir()
	writeGoModuleFiles(t, tmpDir)
	goFile := filepath.Join(tmpDir, "service.go")

	src := auto.New([]string{goFile})
	docs, err := src.ReadDocuments(context.Background())
	if err != nil {
		t.Fatalf("failed to read go file with auto source: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one document")
	}

	doc := findSourceDocByFullName(t, docs, "example.com/demo.Service")
	if doc.Metadata["trpc_ast_language"] != "go" {
		t.Fatalf("language = %v, want go", doc.Metadata["trpc_ast_language"])
	}
	if doc.Metadata["trpc_ast_scope"] != "code" {
		t.Fatalf("scope = %v, want code", doc.Metadata["trpc_ast_scope"])
	}
}

func TestFileReaderTypeGo(t *testing.T) {
	if source.FileReaderTypeGo != "go" {
		t.Fatalf("expected FileReaderTypeGo='go', got %s", source.FileReaderTypeGo)
	}
}

func writeGoModuleFiles(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"go.mod": "module example.com/demo\n\ngo 1.21\n",
		"service.go": `package demo

import "context"

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) Do(ctx context.Context) error {
	return nil
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
}

func findSourceDocByFullName(t *testing.T, docs []*document.Document, fullName string) *document.Document {
	t.Helper()
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_full_name"] == fullName {
			return doc
		}
	}
	t.Fatalf("document %q not found", fullName)
	return nil
}
