//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package python provides Python AST parsing via an embedded Python script.
package python

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

//go:embed python_parser.py
var embeddedScript string

// Parser wraps the embedded Python AST parser script.
type Parser struct {
	pythonPath     string
	extractImports bool
}

// Option configures the Python parser.
type Option func(*Parser)

// WithPythonPath sets the python interpreter path.
func WithPythonPath(path string) Option {
	return func(p *Parser) {
		p.pythonPath = path
	}
}

// WithExtractImports enables/disables extracting file-level imports.
func WithExtractImports(enabled bool) Option {
	return func(p *Parser) {
		p.extractImports = enabled
	}
}

const defaultParseConcurrency = 4

func init() {
	codeast.RegisterDirectoryParser(codeast.FileTypePython, NewParser())
}

// NewParser creates a new Python AST parser.
func NewParser(opts ...Option) *Parser {
	p := &Parser{
		pythonPath:     "python3",
		extractImports: true,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ParseDirectory walks dirPath, parses every .py file, and returns a merged
// result containing all nodes and edges. It implements codeast.DirectoryParser.
func (p *Parser) ParseDirectory(dirPath string, opts ...codeast.ParseOption) (*codeast.Result, error) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("get absolute path: %w", err)
	}

	includeSet := buildIncludeSet(codeast.ParseIncludeFiles(opts))
	files, err := collectPythonFiles(absDir, includeSet)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return &codeast.Result{}, nil
	}

	concurrency := codeast.ParseConcurrency(opts)
	if concurrency <= 0 {
		concurrency = defaultParseConcurrency
	}

	results := make([]parseFileResult, len(files))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, f := range files {
		wg.Add(1)
		go func(idx int, filePath string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			relPath, _ := filepath.Rel(absDir, filePath)
			modulePath := fileToModule(relPath, "")
			r, err := p.parseFile(filePath, modulePath, filePath)
			results[idx] = parseFileResult{result: r, err: err}
		}(i, f)
	}
	wg.Wait()

	return mergeResults(results, absDir)
}

func buildIncludeSet(files []string) map[string]struct{} {
	if len(files) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(files))
	for _, f := range files {
		set[filepath.Clean(f)] = struct{}{}
	}
	return set
}

func collectPythonFiles(absDir string, includeSet map[string]struct{}) ([]string, error) {
	var files []string
	err := filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "__pycache__" || base == ".git" || base == "node_modules" || base == ".venv" || base == "venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".py" {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, "test_") || name == "conftest.py" || name == "setup.py" {
			return nil
		}
		if includeSet != nil {
			if _, ok := includeSet[filepath.Clean(path)]; !ok {
				return nil
			}
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

type parseFileResult struct {
	result *codeast.Result
	err    error
}

func mergeResults(results []parseFileResult, absDir string) (*codeast.Result, error) {
	var allNodes []*codeast.Node
	var allEdges []*codeast.Edge
	importsSet := make(map[string]struct{})
	var errs []error

	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		if r.result == nil {
			continue
		}
		allNodes = append(allNodes, r.result.Nodes...)
		allEdges = append(allEdges, r.result.Edges...)
		if r.result.File != nil {
			for _, imp := range r.result.File.Imports {
				importsSet[imp] = struct{}{}
			}
		}
	}

	if len(allNodes) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("parse directory %s: all %d file(s) failed, first: %w",
			absDir, len(errs), errs[0])
	}

	imports := make([]string, 0, len(importsSet))
	for imp := range importsSet {
		imports = append(imports, imp)
	}
	sort.Strings(imports)

	result := &codeast.Result{
		File: &codeast.FileInfo{
			Name:     absDir,
			Language: codeast.LanguagePython,
			Imports:  imports,
		},
		Nodes: allNodes,
		Edges: allEdges,
	}

	if len(errs) > 0 {
		return result, fmt.Errorf("parse directory %s: %d/%d file(s) failed: %w",
			absDir, len(errs), len(results), errors.Join(errs...))
	}

	return result, nil
}

// ParseContent parses Python source content and returns AST nodes and edges.
// The name parameter is used as the file identifier (typically the file path).
func (p *Parser) ParseContent(name, content string) (*codeast.Result, error) {
	tmpFile, err := os.CreateTemp("", "pyparse-*.py")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	modulePath := fileToModule(name, "")
	return p.parseFile(tmpFile.Name(), modulePath, name)
}

// ParseFileAt parses a Python file at the given path.
func (p *Parser) ParseFileAt(filePath, modulePath string) (*codeast.Result, error) {
	return p.parseFile(filePath, modulePath, filePath)
}

// ParseFileInfo returns file-level metadata without full AST parsing.
func (p *Parser) ParseFileInfo(name, content string) (*codeast.FileInfo, error) {
	result, err := p.ParseContent(name, content)
	if err != nil {
		return nil, err
	}
	return result.File, nil
}

