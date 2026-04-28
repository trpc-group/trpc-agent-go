//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package golang provides internal Go AST parsing for code-aware knowledge ingestion.
package golang

import (
	"encoding/json"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

// Parser parses Go source content into code-aware AST nodes.
type Parser struct {
	extractor codeast.Extractor[*extractInput]
	analyzer  codeast.Analyzer[*analyzeInput]
}

type extractInput struct {
	pkg  *parsedPackage
	fset *token.FileSet
}

type analyzeInput struct {
	pkg *parsedPackage
}

type parserConfig struct {
	concurrency    int
	extractImports bool
}

// Option is a functional option for configuring the parser.
type Option func(*parserConfig)

// WithConcurrency sets the concurrency for parallel extraction.
func WithConcurrency(n int) Option {
	return func(c *parserConfig) {
		if n > 0 {
			c.concurrency = n
		}
	}
}

// WithExtractImports enables or disables extracting file-level imports.
func WithExtractImports(enabled bool) Option {
	return func(c *parserConfig) {
		c.extractImports = enabled
	}
}

// NewParser creates a new Go AST parser.
func NewParser(opts ...Option) *Parser {
	cfg := &parserConfig{
		concurrency:    100,
		extractImports: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return &Parser{
		extractor: newDefaultExtractor(cfg.concurrency, cfg.extractImports),
		analyzer:  newDefaultAnalyzer(),
	}
}

// ParseContent parses Go source content and returns semantic nodes plus reserved edge slots.
func (p *Parser) ParseContent(name, content string) (*codeast.Result, error) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, name, content, goparser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go file: %w", err)
	}

	fileInfo := buildFileInfo(name, fileNode)
	pkgID := fileInfo.Package
	if pkgID == "" {
		pkgID = fileNode.Name.Name
	}
	pkg := &parsedPackage{
		ID:     pkgID,
		Name:   fileNode.Name.Name,
		Syntax: []*ast.File{fileNode},
		Fset:   fset,
	}

	nodes, err := p.extractor.Extract(&extractInput{pkg: pkg, fset: fset})
	if err != nil {
		return nil, err
	}

	nodeSet := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node == nil || node.ID == "" {
			continue
		}
		nodeSet[node.ID] = true
	}

	edges, err := p.analyzer.Analyze(&analyzeInput{pkg: pkg}, nodeSet)
	if err != nil {
		return nil, err
	}

	return &codeast.Result{
		File:  fileInfo,
		Nodes: nodes,
		Edges: edges,
	}, nil
}

// ParseDirectory parses a Go directory/module and returns semantic nodes across all files.
func (p *Parser) ParseDirectory(dirPath string) (*codeast.Result, error) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	fset := token.NewFileSet()
	pkgFiles := make(map[string][]*ast.File)
	pkgNames := make(map[string]string)
	fileInfos := make(map[string]*codeast.FileInfo)
	var orderedFiles []string

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileNode, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
		if err != nil {
			return nil
		}
		dir := filepath.Dir(path)
		pkgFiles[dir] = append(pkgFiles[dir], fileNode)
		pkgNames[dir] = fileNode.Name.Name
		fileInfos[path] = buildFileInfo(path, fileNode)
		orderedFiles = append(orderedFiles, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}
	if len(pkgFiles) == 0 {
		return &codeast.Result{File: &codeast.FileInfo{Name: absDir, Language: codeast.LanguageGo}}, nil
	}

	modulePath := parseGoModulePath(filepath.Join(absDir, "go.mod"))
	if modulePath == "" {
		modulePath = filepath.Base(absDir)
	}

	var allNodes []*codeast.Node
	sort.Strings(orderedFiles)
	for dir, files := range pkgFiles {
		pkgID := modulePathForDir(absDir, dir)
		pkg := &parsedPackage{ID: pkgID, Name: pkgNames[dir], Syntax: files, Fset: fset}
		nodes, err := p.extractor.Extract(&extractInput{pkg: pkg, fset: fset})
		if err != nil {
			return nil, err
		}
		allNodes = append(allNodes, nodes...)
	}

	var mergedImports []string
	seenImports := make(map[string]struct{})
	for _, path := range orderedFiles {
		info := fileInfos[path]
		if info == nil {
			continue
		}
		for _, imp := range info.Imports {
			if _, ok := seenImports[imp]; ok {
				continue
			}
			seenImports[imp] = struct{}{}
			mergedImports = append(mergedImports, imp)
		}
	}

	return &codeast.Result{
		File: &codeast.FileInfo{
			Name:     absDir,
			Language: codeast.LanguageGo,
			Package:  modulePath,
			Imports:  mergedImports,
		},
		Nodes: allNodes,
		Edges: []*codeast.Edge{},
	}, nil
}

