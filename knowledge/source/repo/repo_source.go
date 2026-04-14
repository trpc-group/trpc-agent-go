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
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	isource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/internal/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

const defaultRepoSourceName = "Repository Source"

// Source represents a knowledge source for a code repository.
type Source struct {
	repositories   []Repository
	inputs         []string
	dirs           []string
	repoURLs       []string
	name           string
	metadata       map[string]any
	readers        map[string]reader.Reader
	fileExtensions []string
	recursive      bool
	transformers   []transform.Transformer
	skipDirs       []string
	skipSuffixes   []string
	branch         string
	tag            string
	commit         string
	repoName       string
	repoURL        string
	subdir         string
}

// Repository describes one repository input and its version/scope configuration.
type Repository struct {
	URL      string
	Dir      string
	Branch   string
	Tag      string
	Commit   string
	Subdir   string
	RepoName string
	RepoURL  string
}

// New creates a new repository knowledge source.
func New(inputs []string, opts ...Option) *Source {
	s := &Source{
		inputs:    inputs,
		name:      defaultRepoSourceName,
		metadata:  make(map[string]any),
		recursive: true,
		skipDirs:  []string{".git"},
	}
	for _, opt := range opts {
		opt(s)
	}
	s.initializeReaders()
	return s
}

func (s *Source) initializeReaders() {
	var readerOpts []isource.ReaderOption
	if len(s.transformers) > 0 {
		readerOpts = append(readerOpts, isource.WithTransformers(s.transformers...))
	}
	s.readers = isource.GetReaders(readerOpts...)
}

// ReadDocuments reads all repository inputs and returns documents.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	repositories := s.resolvedRepositories()
	if len(repositories) == 0 {
		return nil, nil
	}
	if len(repositories) > 1 {
		return nil, fmt.Errorf("repo source supports only one repository per source, got %d", len(repositories))
	}

	repository := repositories[0]
	repoRoot, repoInfo, cleanup, err := s.resolveRepository(ctx, repository)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	subdir := repository.Subdir
	if subdir == "" {
		subdir = s.subdir
	}
	rootToScan, err := resolveScanRoot(repoRoot, subdir)
	if err != nil {
		return nil, err
	}

	filePaths, err := s.getFilePaths(rootToScan)
	if err != nil {
		return nil, err
	}

	fc, err := s.classifyFiles(repoRoot, filePaths)
	if err != nil {
		return nil, err
	}

	var allDocuments []*document.Document

	// Step 1: directory-level language parsers (e.g. Go).
	for _, fileType := range fc.dirTypes {
		docs, err := s.processDirectory(rootToScan, fileType, repoRoot, repoInfo, fc.allowedByType[fileType])
		if err != nil {
			return nil, err
		}
		allDocuments = append(allDocuments, docs...)
	}

	// Step 2: code language file parsers (e.g. Proto) – before plain-text readers.
	for _, filePath := range fc.codeFiles {
		docs, err := s.processFile(filePath, repoRoot, repoInfo)
		if err != nil {
			return nil, err
		}
		allDocuments = append(allDocuments, docs...)
	}

	// Step 3: plain-text / doc file readers (e.g. md, txt).
	for _, filePath := range fc.textFiles {
		docs, err := s.processFile(filePath, repoRoot, repoInfo)
		if err != nil {
			return nil, err
		}
		allDocuments = append(allDocuments, docs...)
	}

	return allDocuments, nil
}

// fileClassification groups file paths by processing priority, matching trpc-ast-rag order:
// directory-level parsers first, code-file parsers second, plain-text readers last.
type fileClassification struct {
	dirTypes      []string                       // file types with directoryReader (e.g. "go"), sorted
	codeFiles     []string                       // code language files (e.g. .proto), sorted
	textFiles     []string                       // plain-text/doc files (e.g. .md, .txt), sorted
	allowedByType map[string]map[string]struct{} // allowed repo-relative paths per fileType
}

func resolveScanRoot(repoRoot, subdir string) (string, error) {
	if subdir == "" {
		return repoRoot, nil
	}

	cleanedSubdir := filepath.Clean(subdir)
	if filepath.IsAbs(cleanedSubdir) {
		return "", fmt.Errorf("subdir must be relative to repository root: %s", subdir)
	}

	scanRoot := filepath.Join(repoRoot, cleanedSubdir)
	relPath, err := filepath.Rel(repoRoot, scanRoot)
	if err != nil {
		return "", fmt.Errorf("failed to resolve subdir %q: %w", subdir, err)
	}
	relPath = filepath.Clean(relPath)
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("subdir escapes repository root: %s", subdir)
	}
	return scanRoot, nil
}

