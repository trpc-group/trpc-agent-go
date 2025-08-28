//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package text

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestTextReader_Read_NoChunk(t *testing.T) {
	data := "Hello world!"

	rdr := New(
		WithChunking(false),
	)

	docs, err := rdr.ReadFromReader("greeting", strings.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	if docs[0].Content != data {
		t.Errorf("content mismatch")
	}
	if rdr.Name() != "TextReader" {
		t.Errorf("unexpected reader name")
	}
}

func TestTextReader_FileAndURL(t *testing.T) {
	data := "sample content"

	tmp, _ := os.CreateTemp(t.TempDir(), "*.txt")
	tmp.WriteString(data)
	tmp.Close()

	rdr := New()

	docs, err := rdr.ReadFromFile(tmp.Name())
	if err != nil || len(docs) != 1 {
		t.Fatalf("ReadFromFile err %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(data)) }))
	defer srv.Close()

	docsURL, err := rdr.ReadFromURL(srv.URL + "/a.txt")
	if err != nil || len(docsURL) != 1 {
		t.Fatalf("ReadFromURL err %v", err)
	}
	if docsURL[0].Name != "a" {
		t.Errorf("expected name a got %s", docsURL[0].Name)
	}
}

type failChunker struct{}

func (failChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("fail")
}

func TestTextReader_ChunkError(t *testing.T) {
	rdr := New(WithChunkingStrategy(failChunker{}))
	_, err := rdr.ReadFromReader("x", strings.NewReader("abc"))
	if err == nil {
		t.Fatalf("want error")
	}
}
