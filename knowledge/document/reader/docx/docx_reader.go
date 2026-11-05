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
// Only ID, Type and Target are relevant for restoring hyperlink URLs.
type docxRelationships struct {
	Items []struct {
		ID         string `xml:"Id,attr"`
		Type       string `xml:"Type,attr"`
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
// so it is appended as "anchor (URL)" when the anchor is not the URL itself.
// Paragraphs inside tables and text boxes are extracted as well.
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
	// Keep the extracted text even when the document ends inside a paragraph.
	extractor.flushOpenParagraphs()
	return extractor.text.String(), nil
}

// textExtractor is the stateful part of the word/document.xml walk.
//
// Word nests w:p elements: a text box inside a drawing is a paragraph inside a
// paragraph. Paragraph text and the hyperlink anchors that belong to it are
// therefore kept on a stack instead of one shared buffer; a nested paragraph
// resetting a shared buffer would make the enclosing hyperlink append its URL
// at the wrong offset.
type textExtractor struct {
	links map[string]string
	text  strings.Builder // completed paragraphs
	// paras holds the open w:p elements, outermost first.
	paras []*textParagraph
	// anchors holds the open w:hyperlink elements, outermost first. The anchor
	// text is collected here so nested paragraphs cannot corrupt it.
	anchors []*hyperlinkAnchor
	// alternates tracks the open mc:AlternateContent elements.
	alternates []*alternateContent
	// producedBytes counts extracted non-space text so mc:Choice branches can
	// be classified as readable or not.
	producedBytes int
}

// textParagraph is one open w:p element.
type textParagraph struct {
	text strings.Builder
}

// hyperlinkAnchor is one open w:hyperlink element.
type hyperlinkAnchor struct {
	relID  string
	anchor strings.Builder
}

// alternateContent tracks one open mc:AlternateContent element. mc:Fallback is
// only read when no mc:Choice produced readable text.
type alternateContent struct {
	supported    bool // a readable mc:Choice has been seen
	choiceActive bool // the current mc:Choice is being read
	choiceStart  int  // producedBytes when the current mc:Choice started
}

// supportedChoicePrefixes lists the markup-compatibility prefixes whose content
// this extractor can read: WordprocessingML and drawing extension namespaces.
//
// Go's XML decoder resolves namespaces to URIs but does not expose the prefix
// bindings, so mc:Choice selection uses this fixed list. Choices that require
// namespaces with no readable text (for example math, "m") are skipped, and
// their mc:Fallback content is used instead.
var supportedChoicePrefixes = map[string]bool{
	"w": true, "wp": true, "wp14": true, "wps": true,
	"w14": true, "w15": true, "w16": true, "w16cex": true, "w16cid": true,
	"w16du": true, "w16sdtdh": true, "w16sdtfl": true, "w16se": true,
}

// handleStart handles a start element and may consume the whole subtree.
func (e *textExtractor) handleStart(decoder *xml.Decoder, token xml.StartElement) error {
	if token.Name.Space == namespaceMarkupCompat {
		return e.handleMarkupCompatStart(decoder, token)
	}
	if token.Name.Space != namespaceWordMain {
		// Other namespaces (drawings, comments, ...) carry no body text.
		return nil
	}

	switch token.Name.Local {
	case "del", "moveFrom":
		// Text deleted or moved away by a tracked change must not be indexed.
		return decoder.Skip()
	case "p":
		e.paras = append(e.paras, &textParagraph{})
	case "hyperlink":
		e.anchors = append(e.anchors, &hyperlinkAnchor{relID: hyperlinkRelID(token.Attr)})
	case "t":
		var runText string
		if err := decoder.DecodeElement(&runText, &token); err != nil {
			return err
		}
		e.writeText(runText)
	case "tab":
		e.writeText("\t")
	case "br", "cr":
		e.writeText("\n")
	}
	return nil
}

// handleEnd handles an end element.
func (e *textExtractor) handleEnd(token xml.EndElement) {
	if token.Name.Space == namespaceMarkupCompat {
		e.handleMarkupCompatEnd(token)
		return
	}
	if token.Name.Space != namespaceWordMain {
		return
	}
	switch token.Name.Local {
	case "p":
		e.endParagraph()
	case "hyperlink":
		e.endHyperlink()
	}
}

// handleMarkupCompatStart selects which mc:Choice branch may be read.
func (e *textExtractor) handleMarkupCompatStart(decoder *xml.Decoder, token xml.StartElement) error {
	switch token.Name.Local {
	case "AlternateContent":
		e.alternates = append(e.alternates, &alternateContent{})
	case "Choice":
		frame := e.currentAlternate()
		if frame == nil {
			return nil
		}
		if frame.supported || !choiceSupported(choiceRequires(token.Attr)) {
			return decoder.Skip()
		}
		frame.choiceActive = true
		frame.choiceStart = e.producedBytes
	case "Fallback":
		// The fallback is a duplicate of a readable choice, unless no choice
		// produced text: some choices only carry markup this extractor cannot
		// read (for example math), and then the fallback is the only text.
		if frame := e.currentAlternate(); frame != nil && frame.supported {
			return decoder.Skip()
		}
	}
	return nil
}

// handleMarkupCompatEnd closes mc:Choice / mc:AlternateContent state.
func (e *textExtractor) handleMarkupCompatEnd(token xml.EndElement) {
	switch token.Name.Local {
	case "AlternateContent":
		if n := len(e.alternates); n > 0 {
			e.alternates = e.alternates[:n-1]
		}
	case "Choice":
		frame := e.currentAlternate()
		if frame == nil || !frame.choiceActive {
			return
		}
		frame.choiceActive = false
		if e.producedBytes > frame.choiceStart {
			frame.supported = true
		}
	}
}

// currentAlternate returns the innermost open mc:AlternateContent frame.
func (e *textExtractor) currentAlternate() *alternateContent {
	if len(e.alternates) == 0 {
		return nil
	}
	return e.alternates[len(e.alternates)-1]
}

// writeText appends text to the current paragraph and to every enclosing
// hyperlink anchor, so anchors that span nested paragraphs stay complete.
func (e *textExtractor) writeText(text string) {
	if text == "" {
		return
	}
	e.currentParagraph().text.WriteString(text)
	for _, anchor := range e.anchors {
		anchor.anchor.WriteString(text)
	}
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		e.producedBytes += len(trimmed)
	}
}

