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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestReadFromFileWithoutChunkMarksExampleScope(t *testing.T) {
	tmpDir := t.TempDir()
	exampleDir := filepath.Join(tmpDir, "examples")
	if err := os.MkdirAll(exampleDir, 0755); err != nil {
		t.Fatalf("failed to create examples dir: %v", err)
	}
	goFile := filepath.Join(exampleDir, "main.go")
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
	assertMetadataEquals(t, docs[0].Metadata, "trpc_ast_scope", "example")
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

func TestReadFromDirectoryIncludesNestedModules(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestFile(t, filepath.Join(tmpDir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeTestFile(t, filepath.Join(tmpDir, "root.go"), `package root

func Root() {}
`)

	nestedDir := filepath.Join(tmpDir, "examples", "demo")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("failed to mkdir nested dir: %v", err)
	}
	writeTestFile(t, filepath.Join(nestedDir, "go.mod"), "module example.com/nested\n\ngo 1.21\n")
	writeTestFile(t, filepath.Join(nestedDir, "nested.go"), `package demo

func Nested() {}
`)

	r := New().(*Reader)
	docs, err := r.ReadFromDirectory(tmpDir)
	if err != nil {
		t.Fatalf("ReadFromDirectory() error = %v", err)
	}

	if findDocByFullName(t, docs, "example.com/root.Root") == nil {
		t.Fatal("expected root module document")
	}
	if findDocByFullName(t, docs, "example.com/nested.Nested") == nil {
		t.Fatal("expected nested module document")
	}
}

func TestReadFromReaderAndSupportedExtensions(t *testing.T) {
	r := New().(*Reader)
	docs, err := r.ReadFromReader("inline.go", strings.NewReader(`package demo
func Inline() {}
`))
	if err != nil {
		t.Fatalf("ReadFromReader() error = %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected docs from reader input")
	}
	exts := r.SupportedExtensions()
	if len(exts) != 1 || exts[0] != ".go" {
		t.Fatalf("SupportedExtensions() = %v, want [.go]", exts)
	}
}

func TestReadFromURLAndExtractFileNameFromURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "package demo\nfunc URLFunc() {}\n")
	}))
	defer srv.Close()

	r := New().(*Reader)
	docs, err := r.ReadFromURL(srv.URL + "/service.go?x=1#y")
	if err != nil {
		t.Fatalf("ReadFromURL() error = %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected docs from URL input")
	}
	if got := r.extractFileNameFromURL(srv.URL + "/service.go?x=1#y"); got != "service.go" {
		t.Fatalf("extractFileNameFromURL() = %s, want service.go", got)
	}
	if got := r.extractFileNameFromURL(srv.URL + "/"); got == "" {
		t.Fatal("extractFileNameFromURL() should not return empty")
	}
}

func TestReadFromURLErrors(t *testing.T) {
	r := New().(*Reader)
	if _, err := r.ReadFromURL("://bad-url"); err == nil {
		t.Fatal("expected invalid URL error")
	}
	if _, err := r.ReadFromURL("file:///tmp/demo.go"); err == nil {
		t.Fatal("expected invalid URL scheme error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := r.ReadFromURL(srv.URL + "/x.go"); err == nil {
		t.Fatal("expected HTTP status error")
	}
}

type errTransformer struct{}

func (errTransformer) Name() string { return "errTransformer" }

func (errTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return nil, fmt.Errorf("preprocess error")
}

func (errTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

func TestApplyTransformersErrorPath(t *testing.T) {
	r := New(reader.WithTransformers(errTransformer{})).(*Reader)
	_, err := r.ReadFromReader("inline.go", strings.NewReader("package demo\nfunc F(){}\n"))
	if err == nil {
		t.Fatal("expected transformer preprocess error")
	}
}

type postErrTransformer struct{}

func (postErrTransformer) Name() string { return "postErrTransformer" }

func (postErrTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	return docs, nil
}

func (postErrTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	return nil, fmt.Errorf("postprocess error")
}

func TestApplyTransformersPostprocessErrorPath(t *testing.T) {
	r := New(reader.WithTransformers(postErrTransformer{})).(*Reader)
	_, err := r.ReadFromReader("inline.go", strings.NewReader("package demo\nfunc F(){}\n"))
	if err == nil {
		t.Fatal("expected transformer postprocess error")
	}
}

func TestReadFromFileAndDirectoryErrorPaths(t *testing.T) {
	r := New().(*Reader)

	if _, err := r.ReadFromFile("not-go.txt"); err == nil {
		t.Fatal("expected unsupported extension error")
	}
	if _, err := r.ReadFromFile(filepath.Join(t.TempDir(), "missing.go")); err == nil {
		t.Fatal("expected read file error for missing file")
	}

	missingDir := filepath.Join(t.TempDir(), "missing-dir")
	if _, err := r.ReadFromDirectory(missingDir); err == nil {
		t.Fatal("expected stat error for missing directory")
	}

	filePath := filepath.Join(t.TempDir(), "x.go")
	writeTestFile(t, filePath, "package demo\n")
	if _, err := r.ReadFromDirectory(filePath); err == nil {
		t.Fatal("expected not-a-directory error")
	}
}

func TestReadFromReaderInvalidGoChunkModes(t *testing.T) {
	chunkReader := New().(*Reader)
	if _, err := chunkReader.ReadFromReader("bad.go", strings.NewReader("package demo\nfunc Broken( {")); err == nil {
		t.Fatal("expected parse error in chunk mode")
	}

	nonChunkReader := New(reader.WithChunk(false)).(*Reader)
	docs, err := nonChunkReader.ReadFromReader("bad.go", strings.NewReader("package demo\nfunc Broken( {"))
	if err != nil {
		t.Fatalf("ReadFromReader() non-chunk error = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	assertMetadataEquals(t, docs[0].Metadata, "trpc_ast_type", "file")
}

func TestCreateFileDocumentFromInfoUsesRepoRootForExampleScope(t *testing.T) {
	repoRoot := t.TempDir()
	filePath := filepath.Join(repoRoot, "examples", "demo", "main.go")

	r := New(reader.WithChunk(false)).(*Reader)
	doc := r.createFileDocumentFromInfo(
		"package main\nfunc main() {}\n",
		filePath,
		map[string]any{source.MetaRepoPath: repoRoot},
		nil,
	)

	assertMetadataEquals(t, doc.Metadata, "trpc_ast_scope", "example")
}

func TestChunkedReaderUsesRepoRootForScope(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "examples")
	repoRoot := filepath.Join(workspace, "repo")
	filePath := filepath.Join(repoRoot, "pkg", "service.go")

	r := New().(*Reader)
	docs, err := r.processContent(
		"package demo\n\nfunc Serve() {}\n",
		filePath,
		map[string]any{source.MetaRepoPath: repoRoot},
	)
	if err != nil {
		t.Fatalf("processContent() error = %v", err)
	}

	doc := findDocByFullName(t, docs, "demo.Serve")
	assertMetadataEquals(t, doc.Metadata, "trpc_ast_scope", "code")
}

func TestResolveScopeUsesRepoRootMetadata(t *testing.T) {
	repoRoot := t.TempDir()

	if got := resolveScope(filepath.Join(repoRoot, "examples", "demo", "main.go"), map[string]any{
		source.MetaRepoPath: repoRoot,
	}); got != "example" {
		t.Fatalf("resolveScope(example) = %q, want example", got)
	}

	if got := resolveScope(filepath.Join(repoRoot, "pkg", "service.go"), map[string]any{
		source.MetaRepoPath: repoRoot,
	}); got != "code" {
		t.Fatalf("resolveScope(code) = %q, want code", got)
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
