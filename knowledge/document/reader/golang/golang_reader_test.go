//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package golang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func TestReadFromFileExtractsGoEntities(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestFile(t, filepath.Join(tmpDir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	goFile := filepath.Join(tmpDir, "service.go")
	writeTestFile(t, goFile, `package demo

import "context"

// Service serves requests.
type Service struct {
	Name string
}

// Store is a dependency contract.
type Store interface {
	Load(ctx context.Context) error
}

type ID string

var DefaultName = "demo"

// NewService builds a service.
func NewService(name string) *Service {
	return &Service{Name: name}
}

// Do runs the service logic.
func (s *Service) Do(ctx context.Context) error {
	return nil
}
`)

	r := New().(*Reader)
	docs, err := r.ReadFromFile(goFile)
	if err != nil {
		t.Fatalf("ReadFromFile() error = %v", err)
	}

	if len(docs) != 6 {
		t.Fatalf("len(docs) = %d, want 6", len(docs))
	}

	serviceDoc := findDocByFullName(t, docs, "example.com/demo.Service")
	assertMetadataEquals(t, serviceDoc.Metadata, "trpc_ast_type", "Struct")
	assertMetadataEquals(t, serviceDoc.Metadata, "trpc_ast_package", "example.com/demo")
	assertMetadataEquals(t, serviceDoc.Metadata, "trpc_ast_language", "go")
	assertMetadataEquals(t, serviceDoc.Metadata, "trpc_ast_scope", "code")
	assertMetadataEquals(t, serviceDoc.Metadata, source.MetaChunkIndex, 0)

	methodDoc := findDocByFullName(t, docs, "example.com/demo.Service.Do")
	assertMetadataEquals(t, methodDoc.Metadata, "trpc_ast_type", "Method")
	assertMetadataEquals(t, methodDoc.Metadata, "trpc_ast_receiver_type", "*Service")
	assertMetadataEquals(t, methodDoc.Metadata, "trpc_ast_exported", true)
	if methodDoc.EmbeddingText == "" {
		t.Fatal("expected method embedding text to be populated")
	}
	var embeddingPayload map[string]any
	if err := json.Unmarshal([]byte(methodDoc.EmbeddingText), &embeddingPayload); err != nil {
		t.Fatalf("failed to unmarshal embedding text: %v", err)
	}
	if embeddingPayload["full_name"] != "example.com/demo.Service.Do" {
		t.Fatalf("embedding full_name = %v, want %s", embeddingPayload["full_name"], "example.com/demo.Service.Do")
	}
	if embeddingPayload["id"] != "example.com/demo.Service.Do" {
		t.Fatalf("embedding id = %v, want %s", embeddingPayload["id"], "example.com/demo.Service.Do")
	}
	if _, ok := embeddingPayload["receiver_type"]; ok {
		t.Fatalf("embedding should not include receiver_type, got %v", embeddingPayload["receiver_type"])
	}

	aliasDoc := findDocByFullName(t, docs, "example.com/demo.ID")
	assertMetadataEquals(t, aliasDoc.Metadata, "trpc_ast_type", "Alias")
	assertMetadataEquals(t, aliasDoc.Metadata, "trpc_ast_go_type_kind", "definition")

	interfaceDoc := findDocByFullName(t, docs, "example.com/demo.Store")
	assertMetadataEquals(t, interfaceDoc.Metadata, "trpc_ast_type", "Interface")

	funcDoc := findDocByFullName(t, docs, "example.com/demo.NewService")
	assertMetadataEquals(t, funcDoc.Metadata, "trpc_ast_type", "Function")
	assertMetadataEquals(t, funcDoc.Metadata, "trpc_ast_comment", "NewService builds a service.")

	varDoc := findDocByFullName(t, docs, "example.com/demo.DefaultName")
	assertMetadataEquals(t, varDoc.Metadata, "trpc_ast_type", "Variable")
	assertMetadataEquals(t, varDoc.Metadata, "trpc_ast_go_value_kind", "var")
}

func TestReadFromFileWithoutChunkReturnsSingleFileDocument(t *testing.T) {
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	writeTestFile(t, goFile, `package main

func main() {}
`)

	r := New(reader.WithChunk(false)).(*Reader)
	docs, err := r.ReadFromFile(goFile)
	if err != nil {
		t.Fatalf("ReadFromFile() error = %v", err)
	}

	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	assertMetadataEquals(t, docs[0].Metadata, "trpc_ast_type", "file")
	assertMetadataEquals(t, docs[0].Metadata, "trpc_ast_language", "go")
	assertMetadataEquals(t, docs[0].Metadata, source.MetaChunkIndex, 0)
	if docs[0].EmbeddingText == "" {
		t.Fatal("expected file embedding text to be populated")
	}
	var filePayload map[string]any
	if err := json.Unmarshal([]byte(docs[0].EmbeddingText), &filePayload); err != nil {
		t.Fatalf("failed to unmarshal file embedding text: %v", err)
	}
	if filePayload["id"] != goFile {
		t.Fatalf("file embedding id = %v, want %s", filePayload["id"], goFile)
	}
	if filePayload["file_path"] != goFile {
		t.Fatalf("file embedding file_path = %v, want %s", filePayload["file_path"], goFile)
	}
}

func TestReadFromDirectoryParsesWholeModule(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestFile(t, filepath.Join(tmpDir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeTestFile(t, filepath.Join(tmpDir, "service.go"), `package demo

type Service struct{}
`)
	writeTestFile(t, filepath.Join(tmpDir, "method.go"), `package demo

func (s *Service) Do() error { return nil }
`)

	r := New().(*Reader)
	docs, err := r.ReadFromDirectory(tmpDir)
	if err != nil {
		t.Fatalf("ReadFromDirectory() error = %v", err)
	}
	if findDocByFullName(t, docs, "example.com/demo.Service") == nil {
		t.Fatal("expected service document")
	}
	if findDocByFullName(t, docs, "example.com/demo.Service.Do") == nil {
		t.Fatal("expected method document")
	}
}

func findDocByFullName(t *testing.T, docs []*document.Document, fullName string) *document.Document {
	t.Helper()
	for _, doc := range docs {
		if doc.Metadata["trpc_ast_full_name"] == fullName {
			return doc
		}
	}
	t.Fatalf("document %q not found", fullName)
	return nil
}

func assertMetadataEquals(t *testing.T, metadata map[string]any, key string, want any) {
	t.Helper()
	if got, ok := metadata[key]; !ok || got != want {
		t.Fatalf("metadata[%q] = %v, want %v", key, got, want)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
