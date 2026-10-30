//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package docx provides DOCX document reader implementation.
//
// The reader parses OOXML directly instead of relying on docxlib: docxlib folds
// a w:hyperlink into a single run (dropping anchor runs split by Word) and the
// reader never resolves r:id relationships, so hyperlink URLs were lost during
// extraction.
package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	itransform "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/transform"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

var (
	// supportedExtensions defines the file extensions supported by this reader.
	supportedExtensions = []string{".docx", ".doc"}
)

// init registers the DOCX reader with the global registry.
func init() {
	reader.RegisterReader(supportedExtensions, New)
}

// Reader reads DOCX documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
	transformers     []transform.Transformer
}

// New creates a new DOCX reader with the given options.
// DOCX reader uses FixedSizeChunking by default.
func New(opts ...reader.Option) reader.Reader {
	// Build config from options
	config := &reader.Config{
		Chunk: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// Build chunking strategy using the default builder for DOCX
	strategy := reader.BuildChunkingStrategy(config, buildDefaultChunkingStrategy)

	// Create reader from config
	return &Reader{
		chunk:            config.Chunk,
		chunkingStrategy: strategy,
		transformers:     config.Transformers,
	}
}

// buildDefaultChunkingStrategy builds the default chunking strategy for DOCX reader.
// DOCX uses FixedSizeChunking with configurable size and overlap.
func buildDefaultChunkingStrategy(chunkSize, overlap int) chunking.Strategy {
	var opts []chunking.Option
	if chunkSize != 0 {
		opts = append(opts, chunking.WithChunkSize(chunkSize))
	}
	if overlap != 0 {
		opts = append(opts, chunking.WithOverlap(overlap))
	}
	return chunking.NewFixedSizeChunking(opts...)
}

// ReadFromReader reads DOCX content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	data, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	return r.readFromBytes(data, name)
}

// ReadFromFile reads DOCX content from a file path and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Get file name without extension.
	fileName := strings.TrimSuffix(
		filepath.Base(filePath), filepath.Ext(filePath),
	)
	return r.readFromBytes(data, fileName)
}

// ReadFromURL reads DOCX content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(urlStr string) ([]*document.Document, error) {
	// Validate URL to prevent potential security issues.
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid URL scheme: %s", urlStr)
	}

	// Download DOCX from URL.
	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Get file name from URL.
	fileName := r.extractFileNameFromURL(urlStr)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	return r.readFromBytes(data, fileName)
}

// readFromBytes extracts text from DOCX bytes and applies chunking.
func (r *Reader) readFromBytes(data []byte, name string) ([]*document.Document, error) {
	// Parse the DOCX document.
	textContent, err := extractText(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DOCX: %w", err)
	}

	// Create document.
	docResult := idocument.CreateDocument(textContent, name)

	// Apply preprocess.
	docs, err := itransform.ApplyPreprocess([]*document.Document{docResult}, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply preprocess: %w", err)
	}

	// Apply chunking if enabled.
	if r.chunk {
		docs, err = r.chunkDocuments(docs)
		if err != nil {
			return nil, err
		}
	}

	// Apply postprocess.
	docs, err = itransform.ApplyPostprocess(docs, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply postprocess: %w", err)
	}

	return docs, nil
}

// chunkDocuments applies chunking to documents.
func (r *Reader) chunkDocuments(docs []*document.Document) ([]*document.Document, error) {
	if r.chunkingStrategy == nil {
		r.chunkingStrategy = chunking.NewFixedSizeChunking()
	}

	var result []*document.Document
	for _, doc := range docs {
		chunks, err := r.chunkingStrategy.Chunk(doc)
		if err != nil {
			return nil, err
		}
		result = append(result, chunks...)
	}
	return result, nil
}

// extractFileNameFromURL extracts a file name from a URL.
func (r *Reader) extractFileNameFromURL(url string) string {
	// Extract the last part of the URL as the file name.
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		fileName := parts[len(parts)-1]
		// Remove query parameters and fragments.
		if idx := strings.Index(fileName, "?"); idx != -1 {
			fileName = fileName[:idx]
		}
		if idx := strings.Index(fileName, "#"); idx != -1 {
			fileName = fileName[:idx]
		}
		// Remove file extension.
		fileName = strings.TrimSuffix(fileName, ".docx")
		return fileName
	}
	return "docx_document"
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "DOCXReader"
}

// SupportedExtensions returns the file extensions this reader supports.
func (r *Reader) SupportedExtensions() []string {
	return supportedExtensions
}

const (
	// namespaceWordMain is the WordprocessingML main namespace.
	namespaceWordMain = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	// namespaceOfficeRelationships is the namespace of the r:id attribute.
	namespaceOfficeRelationships = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	// namespaceMarkupCompat is the namespace of mc:Fallback.
	namespaceMarkupCompat = "http://schemas.openxmlformats.org/markup-compatibility/2006"
)