func (s *Source) classifyFiles(repoRoot string, filePaths []string) (*fileClassification, error) {
	fc := &fileClassification{
		allowedByType: make(map[string]map[string]struct{}),
	}
	dirTypeSeen := make(map[string]struct{})

	for _, filePath := range filePaths {
		fileType := isource.GetFileType(filePath)
		r, exists := s.readers[fileType]
		if !exists {
			return nil, fmt.Errorf("no reader available for file type: %s", fileType)
		}

		relPath, err := filepath.Rel(repoRoot, filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to build repo-relative path: %w", err)
		}
		relPath = filepath.ToSlash(relPath)
		if fc.allowedByType[fileType] == nil {
			fc.allowedByType[fileType] = make(map[string]struct{})
		}
		fc.allowedByType[fileType][relPath] = struct{}{}

		if _, ok := r.(directoryReader); ok {
			if _, seen := dirTypeSeen[fileType]; !seen {
				dirTypeSeen[fileType] = struct{}{}
				fc.dirTypes = append(fc.dirTypes, fileType)
			}
			continue
		}
		if isCodeFileType(fileType) {
			fc.codeFiles = append(fc.codeFiles, filePath)
		} else {
			fc.textFiles = append(fc.textFiles, filePath)
		}
	}

	slices.Sort(fc.dirTypes)
	slices.Sort(fc.codeFiles)
	slices.Sort(fc.textFiles)
	return fc, nil
}

func (s *Source) resolvedRepositories() []Repository {
	if len(s.repositories) > 0 {
		return slices.Clone(s.repositories)
	}
	inputs := s.resolvedInputs()
	repositories := make([]Repository, 0, len(inputs))
	for _, input := range inputs {
		repo := Repository{
			Branch:   s.branch,
			Tag:      s.tag,
			Commit:   s.commit,
			Subdir:   s.subdir,
			RepoName: s.repoName,
			RepoURL:  s.repoURL,
		}
		if looksLikeGitURL(input) {
			repo.URL = input
		} else {
			repo.Dir = input
		}
		repositories = append(repositories, repo)
	}
	return repositories
}

func (s *Source) resolvedInputs() []string {
	if len(s.dirs) == 0 && len(s.repoURLs) == 0 {
		return slices.Clone(s.inputs)
	}
	return append(slices.Clone(s.repoURLs), s.dirs...)
}

// Name returns the source name.
func (s *Source) Name() string { return s.name }

// Type returns the source type.
func (s *Source) Type() string { return source.TypeRepo }

// GetMetadata returns source metadata.
func (s *Source) GetMetadata() map[string]any {
	return maps.Clone(s.metadata)
}

type repoInfo struct {
	name   string
	url    string
	branch string
}

type checkoutTargetKind string

const (
	checkoutTargetDefault checkoutTargetKind = "default"
	checkoutTargetBranch  checkoutTargetKind = "branch"
	checkoutTargetTag     checkoutTargetKind = "tag"
	checkoutTargetCommit  checkoutTargetKind = "commit"
)

type directoryReader interface {
	ReadFromDirectory(dirPath string) ([]*document.Document, error)
}

// isCodeFileType returns true for file types handled by language/code parsers
// (but not via directoryReader). These run before plain-text file readers to
// match trpc-ast-rag's processing order: language parsers first, text readers last.
func isCodeFileType(fileType string) bool {
	switch fileType {
	case "proto":
		return true
	default:
		return false
	}
}

