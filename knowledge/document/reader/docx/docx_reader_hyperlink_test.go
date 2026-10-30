//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package docx

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
)

// Chinese text below is test data: it reproduces documents captured from Word
// (product links in a price list) and intentionally verifies localized content.
const testDocxDocumentXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p>
      <w:r><w:t>商品链接：</w:t></w:r>
      <w:hyperlink r:id="rId2" w:history="1">
        <w:r><w:rPr><w:rStyle w:val="ae"/></w:rPr><w:t>https://</w:t></w:r>
        <w:r><w:rPr><w:rStyle w:val="ae"/></w:rPr><w:t>item.jd.com</w:t></w:r>
        <w:r><w:rPr><w:rStyle w:val="ae"/></w:rPr><w:t>/</w:t></w:r>
        <w:r><w:rPr><w:rStyle w:val="ae"/></w:rPr><w:t>100425372996.html</w:t></w:r>
      </w:hyperlink>
    </w:p>
    <w:p>
      <w:r><w:t>商品链接：</w:t></w:r>
      <w:hyperlink r:id="rId3" w:history="1">
        <w:r><w:rPr><w:rStyle w:val="ae"/></w:rPr><w:t>点击打开商品页</w:t></w:r>
      </w:hyperlink>
    </w:p>
    <w:tbl>
      <w:tr>
        <w:tc>
          <w:p>
            <w:r><w:t>表内链接：</w:t></w:r>
            <w:hyperlink r:id="rId4" w:history="1">
              <w:r><w:rPr><w:rStyle w:val="ae"/></w:rPr><w:t>表格里的链接</w:t></w:r>
            </w:hyperlink>
          </w:p>
        </w:tc>
        <w:tc><w:p><w:r><w:t>第二列</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
    <w:p><w:r><w:t>普通段落，无链接。</w:t></w:r></w:p>
  </w:body>
</w:document>`

const testDocxRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
      Target="https://item.jd.com/100425372996.html" TargetMode="External"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
      Target="https://item.jd.com/111111111111.html" TargetMode="External"/>
  <Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
      Target="https://example.com/product/abc" TargetMode="External"/>
</Relationships>`

const testDocxContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml"
      ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

const testDocxPackageRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
      Target="word/document.xml"/>
</Relationships>`

// buildDOCXPackage builds a DOCX package from the given members.
func buildDOCXPackage(t *testing.T, members map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// buildDocxWithHyperlinks builds a minimal DOCX covering the hyperlink shapes
// that Word and LibreOffice commonly produce.
func buildDocxWithHyperlinks(t *testing.T) []byte {
	t.Helper()
	return buildDOCXPackage(t, map[string]string{
		"[Content_Types].xml":          testDocxContentTypesXML,
		"_rels/.rels":                  testDocxPackageRelsXML,
		"word/document.xml":            testDocxDocumentXML,
		"word/_rels/document.xml.rels": testDocxRelsXML,
	})
}

// TestDOCXReader_PreservesHyperlinkURLs guards the two hyperlink defects of the
// old docxlib-based extraction: anchors split across multiple runs were
// truncated, and relationship targets were never read.
func TestDOCXReader_PreservesHyperlinkURLs(t *testing.T) {
	data := buildDocxWithHyperlinks(t)
	rdr := New(reader.WithChunk(false))

	docs, err := rdr.ReadFromReader("hyperlinks", bytes.NewReader(data))
	require.NoError(t, err)
	require.Len(t, docs, 1)

	content := docs[0].Content
	// 1. An anchor split across four w:r elements must be concatenated.
	require.Contains(t, content, "https://item.jd.com/100425372996.html",
		"hyperlink anchor split across runs was truncated, got: %s", content)
	// 2. A descriptive anchor must keep the relationship target URL.
	require.Contains(t, content, "点击打开商品页 (https://item.jd.com/111111111111.html)",
		"hyperlink relationship target was dropped, got: %s", content)
	// 3. Hyperlinks inside table cells must be extracted as well.
	require.Contains(t, content, "表格里的链接 (https://example.com/product/abc)",
		"table hyperlink was dropped, got: %s", content)
	// 4. Ordinary paragraphs must keep their plain text.
	require.Contains(t, content, "普通段落，无链接。")
}

// TestDOCXReader_ExtractionEdgeCases covers tabs, breaks, empty anchors, anchor
// text that already contains the URL, internal anchors, non-http targets,
// tracked deletions, mc:Fallback duplicates and foreign namespaces.
func TestDOCXReader_ExtractionEdgeCases(t *testing.T) {
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
    xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"
    xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>tab</w:t><w:tab/><w:t>end</w:t></w:r></w:p>
    <w:p><w:r><w:t>line1</w:t><w:br/><w:t>line2</w:t><w:cr/><w:t>line3</w:t></w:r></w:p>
    <w:p><w:r><w:t>空锚：</w:t></w:r><w:hyperlink r:id="rId5"/></w:p>
    <w:p><w:r><w:t>已含URL：</w:t></w:r><w:hyperlink r:id="rId6"><w:r><w:t>https://example.com/dup</w:t></w:r></w:hyperlink></w:p>
    <w:p><w:r><w:t>书签：</w:t></w:r><w:hyperlink w:anchor="bookmark1"><w:r><w:t>跳转</w:t></w:r></w:hyperlink></w:p>
    <w:p><w:r><w:t>邮件：</w:t></w:r><w:hyperlink r:id="rId7"><w:r><w:t>发邮件</w:t></w:r></w:hyperlink></w:p>
    <w:p><w:r><w:t>删除前</w:t></w:r><w:del><w:r><w:delText>已删除文本</w:delText></w:r></w:del><w:r><w:t>删除后</w:t></w:r></w:p>
    <w:p><mc:AlternateContent><mc:Choice Requires="wps"><w:r><w:t>选择文本</w:t></w:r></mc:Choice><mc:Fallback><w:r><w:t>降级文本</w:t></w:r></mc:Fallback></mc:AlternateContent></w:p>
    <w:p><w:r><w:t>外文命名空间</w:t></w:r><a:t>绘图文本</a:t></w:p>
  </w:body>
</w:document>`

	relsXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
      Target="https://example.com/empty-anchor" TargetMode="External"/>
  <Relationship Id="rId6" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
      Target="https://example.com/dup" TargetMode="External"/>
  <Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
      Target="mailto:test@example.com" TargetMode="External"/>
