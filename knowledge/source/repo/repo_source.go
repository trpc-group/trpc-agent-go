//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package repo provides repository-based knowledge source implementation.
package repo

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	codegolang "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/golang"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	isource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/internal/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

const defaultRepoSourceName = "Repository Source"

// Source represents a knowledge source for a code repository.
type Source struct {
	inputs                 []string
	name                   string
	metadata               map[string]any
	readers                map[string]reader.Reader
	fileExtensions         []string
	recursive              bool
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	ocrExtractor           ocr.Extractor
	transformers           []transform.Transformer
	fileReaderType         source.FileReaderType
	skipDirs               []string
	skipSuffixes           []string
	branch                 string
	commit                 string
	repoName               string
	repoURL                string
	subdir                 string
}

// New creates a new repository knowledge source.
func New(inputs []string, opts ...Option) *Source {
	s := &Source{
		inputs:        inputs,
		name:          defaultRepoSourceName,
		metadata:      make(map[string]any),
		recursive:     true,
		skipDirs:      []string{".git"},
		skipSuffixes:  []string{".pb.go", ".trpc.go", "_mock.go"},
		chunkSize:     0,
		chunkOverlap:  0,
		fileExtensions: nil,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.initializeReaders()
	return s
}

func (s *Source) initializeReaders() {
	var readerOpts []isource.ReaderOption
	if s.chunkSize > 0 {
		readerOpts = append(readerOpts, isource.WithChunkSize(s.chunkSize))
	}
	if s.chunkOverlap > 0 {
		readerOpts = append(readerOpts, isource.WithChunkOverlap(s.chunkOverlap))
	}
	if s.customChunkingStrategy != nil {
		readerOpts = append(readerOpts, isource.WithCustomChunkingStrategy(s.customChunkingStrategy))
	}
	if s.ocrExtractor != nil {
		readerOpts = append(readerOpts, isource.WithOCRExtractor(s.ocrExtractor))
	}
	if len(s.transformers) > 0 {
		readerOpts = append(readerOpts, isource.WithTransformers(s.transformers...))
	}
	s.readers = isource.GetReaders(readerOpts...)
}

// ReadDocuments reads all repository inputs and returns documents.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.inputs) == 0 {
		return nil, nil
	}

	var allDocuments []*document.Document
	for _, input := range s.inputs {
		repoRoot, repoInfo, cleanup, err := s.resolveRepository(ctx, input)
		if err != nil {
			return nil, err
		}
		if cleanup != nil {
			defer cleanup()
		}

		rootToScan := repoRoot
		if s.subdir != "" {
			rootToScan = filepath.Join(repoRoot, filepath.Clean(s.subdir))
		}

		filePaths, err := s.getFilePaths(repoRoot, rootToScan)
		if err != nil {
			return nil, err
		}
		for _, filePath := range filePaths {
			docs, err := s.processFile(filePath, repoRoot, repoInfo)
			if err != nil {
				return nil, err
			}
			allDocuments = append(allDocuments, docs...)
		}
	}
	return allDocuments, nil
}

// Name returns the source name.
func (s *Source) Name() string { return s.name }

// Type returns the source type.
func (s *Source) Type() string { return source.TypeRepo }

// GetMetadata returns source metadata.
func (s *Source) GetMetadata() map[string]any {
	result := make(map[string]any)
	for k, v := range s.metadata {
		result[k] = v
	}
	return result
}

type repoInfo struct {
	name   string
	url    string
	branch string
}