func (s *Source) resolveRepository(ctx context.Context, repository Repository) (string, *repoInfo, func(), error) {
	if repository.URL != "" {
		tmpDir, err := os.MkdirTemp("", "trpc-agent-go-repo-*")
		if err != nil {
			return "", nil, nil, fmt.Errorf("failed to create temp dir: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(tmpDir) }
		target, err := cloneRemoteRepository(ctx, repository, tmpDir)
		if err != nil {
			cleanup()
			return "", nil, nil, err
		}
		info := &repoInfo{
			name:   chooseRepoName(repository.RepoName, repository.URL, tmpDir),
			url:    chooseRepoURL(repository.RepoURL, repository.URL),
			branch: target,
		}
		return tmpDir, info, cleanup, nil
	}

	absPath, err := filepath.Abs(repository.Dir)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	stat, err := os.Stat(absPath)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to stat repository path: %w", err)
	}
	if !stat.IsDir() {
		return "", nil, nil, fmt.Errorf("repository input must be a directory or git URL: %s", repository.Dir)
	}
	info := &repoInfo{
		name:   chooseRepoName(repository.RepoName, absPath, absPath),
		url:    chooseRepoURL(repository.RepoURL, ""),
		branch: firstNonEmpty(repository.Commit, repository.Tag, repository.Branch),
	}
	return absPath, info, nil, nil
}

func resolveCheckoutTarget(repository Repository) (checkoutTargetKind, string) {
	switch {
	case repository.Commit != "":
		return checkoutTargetCommit, repository.Commit
	case repository.Tag != "":
		return checkoutTargetTag, repository.Tag
	case repository.Branch != "":
		return checkoutTargetBranch, repository.Branch
	default:
		return checkoutTargetDefault, ""
	}
}

func cloneRemoteRepository(ctx context.Context, repository Repository, tmpDir string) (string, error) {
	kind, target := resolveCheckoutTarget(repository)
	switch kind {
	case checkoutTargetDefault:
		if err := runGit(ctx, "", "clone", "--depth", "1", repository.URL, tmpDir); err != nil {
			return "", fmt.Errorf("failed to clone repo: %w", err)
		}
	case checkoutTargetBranch, checkoutTargetTag:
		if err := runGit(ctx, "", "clone", "--depth", "1", "--branch", target, repository.URL, tmpDir); err != nil {
			return "", fmt.Errorf("failed to clone repo at %s %q: %w", kind, target, err)
		}
	case checkoutTargetCommit:
		if err := runGit(ctx, "", "clone", "--depth", "1", repository.URL, tmpDir); err != nil {
			return "", fmt.Errorf("failed to clone repo before checking out commit %q: %w", target, err)
		}
		if err := runGit(ctx, tmpDir, "fetch", "--depth", "1", "origin", target); err != nil {
			return "", fmt.Errorf("failed to fetch commit %q: %w", target, err)
		}
		if err := runGit(ctx, tmpDir, "checkout", target); err != nil {
			return "", fmt.Errorf("failed to checkout commit %q: %w", target, err)
		}
	default:
		return "", fmt.Errorf("unsupported checkout target kind: %s", kind)
	}
	return target, nil
}

func (s *Source) getFilePaths(dirPath string) ([]string, error) {
	var filePaths []string
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path == dirPath {
				return nil
			}
			if !s.recursive || s.shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			// Skip git submodule directories that have not been checked out.
			// Populated submodules (with actual source files) are scanned normally.
			if isUnpopulatedGitLink(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() || s.shouldSkipFile(info.Name()) {
			return nil
		}
		if len(s.fileExtensions) > 0 {
			ext := strings.ToLower(filepath.Ext(path))
			if !slices.Contains(s.fileExtensions, ext) {
				return nil
			}
		}
		filePaths = append(filePaths, path)
		return nil
	})
	return filePaths, err
}

// isUnpopulatedGitLink reports whether dirPath is a git submodule directory
// that has not been checked out. Such directories contain a .git pointer file
// (not the usual .git directory) but no actual source files.
// Fully cloned submodules have a .git file plus real source files and are
// allowed through.
func isUnpopulatedGitLink(dirPath string) bool {
	fi, err := os.Lstat(filepath.Join(dirPath, ".git"))
	if err != nil || fi.IsDir() {
		// No .git entry, or .git is a directory (a regular repo root) → not a submodule link.
		return false
	}
	// .git is a file → this directory is a git submodule.
	// If it contains nothing else, the submodule has not been checked out.
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			return false // has other content → submodule is populated
		}
	}
	return true
}

