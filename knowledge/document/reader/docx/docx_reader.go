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
			// Namespace declarations are tracked per element so mc:Choice
			// requirements can be resolved to URIs instead of prefix spellings.
			extractor.pushNamespaces(token.Attr)
			consumed, err := extractor.handleStart(decoder, token)
			if err != nil {
				return "", fmt.Errorf("failed to decode document.xml: %w", err)
			}
			if consumed {
				// decoder.Skip consumed the matching end element.
				extractor.popNamespaces()
			}
		case xml.EndElement:
			extractor.handleEnd(token)
			extractor.popNamespaces()
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
	// namespaces holds the in-scope xmlns prefix bindings, outermost first.
	// Go's XML decoder reports namespace declarations as attributes but does
	// not keep a binding stack, so they are tracked here.
	namespaces []map[string]string
	// producedBytes counts extracted non-space text and emitted hyperlink URLs
	// so mc:Choice branches can be classified as readable or not.
	producedBytes int
}

// textParagraph is one open w:p element.
type textParagraph struct {
	text strings.Builder
	// boundary reports that a run following this point must be separated from
	// the text already collected (a nested paragraph was merged into it).
	boundary bool
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

// supportedChoiceNamespaces lists the namespaces whose content this extractor
// can read: WordprocessingML and the drawing/extension namespaces that wrap
// WordprocessingML text runs.
//
// mc:Choice requirements are prefix spellings, so they are resolved against the
// in-scope xmlns declarations and compared as URIs; this keeps aliases stable
// and detects prefixes rebound to unreadable namespaces.
var supportedChoiceNamespaces = map[string]bool{
	"http://schemas.openxmlformats.org/wordprocessingml/2006/main":           true,
	"http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing": true,
	"http://schemas.microsoft.com/office/word/2010/wordprocessingDrawing":    true,
	"http://schemas.microsoft.com/office/word/2010/wordprocessingShape":      true,
	"http://schemas.microsoft.com/office/word/2010/wordml":                   true,
	"http://schemas.microsoft.com/office/word/2012/wordml":                   true,
	"http://schemas.microsoft.com/office/word/2015/wordml/symex":             true,
	"http://schemas.microsoft.com/office/word/2016/wordml/cid":               true,
	"http://schemas.microsoft.com/office/word/2018/wordml":                   true,
	"http://schemas.microsoft.com/office/word/2018/wordml/cex":               true,
	"http://schemas.microsoft.com/office/word/2020/wordml/sdtdatahash":       true,
	"http://schemas.microsoft.com/office/word/2023/wordml/word16du":          true,
	"http://schemas.microsoft.com/office/word/2024/wordml/sdtformatlock":     true,
}

// handleStart handles a start element. It reports whether the element subtree
// was consumed with decoder.Skip, in which case the caller must pop the
// namespace scope pushed for the element.
func (e *textExtractor) handleStart(decoder *xml.Decoder, token xml.StartElement) (bool, error) {
	if token.Name.Space == namespaceMarkupCompat {
		return e.handleMarkupCompatStart(decoder, token)
	}
	if token.Name.Space != namespaceWordMain {
		// Other namespaces (drawings, comments, ...) carry no body text.
		return false, nil
	}

	switch token.Name.Local {
	case "del", "moveFrom":
		// Text deleted or moved away by a tracked change must not be indexed.
		return true, decoder.Skip()
	case "p":
		e.paras = append(e.paras, &textParagraph{})
	case "hyperlink":
		e.anchors = append(e.anchors, &hyperlinkAnchor{relID: hyperlinkRelID(token.Attr)})
	case "t":
		var runText string
		if err := decoder.DecodeElement(&runText, &token); err != nil {
			return false, err
		}
		e.writeText(runText)
		// DecodeElement consumed the matching end element.
		return true, nil
	case "tab":
		e.writeText("\t")
	case "br", "cr":
		e.writeText("\n")
	}
	return false, nil
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
func (e *textExtractor) handleMarkupCompatStart(decoder *xml.Decoder, token xml.StartElement) (bool, error) {
	switch token.Name.Local {
	case "AlternateContent":
		e.alternates = append(e.alternates, &alternateContent{})
	case "Choice":
		frame := e.currentAlternate()
		if frame == nil {
			return false, nil
		}
		if frame.supported || !e.choiceSupported(choiceRequires(token.Attr)) {
			return true, decoder.Skip()
		}
		frame.choiceActive = true
		frame.choiceStart = e.producedBytes
	case "Fallback":
		// The fallback is a duplicate of a readable choice, unless no choice
		// produced output: some choices only carry markup this extractor cannot
		// read (for example math), and then the fallback is the only text.
		if frame := e.currentAlternate(); frame != nil && frame.supported {
			return true, decoder.Skip()
		}
	}
	return false, nil
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
	paragraph := e.currentParagraph()
	if paragraph.boundary {
		paragraph.text.WriteString("\n")
		paragraph.boundary = false
	}
	paragraph.text.WriteString(text)
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
		parent.boundary = false
		if parent.text.Len() > 0 {
			parent.text.WriteString("\n")
		}
		parent.text.WriteString(paragraphText)
		// Separate any run that continues in the parent after the nested
		// paragraph, so following text cannot be glued onto this content (or
		// onto a URL appended by an enclosing hyperlink).
		parent.boundary = true
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
	paragraph := e.currentParagraph()
	before := paragraph.text.Len()
	appendHyperlink(
		&paragraph.text,
		strings.TrimSpace(anchor.anchor.String()),
		anchor.relID,
		e.links,
	)
	// A hyperlink target is extracted output too: a choice whose only content
	// is a hyperlink must still count as readable.
	if emitted := paragraph.text.Len() - before; emitted > 0 {
		e.producedBytes += emitted
	}
}

// flushOpenParagraphs writes the remaining paragraphs when the input ends while
// paragraphs are still open.
func (e *textExtractor) flushOpenParagraphs() {
	for len(e.paras) > 0 {
		e.endParagraph()
	}
}

// pushNamespaces records the xmlns declarations of a start element.
func (e *textExtractor) pushNamespaces(attrs []xml.Attr) {
	scope := make(map[string]string)
	for _, attr := range attrs {
		switch {
		case attr.Name.Space == "xmlns":
			scope[attr.Name.Local] = attr.Value
		case attr.Name.Space == "" && attr.Name.Local == "xmlns":
			scope[""] = attr.Value
		}
	}
	e.namespaces = append(e.namespaces, scope)
}

// popNamespaces drops the innermost xmlns scope.
func (e *textExtractor) popNamespaces() {
	if n := len(e.namespaces); n > 0 {
		e.namespaces = e.namespaces[:n-1]
	}
}

// lookupNamespace resolves an XML prefix using the in-scope declarations.
func (e *textExtractor) lookupNamespace(prefix string) (string, bool) {
	for i := len(e.namespaces) - 1; i >= 0; i-- {
		if uri, ok := e.namespaces[i][prefix]; ok {
			return uri, true
		}
	}
	return "", false
}

// choiceSupported reports whether every namespace required by an mc:Choice can
// be read by this extractor.
func (e *textExtractor) choiceSupported(requires string) bool {
	if strings.TrimSpace(requires) == "" {
		return true
	}
	for _, prefix := range strings.Fields(requires) {
		uri, ok := e.lookupNamespace(prefix)
		if !ok || !supportedChoiceNamespaces[uri] {
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