func (s *Source) resolveRepository(ctx context.Context, input string) (string, *repoInfo, func(), error) {
	if looksLikeGitURL(input) {
		tmpDir, err := os.MkdirTemp("", "trpc-agent-go-repo-*")
		if err != nil {
			return "", nil, nil, fmt.Errorf("failed to create temp dir: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(tmpDir) }
		if err := runGit(ctx, "", "clone", "--depth", "1", input, tmpDir); err != nil {
			cleanup()
			return "", nil, nil, fmt.Errorf("failed to clone repo: %w", err)
		}
		if s.branch != "" {
			if err := runGit(ctx, tmpDir, "checkout", s.branch); err != nil {
				cleanup()
				return "", nil, nil, fmt.Errorf("failed to checkout branch: %w", err)
			}
		}
		if s.commit != "" {
			if err := runGit(ctx, tmpDir, "checkout", s.commit); err != nil {
				cleanup()
				return "", nil, nil, fmt.Errorf("failed to checkout commit: %w", err)
			}
		}
		return tmpDir, &repoInfo{name: chooseRepoName(s.repoName, input, tmpDir), url: chooseRepoURL(s.repoURL, input), branch: chooseBranch(s.branch)}, cleanup, nil
	}

	absPath, err := filepath.Abs(input)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	stat, err := os.Stat(absPath)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to stat repository path: %w", err)
	}
	if !stat.IsDir() {
		return "", nil, nil, fmt.Errorf("repository input must be a directory or git URL: %s", input)
	}
	return absPath, &repoInfo{name: chooseRepoName(s.repoName, absPath, absPath), url: chooseRepoURL(s.repoURL, ""), branch: chooseBranch(s.branch)}, nil, nil
}

func (s *Source) getFilePaths(repoRoot, dirPath string) ([]string, error) {
	var filePaths []string
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != dirPath {
				if !s.recursive {
					return filepath.SkipDir
				}
				if s.shouldSkipDir(info.Name()) {
					return filepath.SkipDir
				}
				if path != repoRoot {
					if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if s.shouldSkipFile(info.Name()) {
			return nil
		}
		if len(s.fileExtensions) > 0 {
			ext := strings.ToLower(filepath.Ext(path))
			matched := false
			for _, allowedExt := range s.fileExtensions {
				if ext == allowedExt {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}
		filePaths = append(filePaths, path)
		return nil
	})
	return filePaths, err
}

func (s *Source) processFile(filePath, repoRoot string, info *repoInfo) ([]*document.Document, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	fileType := isource.ResolveFileType(string(s.fileReaderType), isource.GetFileType(filePath))
	r, exists := s.readers[fileType]
	if !exists {
		return nil, fmt.Errorf("no reader available for file type: %s", fileType)
	}
	var documents []*document.Document
	if goReader, ok := r.(*codegolang.Reader); ok {
		documents, err = goReader.ReadFromDirectory(filepath.Dir(filePath))
	} else {
		documents, err = r.ReadFromFile(filePath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read file with reader: %w", err)
	}
	relPath, err := filepath.Rel(repoRoot, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to build repo-relative path: %w", err)
	}
	relPath = filepath.ToSlash(relPath)
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	fileURL := (&url.URL{Scheme: "file", Path: absPath}).String()

	metadata := make(map[string]any)
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeRepo
	metadata[source.MetaFilePath] = relPath
	metadata[source.MetaRepoPath] = repoRoot
	metadata[source.MetaFileName] = filepath.Base(filePath)
	metadata[source.MetaFileExt] = filepath.Ext(filePath)
	metadata[source.MetaFileSize] = fileInfo.Size()
	metadata[source.MetaFileMode] = fileInfo.Mode().String()
	metadata[source.MetaModifiedAt] = fileInfo.ModTime().UTC()
	metadata[source.MetaURI] = fileURL
	metadata[source.MetaSourceName] = s.name
	if info != nil {
		if info.name != "" {
			metadata[source.MetaRepoName] = info.name
		}
		if info.url != "" {
			metadata[source.MetaRepoURL] = info.url
		}
		if info.branch != "" {
			metadata[source.MetaBranch] = info.branch
		}
	}

	for _, doc := range documents {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		for k, v := range metadata {
			doc.Metadata[k] = v
		}
		doc.Metadata["trpc_ast_file_path"] = relPath
		if doc.Metadata["trpc_ast_type"] == "file" {
			doc.Metadata["trpc_ast_name"] = relPath
			doc.Metadata["trpc_ast_full_name"] = relPath
		}
	}
	return documents, nil
}

func (s *Source) shouldSkipDir(name string) bool {
	for _, dir := range s.skipDirs {
		if name == dir {
			return true
		}
	}
	return false
}

func (s *Source) shouldSkipFile(name string) bool {
	for _, suffix := range s.skipSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func looksLikeGitURL(input string) bool {
	if strings.HasPrefix(input, "git@") || strings.HasPrefix(input, "ssh://") {
		return true
	}
	if u, err := url.Parse(input); err == nil {
		return u.Scheme == "http" || u.Scheme == "https"
	}
	return false
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func chooseRepoName(explicit, rawInput, fallbackPath string) string {
	if explicit != "" {
		return explicit
	}
	trimmed := strings.TrimSuffix(rawInput, ".git")
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed != "" {
		if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
			return trimmed[idx+1:]
		}
		if idx := strings.LastIndex(trimmed, ":"); idx >= 0 {
			return trimmed[idx+1:]
		}
	}
	return filepath.Base(fallbackPath)
}

func chooseRepoURL(explicit, input string) string {
	if explicit != "" {
		return explicit
	}
	if looksLikeGitURL(input) {
		return input
	}
	return ""
}

func chooseBranch(branch string) string {
	if branch != "" {
		return branch
	}
	return "HEAD"
}