// currentParagraph returns the innermost open paragraph, creating one when a
// run appears outside any w:p (malformed input) so no text is dropped.
func (e *textExtractor) currentParagraph() *textParagraph {
	if len(e.paras) == 0 {
		e.paras = append(e.paras, &textParagraph{})
	}
	return e.paras[len(e.paras)-1]
}

// endParagraph closes the innermost paragraph. Text of a nested paragraph is
// merged into its parent so table and text-box content keeps document order.
func (e *textExtractor) endParagraph() {
	if len(e.paras) == 0 {
		return
	}
	para := e.paras[len(e.paras)-1]
	e.paras = e.paras[:len(e.paras)-1]
	paragraphText := strings.TrimSpace(para.text.String())
	if paragraphText == "" {
		return
	}
	if len(e.paras) > 0 {
		parent := e.paras[len(e.paras)-1]
		if parent.text.Len() > 0 {
			parent.text.WriteString("\n")
		}
		parent.text.WriteString(paragraphText)
		return
	}
	if e.text.Len() > 0 {
		e.text.WriteString("\n")
	}
	e.text.WriteString(paragraphText)
}

// endHyperlink appends the relationship target of the closed hyperlink to the
// paragraph that contains it.
func (e *textExtractor) endHyperlink() {
	if len(e.anchors) == 0 {
		return
	}
	anchor := e.anchors[len(e.anchors)-1]
	e.anchors = e.anchors[:len(e.anchors)-1]
	appendHyperlink(
		&e.currentParagraph().text,
		strings.TrimSpace(anchor.anchor.String()),
		anchor.relID,
		e.links,
	)
}

// flushOpenParagraphs writes the remaining paragraphs when the input ends while
// paragraphs are still open.
func (e *textExtractor) flushOpenParagraphs() {
	for len(e.paras) > 0 {
		e.endParagraph()
	}
}

// choiceSupported reports whether every namespace required by an mc:Choice can
// be read by this extractor.
func choiceSupported(requires string) bool {
	if strings.TrimSpace(requires) == "" {
		return true
	}
	for _, prefix := range strings.Fields(requires) {
		if !supportedChoicePrefixes[prefix] {
			return false
		}
	}
	return true
}

// choiceRequires returns the Requires attribute of an mc:Choice element. The
// attribute is unprefixed, so Go's XML decoder reports an empty namespace for
// it; tolerate the markup-compatibility namespace as well for packages that
// qualify it.
func choiceRequires(attrs []xml.Attr) string {
	for _, attr := range attrs {
		if attr.Name.Local != "Requires" {
			continue
		}
		if attr.Name.Space == "" || attr.Name.Space == namespaceMarkupCompat {
			return attr.Value
		}
	}
	return ""
}

// attrValue returns the value of the namespaced attribute.
func attrValue(attrs []xml.Attr, space, local string) string {
	for _, attr := range attrs {
		if attr.Name.Space == space && attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
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
		// Skip internal targets such as bookmarks, non-hyperlink relationships
		// (a crafted package can point a hyperlink at one) and non-http URLs.
		if rel.TargetMode != "External" || !isHyperlinkRelationship(rel.Type) || !isHTTPURL(rel.Target) {
			continue
		}
		links[rel.ID] = rel.Target
	}
	return links
}

// appendHyperlink appends the relationship target after the anchor text.
// The target is not repeated when the anchor text already is exactly the URL.
func appendHyperlink(paragraph *strings.Builder, anchor, relID string, links map[string]string) {
	target := links[relID]
	if target == "" {
		return
	}
	if anchor == "" {
		paragraph.WriteString(target)
		return
	}
	if anchor == target {
		return
	}
	paragraph.WriteString(" (")
	paragraph.WriteString(target)
	paragraph.WriteString(")")
}

// hyperlinkRelID returns the r:id attribute of a w:hyperlink element.
func hyperlinkRelID(attrs []xml.Attr) string {
	return attrValue(attrs, namespaceOfficeRelationships, "id")
}

// isHyperlinkRelationship reports whether relType is the hyperlink relationship
// type (transitional or strict OOXML).
func isHyperlinkRelationship(relType string) bool {
	return strings.HasSuffix(relType, "/hyperlink")
}

// isHTTPURL reports whether raw is an absolute http(s) URL.
func isHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
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
