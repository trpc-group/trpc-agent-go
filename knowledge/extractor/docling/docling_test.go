//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package docling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
)

func newMockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestNew(t *testing.T) {
	ext := New()
	assert.Equal(t, defaultEndpoint, ext.opts.endpoint)
	assert.Equal(t, defaultTimeout, ext.opts.timeout)
	assert.True(t, ext.opts.ocrEnabled)
	assert.Equal(t, defaultFormats, ext.opts.formats)

	ext = New(
		WithEndpoint("http://custom:8080"),
		WithTimeout(30*time.Second),
		WithOCR(false),
		WithFormats([]string{".pdf"}),
		WithImageRefMode(ImageRefModePlaceholder),
	)
	assert.Equal(t, "http://custom:8080", ext.opts.endpoint)
	assert.Equal(t, 30*time.Second, ext.opts.timeout)
	assert.False(t, ext.opts.ocrEnabled)
	assert.Equal(t, []string{".pdf"}, ext.opts.formats)
	assert.Equal(t, ImageRefModePlaceholder, ext.opts.imageRefMode)
}

func TestNew_CustomHTTPClient(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	ext := New(WithHTTPClient(client))
	assert.Equal(t, client, ext.opts.httpClient)
}

func TestSupportedFormats(t *testing.T) {
	ext := New()
	formats := ext.SupportedFormats()
	assert.Contains(t, formats, ".pdf")
	assert.Contains(t, formats, ".docx")
	assert.Contains(t, formats, ".pptx")
	assert.Contains(t, formats, ".png")
}

func TestClose(t *testing.T) {
	ext := New()
	assert.NoError(t, ext.Close())
}

func TestExtract_Success(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, convertFilePath, r.URL.Path)
		assert.Contains(t, r.Header.Get("Content-Type"), "multipart/form-data")

		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)

		file, _, err := r.FormFile("files")
		require.NoError(t, err)
		defer file.Close()

		data, err := io.ReadAll(file)
		require.NoError(t, err)
		assert.Equal(t, "fake pdf content", string(data))
		assert.Equal(t, "md", r.FormValue("to_formats"))
		assert.Equal(t, "placeholder", r.FormValue("image_export_mode"))

		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# Extracted Title\n\nSome content here."
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	result, err := ext.Extract(context.Background(), []byte("fake pdf content"))
	require.NoError(t, err)
	assert.Equal(t, extractor.FormatMarkdown, result.Format)

	content, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	assert.Equal(t, "# Extracted Title\n\nSome content here.", string(content))
}

func TestExtractFromReader_Success(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# From Reader"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	result, err := ext.ExtractFromReader(context.Background(), strings.NewReader("reader content"))
	require.NoError(t, err)
	assert.Equal(t, extractor.FormatMarkdown, result.Format)

	content, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	assert.Equal(t, "# From Reader", string(content))
}

func TestExtract_WithOutputFormat(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)

		assert.Equal(t, "text", r.FormValue("to_formats"))

		resp := convertDocumentResponse{}
		resp.Document.TextContent = "plain text content"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	result, err := ext.Extract(context.Background(), []byte("data"),
		extractor.WithOutputFormat(extractor.FormatText))
	require.NoError(t, err)
	assert.Equal(t, extractor.FormatText, result.Format)
	content, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	assert.Equal(t, "plain text content", string(content))
}

func TestExtract_WithOCRDisabled(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)

		assert.Equal(t, "false", r.FormValue("do_ocr"))

		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# No OCR"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL), WithOCR(false))
	result, err := ext.Extract(context.Background(), []byte("data"))
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestExtract_ServerError(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	_, err := ext.Extract(context.Background(), []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "docling returned status 500")
}

