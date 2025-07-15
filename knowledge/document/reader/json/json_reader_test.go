//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package json

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestJSONReadFromReaderNoChunk(t *testing.T) {
	data := `{"name":"Alice","age":30}`

	rdr := New(
		WithChunking(false),
	)

	docs, err := rdr.ReadFromReader("person", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	// jsonToText should pretty-print JSON, so content will have newlines and spaces.
	if !strings.Contains(docs[0].Content, "\"name\": \"Alice\"") {
		t.Errorf("pretty JSON content mismatch: %s", docs[0].Content)
	}
	if rdr.Name() != "JSONReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestJSONReader_FileAndURL(t *testing.T) {
	data := `{"k":"v"}`

	tmp, err := os.CreateTemp(t.TempDir(), "*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString(data)
	tmp.Close()

	rdr := New()

	docs, err := rdr.ReadFromFile(tmp.Name())
	if err != nil || len(docs) != 1 {
		t.Fatalf("ReadFromFile error: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(data))
	}))
	defer srv.Close()

	docsURL, err := rdr.ReadFromURL(srv.URL + "/conf.json")
	if err != nil || len(docsURL) != 1 {
		t.Fatalf("ReadFromURL error: %v", err)
	}
	if docsURL[0].Name != "conf" {
		t.Errorf("expected extracted name conf, got %s", docsURL[0].Name)
	}
}

// mockChunker returns err to ensure chunkDocument path executed.
type errChunker struct{}

func (e errChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("fail")
}

func TestJSONReader_jsonToTextError(t *testing.T) {
	rdr := New()
	_, err := rdr.jsonToText("{invalid json}")
	if err == nil {
		t.Fatalf("expected error for invalid json")
	}
}

func TestJSONReader_CustomChunkerError(t *testing.T) {
	rdr := New(WithChunkingStrategy(errChunker{}))
	// use simple valid json
	docs, err := rdr.ReadFromReader("n", strings.NewReader(`{"a":1}`))
	if err == nil {
		t.Fatalf("expected chunker error, got nil")
	}
	if docs != nil {
		t.Fatalf("expected nil docs on error")
	}
	_ = http.MethodGet // silence unused import due to build tags
}
