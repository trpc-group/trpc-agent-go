//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package python

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	docreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

type directoryReader interface {
	ReadFromDirectory(string) ([]*document.Document, error)
}

func newDirectoryReader(t *testing.T) directoryReader {
	t.Helper()
	r, ok := New().(directoryReader)
	if !ok {
		t.Fatal("New() reader does not support ReadFromDirectory")
	}
	return r
}

type testTransformer struct {
	preErr      error
	postErr     error
	preCalled   bool
	postCalled  bool
	metadataKey string
}

func (t *testTransformer) Preprocess(docs []*document.Document) ([]*document.Document, error) {
	t.preCalled = true
	if t.preErr != nil {
		return nil, t.preErr
	}
	for _, doc := range docs {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		doc.Metadata[t.metadataKey] = "pre"
	}
	return docs, nil
}

func (t *testTransformer) Postprocess(docs []*document.Document) ([]*document.Document, error) {
	t.postCalled = true
	if t.postErr != nil {
		return nil, t.postErr
	}
	for _, doc := range docs {
		doc.Metadata[t.metadataKey] = "post"
	}
	return docs, nil
}

func (t *testTransformer) Name() string { return "test-transformer" }

var _ transform.Transformer = (*testTransformer)(nil)

func TestReaderPublicAPI(t *testing.T) {
	r := New()
	if r.Name() != "PythonReader" {
		t.Fatalf("Name() = %q, want PythonReader", r.Name())
	}
	if got := r.SupportedExtensions(); len(got) != 1 || got[0] != ".py" {
		t.Fatalf("SupportedExtensions() = %v, want [.py]", got)
	}

	docs, err := r.ReadFromReader("sample.py", strings.NewReader("class Service:\n    pass\n"))
	if err != nil {
		t.Fatalf("ReadFromReader() error = %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("ReadFromReader() returned no docs")
	}
	if docs[0].Metadata["trpc_ast_language"] != string(codeast.LanguagePython) {
		t.Fatalf("trpc_ast_language = %v, want python", docs[0].Metadata["trpc_ast_language"])
	}
}

func TestReadFromFileMetadataAndErrors(t *testing.T) {
	dir := t.TempDir()
	pyPath := filepath.Join(dir, "service.py")
	if err := os.WriteFile(pyPath, []byte("import os\nclass Service:\n    pass\n"), 0644); err != nil {
		t.Fatalf("write service.py: %v", err)
	}
	txtPath := filepath.Join(dir, "service.txt")
	if err := os.WriteFile(txtPath, []byte("not python"), 0644); err != nil {
		t.Fatalf("write service.txt: %v", err)
	}

	r := New()
	docs, err := r.ReadFromFile(pyPath)
	if err != nil {
		t.Fatalf("ReadFromFile() error = %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("ReadFromFile() returned no docs")
	}
	metadata := docs[0].Metadata
	if metadata[source.MetaSource] != source.TypeFile {
		t.Fatalf("source metadata = %v, want %s", metadata[source.MetaSource], source.TypeFile)
	}
	if metadata[source.MetaFilePath] != pyPath {
		t.Fatalf("file path metadata = %v, want %s", metadata[source.MetaFilePath], pyPath)
	}
	if !strings.HasPrefix(metadata[source.MetaURI].(string), "file://") {
		t.Fatalf("uri metadata = %v, want file URI", metadata[source.MetaURI])
	}

	if _, err := r.ReadFromFile(txtPath); err == nil || !strings.Contains(err.Error(), "unsupported file extension") {
		t.Fatalf("ReadFromFile(.txt) error = %v, want unsupported extension", err)
	}
	if _, err := r.ReadFromFile(filepath.Join(dir, "missing.py")); err == nil || !strings.Contains(err.Error(), "failed to read file") {
		t.Fatalf("ReadFromFile(missing) error = %v, want read error", err)
	}
}

func TestReadFromURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error.py" {
			http.Error(w, "bad", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("def run():\n    return 1\n"))
	}))
	defer server.Close()

	r := New()
	docs, err := r.ReadFromURL(server.URL + "/pkg/mod.py?download=1#frag")
	if err != nil {
		t.Fatalf("ReadFromURL() error = %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("ReadFromURL() returned no docs")
	}
	if docs[0].Metadata["trpc_ast_file_path"] != "mod.py" {
		t.Fatalf("trpc_ast_file_path = %v, want mod.py", docs[0].Metadata["trpc_ast_file_path"])
	}

	if _, err := r.ReadFromURL("ftp://example.com/mod.py"); err == nil || !strings.Contains(err.Error(), "invalid URL scheme") {
		t.Fatalf("ReadFromURL(ftp) error = %v, want scheme error", err)
	}
	if _, err := r.ReadFromURL(server.URL + "/error.py"); err == nil || !strings.Contains(err.Error(), "HTTP error: 500") {
		t.Fatalf("ReadFromURL(500) error = %v, want HTTP error", err)
	}
}