// buildBaseMetadata returns common metadata shared by all documents produced from this source.
func (s *Source) buildBaseMetadata(repoRoot string, info *repoInfo) map[string]any {
	metadata := make(map[string]any, len(s.metadata)+6)
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeRepo
	metadata[source.MetaRepoPath] = repoRoot
	metadata[source.MetaSourceName] = s.name
	if info == nil {
		return metadata
	}
	if info.name != "" {
		metadata[source.MetaRepoName] = info.name
	}
	if info.url != "" {
		metadata[source.MetaRepoURL] = info.url
	}
	if info.branch != "" {
		metadata[source.MetaBranch] = info.branch
	}
	return metadata
}

func (s *Source) processFile(filePath, repoRoot string, info *repoInfo) ([]*document.Document, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	fileType := isource.GetFileType(filePath)
	r, exists := s.readers[fileType]
	if !exists {
		return nil, fmt.Errorf("no reader available for file type: %s", fileType)
	}
	documents, err := r.ReadFromFile(filePath)
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

	metadata := s.buildBaseMetadata(repoRoot, info)
	metadata[source.MetaFilePath] = relPath
	metadata[source.MetaFileName] = filepath.Base(filePath)
	metadata[source.MetaFileExt] = filepath.Ext(filePath)
	metadata[source.MetaFileSize] = fileInfo.Size()
	metadata[source.MetaFileMode] = fileInfo.Mode().String()
	metadata[source.MetaModifiedAt] = fileInfo.ModTime().UTC()
	metadata[source.MetaURI] = (&url.URL{Scheme: "file", Path: absPath}).String()

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

func (s *Source) processDirectory(dirPath, fileType, repoRoot string, info *repoInfo, allowedPaths map[string]struct{}) ([]*document.Document, error) {
	r, exists := s.readers[fileType]
	if !exists {
		return nil, fmt.Errorf("no reader available for file type: %s", fileType)
	}
	dirReader, ok := r.(directoryReader)
	if !ok {
		return nil, fmt.Errorf("reader for file type %s is not directory-capable", fileType)
	}
	documents, err := dirReader.ReadFromDirectory(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory with reader: %w", err)
	}

	baseMetadata := s.buildBaseMetadata(repoRoot, info)
	filtered := make([]*document.Document, 0, len(documents))
	for _, doc := range documents {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		for k, v := range baseMetadata {
			doc.Metadata[k] = v
		}

		relPath := toRelativeRepoPath(repoRoot, doc.Metadata["trpc_ast_file_path"])
		if relPath == "" {
			relPath = toRelativeRepoPath(repoRoot, doc.Metadata[source.MetaFilePath])
		}
		if len(allowedPaths) > 0 {
			if _, ok := allowedPaths[relPath]; !ok {
				continue
			}
		}
		if relPath != "" && s.shouldSkipFile(filepath.Base(relPath)) {
			continue
		}
		if relPath == "" {
			filtered = append(filtered, doc)
			continue
		}
		doc.Metadata[source.MetaFilePath] = relPath
		doc.Metadata["trpc_ast_file_path"] = relPath
		if doc.Metadata["trpc_ast_type"] == "file" {
			doc.Metadata["trpc_ast_name"] = relPath
			doc.Metadata["trpc_ast_full_name"] = relPath
		}
		absPath := filepath.Join(repoRoot, filepath.FromSlash(relPath))
		if fileInfo, err := os.Stat(absPath); err == nil {
			doc.Metadata[source.MetaFileName] = filepath.Base(absPath)
			doc.Metadata[source.MetaFileExt] = filepath.Ext(absPath)
			doc.Metadata[source.MetaFileSize] = fileInfo.Size()
			doc.Metadata[source.MetaFileMode] = fileInfo.Mode().String()
			doc.Metadata[source.MetaModifiedAt] = fileInfo.ModTime().UTC()
			doc.Metadata[source.MetaURI] = (&url.URL{Scheme: "file", Path: absPath}).String()
		}
		filtered = append(filtered, doc)
	}
	return filtered, nil
}

func toRelativeRepoPath(repoRoot string, raw any) string {
	if raw == nil {
		return ""
	}
	path, ok := raw.(string)
	if !ok || strings.TrimSpace(path) == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (s *Source) shouldSkipDir(name string) bool {
	return slices.Contains(s.skipDirs, name)
}

func (s *Source) shouldSkipFile(name string) bool {
	return slices.ContainsFunc(s.skipSuffixes, func(suffix string) bool {
		return strings.HasSuffix(name, suffix)
	})
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

// firstNonEmpty returns the first non-empty string from vals.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