func TestExtract_EmptyContent(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := convertDocumentResponse{}
		resp.Document.MdContent = ""
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	_, err := ext.Extract(context.Background(), []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty content")
}

func TestExtract_InvalidJSON(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	_, err := ext.Extract(context.Background(), []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestExtract_ContextCancelled(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# Late"
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := ext.Extract(ctx, []byte("data"))
	assert.Error(t, err)
}

func TestExtract_ConnectionRefused(t *testing.T) {
	ext := New(WithEndpoint("http://localhost:1"))
	_, err := ext.Extract(context.Background(), []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "docling request failed")
}

func TestExtract_ImageExportModeFormField(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)

		// image_export_mode should be a separate form field, not inside options JSON.
		assert.Equal(t, "placeholder", r.FormValue("image_export_mode"))

		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# Placeholder Mode"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	result, err := ext.Extract(context.Background(), []byte("data"))
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestExtract_ImageExportModeEmbedded(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)

		assert.Equal(t, "embedded", r.FormValue("image_export_mode"))

		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# Embedded Mode"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL), WithImageRefMode(ImageRefModeEmbedded))
	result, err := ext.Extract(context.Background(), []byte("data"))
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestBuildPipelineOptions(t *testing.T) {
	tests := []struct {
		name     string
		ext      *Extractor
		eopts    *extractor.Options
		contains []string
	}{
		{
			name:  "default options",
			ext:   New(),
			eopts: &extractor.Options{},
			contains: []string{
				`"to_formats":["md"]`,
				`"image_export_mode":"placeholder"`,
			},
		},
		{
			name:  "OCR disabled",
			ext:   New(WithOCR(false)),
			eopts: &extractor.Options{},
			contains: []string{
				`"do_ocr":false`,
				`"to_formats":["md"]`,
			},
		},
		{
			name:  "text output format",
			ext:   New(),
			eopts: &extractor.Options{OutputFormat: extractor.FormatText},
			contains: []string{
				`"to_formats":["text"]`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(buildConvertOptions(tt.ext.opts, tt.eopts))
			require.NoError(t, err)
			result := string(data)
			for _, s := range tt.contains {
				assert.Contains(t, result, s)
			}
		})
	}
}

// Verify that Extractor implements the extractor.Extractor interface at compile time.
var _ extractor.Extractor = (*Extractor)(nil)

// TestDecodeConvertResponse_ConversionFailed verifies error when status is not "success".
func TestDecodeConvertResponse_ConversionFailed(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := convertDocumentResponse{
			Status: "failure",
			Errors: []struct {
				ErrorMessage string `json:"error_message"`
			}{{ErrorMessage: "conversion error detail"}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	_, err := ext.Extract(context.Background(), []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conversion error detail")
}

// TestDecodeConvertResponse_ConversionFailedNoErrors verifies error when status is not "success" and no error messages.
func TestDecodeConvertResponse_ConversionFailedNoErrors(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := convertDocumentResponse{
			Status: "pending",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	_, err := ext.Extract(context.Background(), []byte("data"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pending")
}

// TestPickOutputContent_TextFallbackToMd verifies that text format falls back to md when TextContent is empty.
func TestPickOutputContent_TextFallbackToMd(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# Fallback MD"
		resp.Document.TextContent = "" // empty text, should fall back to md
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	result, err := ext.Extract(context.Background(), []byte("data"),
		extractor.WithOutputFormat(extractor.FormatText))
	require.NoError(t, err)
	// Falls back to markdown format
	assert.Equal(t, extractor.FormatMarkdown, result.Format)
	content, _ := io.ReadAll(result.Reader)
	assert.Equal(t, "# Fallback MD", string(content))
}

// TestPickOutputContent_MdFallbackToText verifies that md format falls back to text when MdContent is empty.
func TestPickOutputContent_MdFallbackToText(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := convertDocumentResponse{}
		resp.Document.MdContent = "" // empty md, should fall back to text
		resp.Document.TextContent = "plain text fallback"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	ext := New(WithEndpoint(server.URL))
	result, err := ext.Extract(context.Background(), []byte("data"))
	require.NoError(t, err)
	assert.Equal(t, extractor.FormatText, result.Format)
	content, _ := io.ReadAll(result.Reader)
	assert.Equal(t, "plain text fallback", string(content))
}

// TestWriteFileRequest_MultipleFormats verifies multiple to_formats fields are written.
func TestWriteFileRequest_MultipleFormats(t *testing.T) {
	server := newMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		require.NoError(t, err)
		// Verify do_ocr is not set when OCR is enabled (default)
		assert.Empty(t, r.FormValue("do_ocr"))
		resp := convertDocumentResponse{}
		resp.Document.MdContent = "# OK"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer server.Close()

	// OCR enabled (default) - do_ocr field should NOT be written
	ext := New(WithEndpoint(server.URL), WithOCR(true))
	result, err := ext.Extract(context.Background(), []byte("data"))
	require.NoError(t, err)
	assert.NotNil(t, result)
}