func (p *Parser) parseFile(filePath, modulePath, reportedPath string) (*codeast.Result, error) {
	extractImportsArg := "true"
	if !p.extractImports {
		extractImportsArg = "false"
	}

	cmd := exec.Command(p.pythonPath, "-c", embeddedScript, filePath, modulePath, extractImportsArg) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("python parser: %s", extractPythonError(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("run python parser: %w", err)
	}

	var raw pythonResult
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, fmt.Errorf("decode python output: %w", err)
	}

	return p.convertResult(raw, reportedPath, modulePath), nil
}

func (p *Parser) convertResult(raw pythonResult, reportedPath, modulePath string) *codeast.Result {
	nodes := make([]*codeast.Node, 0, len(raw.Nodes))
	var imports []string

	for _, n := range raw.Nodes {
		chunkIndex := 0
		if n.Metadata != nil {
			if v, ok := n.Metadata[codeast.MetadataKeyCodeChunkIndex]; ok {
				chunkIndex = toInt(v)
				delete(n.Metadata, codeast.MetadataKeyCodeChunkIndex)
			}
		}
		if len(n.Imports) > 0 && len(imports) == 0 {
			imports = n.Imports
		}

		filePath := n.FilePath
		if reportedPath != "" && filePath != reportedPath {
			filePath = reportedPath
		}

		node := &codeast.Node{
			ID:         n.ID,
			Name:       n.Name,
			FullName:   n.FullName,
			Type:       mapEntityType(n.Type),
			Scope:      codeast.ScopeCode,
			Language:   codeast.LanguagePython,
			Package:    n.Package,
			Imports:    n.Imports,
			Signature:  n.Signature,
			Comment:    n.Comment,
			Code:       n.Code,
			FilePath:   filePath,
			LineStart:  n.LineStart,
			LineEnd:    n.LineEnd,
			ChunkIndex: chunkIndex,
			Metadata:   n.Metadata,
		}
		if node.Metadata == nil {
			node.Metadata = make(map[string]any)
		}
		nodes = append(nodes, node)
	}

	edges := make([]*codeast.Edge, 0, len(raw.Edges))
	for _, e := range raw.Edges {
		edges = append(edges, &codeast.Edge{
			FromID: e.FromID,
			ToID:   e.ToID,
			Type:   mapRelationType(e.Type),
		})
	}

	return &codeast.Result{
		File: &codeast.FileInfo{
			Name:     reportedPath,
			Language: codeast.LanguagePython,
			Package:  modulePath,
			Imports:  imports,
		},
		Nodes: nodes,
		Edges: edges,
	}
}

// BuildNodeEmbeddingText generates embedding text for a Python AST node.
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

type pythonResult struct {
	Nodes []pythonNode `json:"nodes"`
	Edges []pythonEdge `json:"edges"`
}

type pythonNode struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	FullName  string         `json:"full_name"`
	Type      string         `json:"type"`
	Package   string         `json:"package"`
	Imports   []string       `json:"imports"`
	Signature string         `json:"signature"`
	Comment   string         `json:"comment"`
	Code      string         `json:"code"`
	FilePath  string         `json:"file_path"`
	LineStart int            `json:"line_start"`
	LineEnd   int            `json:"line_end"`
	Metadata  map[string]any `json:"metadata"`
}

type pythonEdge struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Type   string `json:"type"`
}

func mapEntityType(t string) codeast.EntityType {
	switch t {
	case "Function":
		return codeast.EntityFunction
	case "Method":
		return codeast.EntityMethod
	case "Class":
		return codeast.EntityClass
	case "Interface":
		return codeast.EntityInterface
	case "Variable":
		return codeast.EntityVariable
	case "Module":
		return codeast.EntityModule
	default:
		return codeast.EntityType(t)
	}
}

func mapRelationType(t string) codeast.RelationType {
	switch t {
	case "CALLS":
		return codeast.RelationCalls
	case "METHOD":
		return codeast.RelationMethod
	case "IMPLEMENTS":
		return codeast.RelationImplements
	case "IMPORTS":
		return codeast.RelationImports
	case "INHERITS":
		return codeast.RelationInherits
	case "FIELD":
		return codeast.RelationField
	case "PARAM":
		return codeast.RelationParam
	case "RETURNS":
		return codeast.RelationReturns
	default:
		return codeast.RelationType(t)
	}
}

// fileToModule converts a file path to a Python module path.
func fileToModule(filePath, baseModule string) string {
	name := filepath.Base(filePath)
	name = strings.TrimSuffix(name, ".py")
	if name == "__init__" {
		dir := filepath.Dir(filePath)
		name = filepath.Base(dir)
	}

	relPath := strings.TrimSuffix(filePath, ".py")
	relPath = strings.ReplaceAll(relPath, string(filepath.Separator), ".")
	relPath = strings.TrimSuffix(relPath, ".__init__")

	if baseModule != "" {
		return baseModule + "." + name
	}
	return relPath
}

func extractPythonError(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) == 0 {
		return stderr
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "SyntaxError:") ||
			strings.HasPrefix(line, "IndentationError:") ||
			strings.HasPrefix(line, "TabError:") ||
			strings.HasPrefix(line, "ModuleNotFoundError:") ||
			strings.HasPrefix(line, "ImportError:") {
			return line
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return stderr
}

func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	default:
		return 0
	}
}
