//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package python provides Python source file reader implementation.
package python

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
	codepython "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/python/internal/codeast/python"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	itransform "trpc.group/trpc-go/trpc-agent-go/knowledge/internal/transform"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

var supportedExtensions = []string{".py"}

func init() {
	reader.RegisterReader(supportedExtensions, New)
}

// Reader reads Python files and extracts AST-based entities.
type Reader struct {
	chunk        bool
	transformers []transform.Transformer
	parser       *codepython.Parser
}

// New creates a new Python reader with the given options.
func New(opts ...reader.Option) reader.Reader {
	config := &reader.Config{Chunk: true}
	for _, opt := range opts {
		opt(config)
	}
	return &Reader{
		chunk:        config.Chunk,
		transformers: config.Transformers,
		parser:       codepython.NewParser(),
	}
}

// ReadFromReader reads Python content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, rd io.Reader) ([]*document.Document, error) {
	content, err := io.ReadAll(rd)
	if err != nil {
		return nil, err
	}
	return r.processContent(string(content), name, nil)
}

// ReadFromFile reads a Python file and returns a list of AST entity documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".py" {
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
		source.MetaContentLength: utf8.RuneCountInString(string(content)),
	}

	return r.processContent(string(content), filePath, baseMetadata)
}

// ReadFromURL reads Python content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(urlStr string) ([]*document.Document, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid URL scheme: %s", urlStr)
	}

	resp, err := http.Get(parsedURL.String()) //nolint:gosec
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

	return r.processContent(string(content), extractFileNameFromURL(urlStr), nil)
}

// ReadFromDirectory reads a Python package directory and returns AST entity documents.
func (r *Reader) ReadFromDirectory(dirPath string) ([]*document.Document, error) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	stat, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", dirPath)
	}

	baseModule := filepath.Base(absDir)
	baseMetadata := map[string]any{
		source.MetaSource:     source.TypeDir,
		source.MetaSourceName: r.Name(),
	}

	var allDocs []*document.Document
	err = filepath.Walk(absDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".py") || isTestFile(info.Name()) {
			return nil
		}

		relPath, _ := filepath.Rel(absDir, path)
		modulePath := fileToModulePath(relPath, baseModule)

		result, parseErr := r.parser.ParseFileAt(path, modulePath)
		if parseErr != nil {
			return nil
		}
		if result == nil || len(result.Nodes) == 0 {
			return nil
		}

		docs := r.nodesToDocuments(result, baseMetadata)
		allDocs = append(allDocs, docs...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return r.applyTransformers(allDocs)
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "PythonReader"
}

// SupportedExtensions returns the file extensions this reader supports.
func (r *Reader) SupportedExtensions() []string {
	return supportedExtensions
}

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
		doc := r.createFileDocumentFromInfo(content, name, baseMetadata, result.File)
		return r.applyTransformers([]*document.Document{doc})
	}

	return r.applyTransformers(docs)
}

func (r *Reader) nodesToDocuments(result *codeast.Result, baseMetadata map[string]any) []*document.Document {
	payloads := codeast.NodesToDocumentPayloads(result, codeast.NodeDocumentPayloadOptions{
		BaseMetadata:  baseMetadata,
		ScopeBasePath: repoRootFromMetadata(baseMetadata),
		FileInfo:      result.File,
		FormatType: func(entityType codeast.EntityType) string {
			return string(entityType)
		},
		BuildEmbeddingText: codepython.BuildNodeEmbeddingText,
	})
	docs := make([]*document.Document, 0, len(payloads))
	for _, payload := range payloads {
		docs = append(docs, idocument.CreateDocumentFromPayload(payload))
	}
	return docs
}

func (r *Reader) createFileDocument(content, name string, baseMetadata map[string]any) *document.Document {
	fileInfo, err := r.parser.ParseFileInfo(name, content)
	if err != nil {
		return r.createFileDocumentFromInfo(content, name, baseMetadata, nil)
	}
	return r.createFileDocumentFromInfo(content, name, baseMetadata, fileInfo)
}

func (r *Reader) createFileDocumentFromInfo(content, name string, baseMetadata map[string]any, fileInfo *codeast.FileInfo) *document.Document {
	doc := idocument.CreateDocument(content, name)
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]any)
	}
	for k, v := range baseMetadata {
		doc.Metadata[k] = v
	}

	doc.Metadata["trpc_ast_type"] = "file"
	doc.Metadata["trpc_ast_name"] = name
	doc.Metadata["trpc_ast_full_name"] = name
	doc.Metadata["trpc_ast_language"] = "python"
	doc.Metadata["trpc_ast_scope"] = resolveScope(name, baseMetadata)
	doc.Metadata["trpc_ast_file_path"] = name
	if fileInfo != nil {
		if fileInfo.Package != "" {
			doc.Metadata["trpc_ast_package"] = fileInfo.Package
		}
		if len(fileInfo.Imports) > 0 {
			doc.Metadata["trpc_ast_imports"] = append([]string(nil), fileInfo.Imports...)
			doc.Metadata["trpc_ast_import_count"] = len(fileInfo.Imports)
		}
	}
	doc.Metadata[source.MetaChunkIndex] = 0
	doc.Metadata[source.MetaChunkSize] = utf8.RuneCountInString(content)
	doc.Metadata[source.MetaContentLength] = utf8.RuneCountInString(content)

	return doc
}

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

func resolveScope(filePath string, baseMetadata map[string]any) string {
	if codeast.IsExamplePath(filePath, repoRootFromMetadata(baseMetadata)) {
		return string(codeast.ScopeExample)
	}
	return string(codeast.ScopeCode)
}

func repoRootFromMetadata(baseMetadata map[string]any) string {
	if baseMetadata != nil {
		if v, ok := baseMetadata[source.MetaRepoPath]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func extractFileNameFromURL(urlStr string) string {
	parts := strings.Split(urlStr, "/")
	if len(parts) == 0 {
		return "python_file.py"
	}
	fileName := parts[len(parts)-1]
	if idx := strings.Index(fileName, "?"); idx != -1 {
		fileName = fileName[:idx]
	}
	if idx := strings.Index(fileName, "#"); idx != -1 {
		fileName = fileName[:idx]
	}
	if fileName == "" {
		return "python_file.py"
	}
	return fileName
}

func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	skip := map[string]bool{
		"__pycache__": true, "node_modules": true,
		"venv": true, ".venv": true, "env": true,
		"dist": true, "build": true, ".git": true,
	}
	return skip[strings.ToLower(name)]
}

func isTestFile(name string) bool {
	lower := strings.ToLower(name)
	baseName := strings.TrimSuffix(lower, ".py")
	return strings.HasPrefix(baseName, "test_") || strings.HasSuffix(baseName, "_test")
}

func fileToModulePath(relPath, baseModule string) string {
	relPath = strings.TrimSuffix(relPath, ".py")
	modulePath := strings.ReplaceAll(relPath, string(filepath.Separator), ".")
	modulePath = strings.TrimSuffix(modulePath, ".__init__")
	if baseModule != "" {
		return baseModule + "." + modulePath
	}
	return modulePath
}