func modulePathForDir(baseDir, dir string) string {
	moduleDir, modulePath := findNearestGoModule(dir)
	if modulePath == "" {
		modulePath = filepath.Base(baseDir)
		moduleDir = baseDir
	}

	relPath, err := filepath.Rel(moduleDir, dir)
	if err != nil || relPath == "." || relPath == "" {
		return modulePath
	}
	return modulePath + "/" + filepath.ToSlash(relPath)
}

// ParseFileInfo extracts file-level metadata without requiring a full semantic extraction result.
func (p *Parser) ParseFileInfo(name, content string) (*codeast.FileInfo, error) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, name, content, goparser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go file: %w", err)
	}
	return buildFileInfo(name, fileNode), nil
}

type parsedPackage struct {
	ID     string
	Name   string
	Syntax []*ast.File
	Fset   *token.FileSet
}

func buildFileInfo(name string, fileNode *ast.File) *codeast.FileInfo {
	return &codeast.FileInfo{
		Name:     name,
		Language: codeast.LanguageGo,
		Package:  resolvePackagePath(name, fileNode.Name.Name),
		Imports:  extractImportsFromASTFile(fileNode),
	}
}

// BuildNodeEmbeddingText builds the embedding payload for a parsed Go node.
func BuildNodeEmbeddingText(node *codeast.Node) string {
	if node == nil {
		return ""
	}

	comment := strings.TrimSpace(node.Comment)
	payload := map[string]string{
		"id":        node.ID,
		"type":      string(node.Type),
		"name":      node.Name,
		"full_name": node.FullName,
		"package":   node.Package,
		"file_path": node.FilePath,
		"signature": node.Signature,
		"comment":   comment,
	}

	jsonBytes, _ := json.Marshal(payload)
	return string(jsonBytes)
}

// BuildFileEmbeddingText builds the embedding payload for a whole Go file document.
func BuildFileEmbeddingText(content, name, packagePath string, imports []string) string {
	payload := map[string]string{
		"id":        name,
		"type":      "file",
		"name":      name,
		"full_name": name,
		"package":   packagePath,
		"file_path": name,
		"comment":   "",
		"signature": "",
	}
	if content != "" {
		payload["code"] = content
	}

	jsonBytes, _ := json.Marshal(payload)
	return string(jsonBytes)
}

func resolvePackagePath(fileName, packageName string) string {
	if packageName == "" {
		return ""
	}
	if !looksLikeLocalPath(fileName) {
		return packageName
	}

	fileDir := filepath.Dir(fileName)
	moduleDir, modulePath := findNearestGoModule(fileDir)
	if moduleDir == "" || modulePath == "" {
		return packageName
	}

	relPath, err := filepath.Rel(moduleDir, fileDir)
	if err != nil || strings.HasPrefix(relPath, "..") {
		return packageName
	}
	if relPath == "." {
		return modulePath
	}
	return modulePath + "/" + filepath.ToSlash(relPath)
}

func looksLikeLocalPath(name string) bool {
	return filepath.IsAbs(name) || strings.Contains(name, string(filepath.Separator))
}

func findNearestGoModule(startDir string) (string, string) {
	dir := startDir
	for {
		goModPath := filepath.Join(dir, "go.mod")
		modulePath := parseGoModulePath(goModPath)
		if modulePath != "" {
			return dir, modulePath
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

func parseGoModulePath(goModPath string) string {
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		modulePath := strings.TrimSpace(strings.TrimPrefix(line, "module "))
		if idx := strings.Index(modulePath, "//"); idx >= 0 {
			modulePath = strings.TrimSpace(modulePath[:idx])
		}
		return strings.Trim(modulePath, "\"")
	}

	return ""
}
