//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package proto provides protocol buffer definition file reader implementation.
package proto

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/knowledge/document/internal/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	codeproto "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast/proto"
	itransform "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/transform"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

var (
	// supportedExtensions defines the file extensions supported by this reader.
	supportedExtensions = []string{".proto"}
)

// init registers the proto reader with the global registry.
func init() {
	reader.RegisterReader(supportedExtensions, New)
}

// Reader reads protocol buffer definition files and extracts AST-based entities.
type Reader struct {
	chunk        bool
	transformers []transform.Transformer
	parser       *codeproto.Parser
}

// New creates a new proto reader with the given options.
func New(opts ...reader.Option) reader.Reader {
	config := &reader.Config{Chunk: true}
	for _, opt := range opts {
		opt(config)
	}

	return &Reader{
		chunk:        config.Chunk,
		transformers: config.Transformers,
		parser:       codeproto.NewParser(),
	}
}

// ReadFromReader reads proto content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}

	return r.processContent(string(content), name, nil)
}

// ReadFromFile reads a proto file and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".proto" {
		return nil, fmt.Errorf("unsupported file extension: %s", ext)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	baseMetadata := map[string]any{
		source.MetaSource:        source.TypeFile,
		source.MetaFilePath:      filePath,
		source.MetaFileName:      filepath.Base(filePath),
		source.MetaFileExt:       filepath.Ext(filePath),
		source.MetaFileSize:      fileInfo.Size(),
		source.MetaFileMode:      fileInfo.Mode().String(),
		source.MetaModifiedAt:    fileInfo.ModTime().UTC(),
		source.MetaURI:           (&url.URL{Scheme: "file", Path: absPath}).String(),
		source.MetaSourceName:    r.Name(),
		source.MetaContentLength: utf8.RuneCount(content),
	}

	return r.processContent(string(content), filePath, baseMetadata)
}

// ReadFromURL reads proto content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(urlStr string) ([]*document.Document, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read URL content: %w", err)
	}

	return r.processContent(string(content), r.extractFileNameFromURL(urlStr), nil)
}

// processContent processes proto content and extracts AST-based entities.
func (r *Reader) processContent(content, name string, baseMetadata map[string]any) ([]*document.Document, error) {
	if !r.chunk {
		doc := r.createFileDocument(content, name, baseMetadata)
		return r.applyTransformers([]*document.Document{doc})
	}

	result, err := r.parser.ParseContent(name, content)
	if err != nil {
		return nil, err
	}

	docs := r.nodesToDocuments(result, baseMetadata)
	if len(docs) == 0 {
		doc := r.createFileDocument(content, name, baseMetadata)
		return r.applyTransformers([]*document.Document{doc})
	}

	return r.applyTransformers(docs)
}

func (r *Reader) nodesToDocuments(result *codeast.Result, baseMetadata map[string]any) []*document.Document {
	payloads := codeast.NodesToDocumentPayloads(result, codeast.NodeDocumentPayloadOptions{
		BaseMetadata: baseMetadata,
		FileInfo:     result.File,
		FormatType: func(entityType codeast.EntityType) string {
			return strings.ToLower(string(entityType))
		},
		BuildEmbeddingText: codeproto.BuildNodeEmbeddingText,
	})
	docs := make([]*document.Document, 0, len(payloads))
	for _, payload := range payloads {
		docs = append(docs, idocument.CreateDocumentFromPayload(payload))
	}
	return docs
}

func (r *Reader) createFileDocument(content, name string, baseMetadata map[string]any) *document.Document {
	doc := idocument.CreateDocument(content, name)
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]any)
	}
	for k, v := range baseMetadata {
		doc.Metadata[k] = v
	}

	fileMetadata := codeproto.ExtractFileMetadata(content)
	for k, v := range fileMetadata {
		doc.Metadata[codeast.TrpcAstMetaPrefix+k] = v
	}

	doc.Metadata[codeast.TrpcAstMetaPrefix+"type"] = "file"
	doc.Metadata[codeast.TrpcAstMetaPrefix+"name"] = name
	doc.Metadata[codeast.TrpcAstMetaPrefix+"full_name"] = name
	doc.Metadata[codeast.TrpcAstMetaPrefix+"language"] = string(codeast.LanguageProto)
	doc.Metadata[codeast.TrpcAstMetaPrefix+"scope"] = string(codeast.ScopeCode)
	doc.Metadata[codeast.TrpcAstMetaPrefix+"file_path"] = name
	doc.Metadata[source.MetaChunkIndex] = 0
	doc.Metadata[source.MetaChunkSize] = utf8.RuneCountInString(content)
	doc.Metadata[source.MetaContentLength] = utf8.RuneCountInString(content)
	doc.EmbeddingText = codeproto.BuildFileEmbeddingText(content, name, fileMetadata)
	return doc
}

// extractFileNameFromURL extracts a file name from a URL.
func (r *Reader) extractFileNameFromURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		fileName := parts[len(parts)-1]
		fileName = strings.Split(fileName, "?")[0]
		fileName = strings.Split(fileName, "#")[0]
		if fileName == "" {
			return "proto_file"
		}
		fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName))
		return fileName
	}
	return "proto_file"
}

// applyTransformers applies all transformers to the documents.
func (r *Reader) applyTransformers(docs []*document.Document) ([]*document.Document, error) {
	result, err := itransform.ApplyPreprocess(docs, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply preprocess: %w", err)
	}

	result, err = itransform.ApplyPostprocess(result, r.transformers...)
	if err != nil {
		return nil, fmt.Errorf("failed to apply postprocess: %w", err)
	}

	return result, nil
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "Proto Reader"
}

// SupportedExtensions returns the file extensions this reader supports.
func (r *Reader) SupportedExtensions() []string {
	return supportedExtensions
}