</Relationships>`

	data := buildDOCXPackage(t, map[string]string{
		"word/document.xml":            documentXML,
		"word/_rels/document.xml.rels": relsXML,
	})
	rdr := New(reader.WithChunk(false))

	docs, err := rdr.ReadFromReader("edge", bytes.NewReader(data))
	require.NoError(t, err)
	require.Len(t, docs, 1)
	content := docs[0].Content

	require.Contains(t, content, "tab\tend")
	require.Contains(t, content, "line1\nline2\nline3")
	require.Contains(t, content, "空锚：https://example.com/empty-anchor")
	require.Equal(t, 1, strings.Count(content, "https://example.com/dup"),
		"an anchor that already is the URL must not be duplicated, got: %s", content)
	require.Contains(t, content, "书签：跳转")
	require.NotContains(t, content, "mailto:")
	require.Contains(t, content, "删除前删除后")
	require.NotContains(t, content, "已删除文本")
	require.Contains(t, content, "选择文本")
	require.NotContains(t, content, "降级文本")
	require.Contains(t, content, "外文命名空间")
	require.NotContains(t, content, "绘图文本")
}

// TestDOCXReader_MissingRelationships verifies that a package without a
// relationships part still yields the anchor text.
func TestDOCXReader_MissingRelationships(t *testing.T) {
	data := buildDOCXPackage(t, map[string]string{
		"word/document.xml": testDocxDocumentXML,
	})

	docs, err := New(reader.WithChunk(false)).ReadFromReader("no-rels", bytes.NewReader(data))
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Contains(t, docs[0].Content, "商品链接")
}

// TestDOCXReader_BrokenRelationships verifies that malformed relationship data
// only disables URL enrichment, never text extraction.
func TestDOCXReader_BrokenRelationships(t *testing.T) {
	data := buildDOCXPackage(t, map[string]string{
		"word/document.xml":            testDocxDocumentXML,
		"word/_rels/document.xml.rels": "<Relationships><broken",
	})

	docs, err := New(reader.WithChunk(false)).ReadFromReader("broken-rels", bytes.NewReader(data))
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Contains(t, docs[0].Content, "商品链接")
}

// TestDOCXReader_ExtractionErrors covers invalid packages and empty documents.
func TestDOCXReader_ExtractionErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"not_a_zip", []byte("not a zip")},
		{"missing_document_xml", buildDOCXPackage(t, map[string]string{
			"[Content_Types].xml": testDocxContentTypesXML,
		})},
		{"invalid_document_xml", buildDOCXPackage(t, map[string]string{
			"word/document.xml": "<w:document",
		})},
		{"empty_document", buildDOCXPackage(t, map[string]string{
			"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p/></w:body></w:document>`,
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New().ReadFromReader(tt.name, bytes.NewReader(tt.data))
			require.Error(t, err)
		})
	}
}

// failingTransformer lets the tests cover the transform error branches.
type failingTransformer struct{}

func (failingTransformer) Preprocess([]*document.Document) ([]*document.Document, error) {
	return nil, errors.New("preprocess failed")
}

func (failingTransformer) Postprocess([]*document.Document) ([]*document.Document, error) {
	return nil, errors.New("postprocess failed")
}

func (failingTransformer) Name() string { return "FailingTransformer" }

func TestDOCXReader_TransformerErrors(t *testing.T) {
	data := buildDocxWithHyperlinks(t)

	_, err := New(reader.WithChunk(false), reader.WithTransformers(failingTransformer{})).
		ReadFromReader("transform", bytes.NewReader(data))
	require.ErrorContains(t, err, "preprocess failed")
}

// TestDOCXReader_ReadFromURLCases covers the URL entry point.
func TestDOCXReader_ReadFromURLCases(t *testing.T) {
	data := buildDocxWithHyperlinks(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()

	docs, err := New(reader.WithChunk(false)).ReadFromURL(server.URL + "/remote.docx")
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Contains(t, docs[0].Content, "https://item.jd.com/100425372996.html")

	_, err = New(reader.WithChunk(false)).ReadFromURL(server.URL + "/missing")
	require.Error(t, err)

	_, err = New(reader.WithChunk(false)).ReadFromURL("ftp://example.com/a.docx")
	require.Error(t, err)

	_, err = New(reader.WithChunk(false)).ReadFromURL("http://[::1]:namedport/a.docx")
	require.Error(t, err)
}

// TestDOCXReader_ReadFromFileCases covers the file entry point.
func TestDOCXReader_ReadFromFileCases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hyperlinks.docx")
	require.NoError(t, os.WriteFile(path, buildDocxWithHyperlinks(t), 0o644))

	docs, err := New(reader.WithChunk(false)).ReadFromFile(path)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Contains(t, docs[0].Content, "https://item.jd.com/100425372996.html")

	_, err = New(reader.WithChunk(false)).ReadFromFile(filepath.Join(dir, "missing.docx"))
	require.Error(t, err)
}