func TestReadWithoutChunkCreatesFileDocument(t *testing.T) {
	r := New(docreader.WithChunk(false))
	docs, err := r.ReadFromReader("pkg/sample.py", strings.NewReader("import os\nvalue = 1\n"))
	if err != nil {
		t.Fatalf("ReadFromReader() error = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ReadFromReader() returned %d docs, want 1", len(docs))
	}
	metadata := docs[0].Metadata
	if metadata["trpc_ast_type"] != "file" {
		t.Fatalf("trpc_ast_type = %v, want file", metadata["trpc_ast_type"])
	}
	if metadata["trpc_ast_package"] != "pkg.sample" {
		t.Fatalf("trpc_ast_package = %v, want pkg.sample", metadata["trpc_ast_package"])
	}
	if metadata["trpc_ast_import_count"] != 1 {
		t.Fatalf("trpc_ast_import_count = %v, want 1", metadata["trpc_ast_import_count"])
	}
}

func TestReadWithoutChunkFallsBackWhenFileInfoFails(t *testing.T) {
	r := New(docreader.WithChunk(false))
	docs, err := r.ReadFromReader("broken.py", strings.NewReader("def broken(:\n"))
	if err != nil {
		t.Fatalf("ReadFromReader() error = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ReadFromReader() returned %d docs, want fallback file doc", len(docs))
	}
	if docs[0].Metadata["trpc_ast_package"] != nil {
		t.Fatalf("trpc_ast_package = %v, want no package when file info fails", docs[0].Metadata["trpc_ast_package"])
	}
}

func TestReadAppliesTransformers(t *testing.T) {
	transformer := &testTransformer{metadataKey: "stage"}
	r := New(docreader.WithTransformers(transformer))
	docs, err := r.ReadFromReader("sample.py", strings.NewReader("class Service:\n    pass\n"))
	if err != nil {
		t.Fatalf("ReadFromReader() error = %v", err)
	}
	if !transformer.preCalled || !transformer.postCalled {
		t.Fatalf("transformer calls pre=%v post=%v, want both", transformer.preCalled, transformer.postCalled)
	}
	if docs[0].Metadata["stage"] != "post" {
		t.Fatalf("stage metadata = %v, want post", docs[0].Metadata["stage"])
	}
}

func TestReadTransformerErrors(t *testing.T) {
	preErr := errors.New("pre failed")
	r := New(docreader.WithTransformers(&testTransformer{preErr: preErr}))
	if _, err := r.ReadFromReader("sample.py", strings.NewReader("class Service:\n    pass\n")); err == nil ||
		!strings.Contains(err.Error(), "failed to apply preprocess") {
		t.Fatalf("ReadFromReader() preprocess error = %v, want wrapped preprocess error", err)
	}

	postErr := errors.New("post failed")
	r = New(docreader.WithTransformers(&testTransformer{postErr: postErr}))
	if _, err := r.ReadFromReader("sample.py", strings.NewReader("class Service:\n    pass\n")); err == nil ||
		!strings.Contains(err.Error(), "failed to apply postprocess") {
		t.Fatalf("ReadFromReader() postprocess error = %v, want wrapped postprocess error", err)
	}
}

func TestResolveScope(t *testing.T) {
	root := t.TempDir()
	examplePath := filepath.Join(root, "examples", "demo.py")
	if got := resolveScope(examplePath, map[string]any{source.MetaRepoPath: root}); got != string(codeast.ScopeExample) {
		t.Fatalf("resolveScope(example) = %q, want example", got)
	}
	if got := resolveScope(filepath.Join(root, "pkg", "demo.py"), map[string]any{source.MetaRepoPath: 123}); got != string(codeast.ScopeCode) {
		t.Fatalf("resolveScope(non-string repo root) = %q, want code", got)
	}
}

func TestReadFromDirectoryContinuesWhenSomeFilesFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.py"), []byte("class Good:\n    pass\n"), 0644); err != nil {
		t.Fatalf("write good.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte("def broken(:\n"), 0644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	r := newDirectoryReader(t)
	docs, err := r.ReadFromDirectory(dir)
	if err != nil {
		t.Fatalf("ReadFromDirectory() error = %v, want nil for partial failure", err)
	}
	if len(docs) == 0 {
		t.Fatal("ReadFromDirectory() returned no docs from successfully parsed file")
	}
}

func TestReadFromDirectoryReturnsErrorWhenAllFilesFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte("def broken(:\n"), 0644); err != nil {
		t.Fatalf("write bad.py: %v", err)
	}

	r := newDirectoryReader(t)
	_, err := r.ReadFromDirectory(dir)
	if err == nil {
		t.Fatal("ReadFromDirectory() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "all 1 file(s) failed") || !strings.Contains(err.Error(), "bad.py") {
		t.Fatalf("ReadFromDirectory() error = %v, want all-files-failed context with file name", err)
	}
}

func TestFileToModulePathInitFiles(t *testing.T) {
	tests := []struct {
		relPath    string
		baseModule string
		want       string
	}{
		{relPath: "__init__.py", baseModule: "pkg", want: "pkg"},
		{relPath: filepath.Join("sub", "__init__.py"), baseModule: "pkg", want: "pkg.sub"},
		{relPath: filepath.Join("sub", "mod.py"), baseModule: "pkg", want: "pkg.sub.mod"},
		{relPath: "__init__.py", want: ""},
	}
	for _, tt := range tests {
		if got := fileToModulePath(tt.relPath, tt.baseModule); got != tt.want {
			t.Errorf("fileToModulePath(%q, %q) = %q, want %q", tt.relPath, tt.baseModule, got, tt.want)
		}
	}
}