// docxRelationships is the minimal structure of word/_rels/document.xml.rels.
// Only ID and Target are relevant for restoring hyperlink URLs.
type docxRelationships struct {
	Items []struct {
		ID         string `xml:"Id,attr"`
		Target     string `xml:"Target,attr"`
		TargetMode string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

// extractText extracts the document body text, keeping the target URL of every
// external hyperlink.
//
// Word splits the anchor text of a hyperlink across several w:r elements (for
// example at spell-check boundaries), so the runs are concatenated in document
// order. The real URL lives in the r:id relationship, not in the anchor text,
// so it is appended as "anchor (URL)" when the anchor does not already contain
// it. Paragraphs inside tables and text boxes are extracted as well.
func extractText(data []byte) (string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("invalid DOCX package: %w", err)
	}

	body, err := openZipMember(zipReader, "word/document.xml")
	if err != nil {
		return "", err
	}
	defer body.Close()

	return decodeDocumentBody(body, readHyperlinkTargets(zipReader))
}

// decodeDocumentBody walks word/document.xml and collects the paragraph text.
func decodeDocumentBody(body io.Reader, links map[string]string) (string, error) {
	extractor := &textExtractor{links: links}
	decoder := xml.NewDecoder(body)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to decode document.xml: %w", err)
		}

		switch token := token.(type) {
		case xml.StartElement:
			if err := extractor.handleStart(decoder, token); err != nil {
				return "", fmt.Errorf("failed to decode document.xml: %w", err)
			}
		case xml.EndElement:
			extractor.handleEnd(token)
		}
	}
	// Keep the extracted text even when the document ends with an unclosed paragraph.
	extractor.flushParagraph()
	return extractor.text.String(), nil
}

// textExtractor is the stateful part of the word/document.xml walk.
type textExtractor struct {
	links map[string]string
	text  strings.Builder // completed paragraphs
	para  strings.Builder // current paragraph
	// Start offset of the hyperlink anchor within the current paragraph, used
	// to decide whether the target URL must be appended.
	linkDepth int
	linkRelID string
	linkStart int
}

// handleStart handles a start element and may consume the whole subtree
// (mc:Fallback / tracked deletions).
func (e *textExtractor) handleStart(decoder *xml.Decoder, token xml.StartElement) error {
	switch {
	case token.Name.Space == namespaceMarkupCompat && token.Name.Local == "Fallback":
		// mc:Fallback duplicates the mc:Choice content, skip it.
		return decoder.Skip()
	case token.Name.Space == namespaceWordMain && token.Name.Local == "del":
		// Text deleted by a tracked change must not be indexed.
		return decoder.Skip()
	case token.Name.Space != namespaceWordMain:
		// Other namespaces (drawings, comments, ...) carry no body text.
		return nil
	}

	switch token.Name.Local {
	case "hyperlink":
		e.linkDepth++
		if e.linkDepth == 1 {
			e.linkRelID = hyperlinkRelID(token.Attr)
			e.linkStart = e.para.Len()
		}
	case "t":
		var runText string
		if err := decoder.DecodeElement(&runText, &token); err != nil {
			return err
		}
		e.para.WriteString(runText)
	case "tab":
		e.para.WriteString("\t")
	case "br", "cr":
		e.para.WriteString("\n")
	}
	return nil
}

// handleEnd handles an end element.
func (e *textExtractor) handleEnd(token xml.EndElement) {
	if token.Name.Space != namespaceWordMain {
		return
	}
	switch token.Name.Local {
	case "p":
		e.flushParagraph()
	case "hyperlink":
		if e.linkDepth == 1 {
			appendHyperlink(&e.para, e.linkStart, e.linkRelID, e.links)
		}
		if e.linkDepth > 0 {
			e.linkDepth--
		}
	}
}

// flushParagraph appends the current paragraph to the extracted text.
func (e *textExtractor) flushParagraph() {
	paragraphText := strings.TrimSpace(e.para.String())
	e.para.Reset()
	if paragraphText == "" {
		return
	}
	if e.text.Len() > 0 {
		e.text.WriteString("\n")
	}
	e.text.WriteString(paragraphText)
}

// readHyperlinkTargets reads word/_rels/document.xml.rels and returns the
// external http(s) URL of every relationship by r:id. A missing or broken
// relationships part only disables URL enrichment, never text extraction.
func readHyperlinkTargets(zipReader *zip.Reader) map[string]string {
	links := make(map[string]string)
	rc, err := openZipMember(zipReader, "word/_rels/document.xml.rels")
	if err != nil {
		return links
	}
	defer rc.Close()
	var rels docxRelationships
	if err := xml.NewDecoder(rc).Decode(&rels); err != nil {
		return links
	}
	for _, rel := range rels.Items {
		// Skip internal targets such as bookmarks.
		if rel.TargetMode != "External" || !isHTTPURL(rel.Target) {
			continue
		}
		links[rel.ID] = rel.Target
	}
	return links
}

// appendHyperlink appends the real URL after the anchor text.
// The URL is not duplicated when the anchor already is the URL.
func appendHyperlink(para *strings.Builder, start int, relID string, links map[string]string) {
	target := links[relID]
	if target == "" {
		return
	}
	anchor := strings.TrimSpace(para.String()[start:])
	if anchor == "" {
		para.WriteString(target)
		return
	}
	if strings.Contains(anchor, target) {
		return
	}
	para.WriteString(" (")
	para.WriteString(target)
	para.WriteString(")")
}

// hyperlinkRelID returns the r:id attribute of a w:hyperlink element.
func hyperlinkRelID(attrs []xml.Attr) string {
	for _, attr := range attrs {
		if attr.Name.Space == namespaceOfficeRelationships && attr.Name.Local == "id" {
			return attr.Value
		}
	}
	return ""
}

// isHTTPURL reports whether raw is an http(s) URL.
func isHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// openZipMember opens the named member of a DOCX package.
func openZipMember(zipReader *zip.Reader, name string) (io.ReadCloser, error) {
	for _, file := range zipReader.File {
		if file.Name == name {
			return file.Open()
		}
	}
	return nil, fmt.Errorf("missing %s in DOCX package", name)
}
