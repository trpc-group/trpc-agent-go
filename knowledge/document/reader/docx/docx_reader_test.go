//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package docx

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	godocx "github.com/gomutex/godocx"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// createDocx creates a simple DOCX file with given text and returns its bytes.
func createDocx(t *testing.T, text string) []byte {
	t.Helper()

	doc, err := godocx.NewDocument()
	require.NoError(t, err)

	doc.AddParagraph(text)

	var buf bytes.Buffer
	_, err = doc.WriteTo(&buf)
	require.NoError(t, err)

	return buf.Bytes()
}

func TestReader_ReadFromReader_NoChunk(t *testing.T) {
	data := createDocx(t, "Hello Docx")
	rdr := New(WithChunking(false))

	docs, err := rdr.ReadFromReader("example", bytes.NewReader(data))
	require.NoError(t, err)
	require.NotEmpty(t, docs)
	require.Contains(t, docs[0].Content, "Hello Docx")
}

type errChunker struct{}

func (errChunker) Chunk(doc *document.Document) ([]*document.Document, error) {
	return nil, errors.New("chunk fail")
}

func TestReader_ReadFile_ChunkError(t *testing.T) {
	data := createDocx(t, "File Mode")
	tmp, _ := os.CreateTemp(t.TempDir(), "*.docx")
	tmp.Write(data)
	tmp.Close()

	// No chunking.
	rdr := New(WithChunking(false))
	docs, err := rdr.ReadFromFile(tmp.Name())
	require.NoError(t, err)
	require.Len(t, docs, 1)

	// Trigger chunk error path.
	rdrErr := New(WithChunkingStrategy(errChunker{}))
	_, err = rdrErr.ReadFromFile(tmp.Name())
	if err == nil {
		t.Fatalf("expected chunk error")
	}
}

func TestReader_ReadFromURL(t *testing.T) {
	data := createDocx(t, "URL Mode")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Write(data)
	}))
	defer srv.Close()

	rdr := New(WithChunking(false))
	docs, err := rdr.ReadFromURL(srv.URL + "/my.docx")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "my", docs[0].Name)
}
