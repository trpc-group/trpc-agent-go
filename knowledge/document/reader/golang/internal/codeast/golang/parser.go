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
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const defaultGoModuleConcurrency = 4

func init() {
	codeast.RegisterDirectoryParser(codeast.FileTypeGo, NewParser(WithEdgeAnalysis(true)))
}

// Parser parses Go source content into code-aware AST nodes.
type Parser struct {
	extractor      *defaultExtractor
	analyzer       *defaultAnalyzer
	concurrency    int
	extractImports bool
	extractEdges   bool
	includeFiles   []string
}

type extractInput struct {
	pkg  *parsedPackage
	fset *token.FileSet
}

type analyzeInput struct {
	pkg        *parsedPackage
	interfaces []*interfaceType
}

type interfaceType struct {
	id       string
	iface    *types.Interface
	external bool
}

type parserConfig struct {
	concurrency    int
	extractImports bool
	extractEdges   bool
	includeFiles   []string
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

// WithEdgeAnalysis enables or disables edge (relationship) extraction.
// Edge analysis requires type-checked packages and is skipped when disabled,
// reducing parse time for callers that only need node (symbol) data.
// Defaults to false; set to true when graph-aware parsing is needed.
func WithEdgeAnalysis(enabled bool) Option {
	return func(c *parserConfig) {
		c.extractEdges = enabled
	}
}

func withIncludeFiles(files []string) Option {
	return func(c *parserConfig) {
		c.includeFiles = append([]string(nil), files...)
	}
}

// NewParser creates a new Go AST parser.
func NewParser(opts ...Option) *Parser {
	cfg := &parserConfig{
		concurrency:    100,
		extractImports: true,
		extractEdges:   false,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return &Parser{
		extractor:      newDefaultExtractor(cfg.concurrency, cfg.extractImports),
		analyzer:       newDefaultAnalyzer(),
		concurrency:    cfg.concurrency,
		extractImports: cfg.extractImports,
		extractEdges:   cfg.extractEdges,
		includeFiles:   cfg.includeFiles,
	}
}

func (p *Parser) withConcurrency(concurrency int) *Parser {
	if concurrency <= 0 {
		concurrency = 1
	}
	return NewParser(
		WithConcurrency(concurrency),
		WithExtractImports(p.extractImports),
		WithEdgeAnalysis(p.extractEdges),
		withIncludeFiles(p.includeFiles),
	)
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

	var edges []*codeast.Edge
	if p.extractEdges {
		var err error
		edges, err = p.analyzer.Analyze(&analyzeInput{pkg: pkg}, nodeSet)
		if err != nil {
			return nil, err
		}
	}

	return &codeast.Result{
		File:  fileInfo,
		Nodes: nodes,
		Edges: edges,
	}, nil
}

// ParseDirectory parses a Go directory/module and returns semantic nodes across all files.
func (p *Parser) ParseDirectory(dirPath string, opts ...codeast.ParseOption) (*codeast.Result, error) {
	if includeFiles := codeast.ParseIncludeFiles(opts); len(includeFiles) > 0 {
		next := *p
		next.includeFiles = includeFiles
		p = &next
	}
	if concurrency := codeast.ParseConcurrency(opts); concurrency > 0 && concurrency != p.concurrency {
		return p.withConcurrency(concurrency).ParseDirectory(dirPath)
	}
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	modules, err := findGoModules(absDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find go modules: %w", err)
	}
	// When absDir is not a module root but contains nested sub-modules, it still
	// belongs to a parent module. Prepend absDir so its own packages are parsed
	// under that parent module alongside the nested sub-modules.
	if len(modules) > 0 && parseGoModulePath(filepath.Join(absDir, "go.mod")) == "" {
		if parentModuleDir, _ := findNearestGoModule(absDir); parentModuleDir != "" {
			modules = append([]string{absDir}, modules...)
		}
	}
	if len(modules) == 0 {
		modules = []string{absDir}
	}
	if len(modules) == 1 && modules[0] == absDir {
		return p.parseDirectoryModule(absDir)
	}

	return p.parseDirectoryModules(absDir, modules)
}

func (p *Parser) parseDirectoryModules(absDir string, modules []string) (*codeast.Result, error) {
	moduleConcurrency, perModuleConcurrency := p.moduleConcurrency(len(modules))
	results, err := p.parseDirectoryModulesWithConcurrency(modules, moduleConcurrency, perModuleConcurrency)
	if err != nil {
		return nil, err
	}
	allNodes, allEdges, imports := mergeModuleResults(results)
	return &codeast.Result{
		File: &codeast.FileInfo{
			Name:     absDir,
			Language: codeast.LanguageGo,
			Package:  modulePathForDir(absDir, absDir),
			Imports:  sortedKeys(imports),
		},
		Nodes: allNodes,
		Edges: allEdges,
	}, nil
}

func (p *Parser) moduleConcurrency(moduleCount int) (int, int) {
	total := p.concurrency
	if total <= 0 {
		total = 1
	}
	moduleConcurrency := defaultGoModuleConcurrency
	if moduleConcurrency > moduleCount {
		moduleConcurrency = moduleCount
	}
	if moduleConcurrency > total {
		moduleConcurrency = total
	}
	if moduleConcurrency <= 0 {
		moduleConcurrency = 1
	}
	perModuleConcurrency := total / moduleConcurrency
	if perModuleConcurrency <= 0 {
		perModuleConcurrency = 1
	}
	return moduleConcurrency, perModuleConcurrency
}

func (p *Parser) parseDirectoryModulesWithConcurrency(
	modules []string,
	moduleConcurrency int,
	perModuleConcurrency int,
) ([]*codeast.Result, error) {
	if len(modules) == 0 {
		return nil, nil
	}
	if moduleConcurrency <= 1 {
		results := make([]*codeast.Result, len(modules))
		moduleParser := p.withConcurrency(perModuleConcurrency)
		for i, moduleDir := range modules {
			result, err := moduleParser.parseDirectoryModule(moduleDir)
			if err != nil {
				return nil, err
			}
			results[i] = result
		}
		return results, nil
	}

	type job struct {
		index int
		path  string
	}
	jobs := make(chan job)
	errCh := make(chan error, 1)
	results := make([]*codeast.Result, len(modules))
	var wg sync.WaitGroup
	for i := 0; i < moduleConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			moduleParser := p.withConcurrency(perModuleConcurrency)
			for item := range jobs {
				result, err := moduleParser.parseDirectoryModule(item.path)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				results[item.index] = result
			}
		}()
	}
	for i, moduleDir := range modules {
		select {
		case err := <-errCh:
			close(jobs)
			wg.Wait()
			return nil, err
		case jobs <- job{index: i, path: moduleDir}:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
		return results, nil
	}
}

func mergeModuleResults(results []*codeast.Result) ([]*codeast.Node, []*codeast.Edge, map[string]struct{}) {
	var allNodes []*codeast.Node
	var allEdges []*codeast.Edge
	imports := make(map[string]struct{})
	for _, result := range results {
		if result == nil {
			continue
		}
		allNodes = append(allNodes, result.Nodes...)
		allEdges = append(allEdges, result.Edges...)
		if result.File == nil {
			continue
		}
		for _, imp := range result.File.Imports {
			imports[imp] = struct{}{}
		}
	}
	return allNodes, allEdges, imports
}

func (p *Parser) parseDirectoryModule(absDir string) (*codeast.Result, error) {
	result, err := p.parseDirectoryFull(absDir)
	if err == nil {
		return result, nil
	}
	log.Infof("golang/parser: parseDirectoryFull failed for %s, falling back to direct AST: %v", absDir, err)
	return p.parseDirectoryDirectAST(absDir)
}

func (p *Parser) parseDirectoryFull(absDir string) (*codeast.Result, error) {
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Context: context.Background(),
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedImports |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedDeps,
		Dir:  absDir,
		Fset: fset,
	}
	patterns := p.loadPatterns(absDir)
	if len(patterns) == 0 {
		return &codeast.Result{
			File: &codeast.FileInfo{
				Name:     absDir,
				Language: codeast.LanguageGo,
				Package:  modulePathForDir(absDir, absDir),
			},
		}, nil
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load packages: %w", err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no packages loaded from %s", absDir)
	}

	var allNodes []*codeast.Node
	parsedPkgs := make([]*parsedPackage, 0, len(pkgs))
	includeFiles := p.includeFileSet()
	for _, pkg := range pkgs {
		if pkg == nil || len(pkg.Syntax) == 0 {
			continue
		}
		parsed := parsedPackageFromPackages(pkg, includeFiles)
		if len(parsed.Syntax) == 0 {
			continue
		}
		nodes, err := p.extractor.Extract(&extractInput{pkg: parsed, fset: parsed.Fset})
		if err != nil {
			return nil, err
		}
		allNodes = append(allNodes, nodes...)
		parsedPkgs = append(parsedPkgs, parsed)
	}
	if len(allNodes) == 0 {
		return nil, fmt.Errorf("no nodes extracted from loaded packages in %s", absDir)
	}

	nodeSet := make(map[string]bool, len(allNodes))
	for _, node := range allNodes {
		if node == nil || node.ID == "" {
			continue
		}
		nodeSet[node.ID] = true
	}
	var allEdges []*codeast.Edge
	if p.extractEdges {
		allEdges, err = p.analyzePackages(parsedPkgs, nodeSet)
		if err != nil {
			return nil, err
		}
	}

	return &codeast.Result{
		File: &codeast.FileInfo{
			Name:     absDir,
			Language: codeast.LanguageGo,
			Package:  modulePathForDir(absDir, absDir),
			Imports:  packageImports(parsedPkgs),
		},
		Nodes: allNodes,
		Edges: allEdges,
	}, nil
}

func (p *Parser) analyzePackages(parsedPkgs []*parsedPackage, nodeSet map[string]bool) ([]*codeast.Edge, error) {
	if len(parsedPkgs) == 0 {
		return nil, nil
	}
	interfaces := packageInterfaces(parsedPkgs, nodeSet)
	concurrency := p.concurrency
	if concurrency <= 1 || len(parsedPkgs) == 1 {
		var allEdges []*codeast.Edge
		for _, pkg := range parsedPkgs {
			edges, err := p.analyzer.Analyze(&analyzeInput{pkg: pkg, interfaces: interfaces}, nodeSet)
			if err != nil {
				return nil, err
			}
			allEdges = append(allEdges, edges...)
		}
		return allEdges, nil
	}
	if concurrency > len(parsedPkgs) {
		concurrency = len(parsedPkgs)
	}

	type job struct {
		index int
		pkg   *parsedPackage
	}
	jobs := make(chan job)
	errCh := make(chan error, 1)
	results := make([][]*codeast.Edge, len(parsedPkgs))
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			analyzer := newDefaultAnalyzer()
			for item := range jobs {
				edges, err := analyzer.Analyze(&analyzeInput{pkg: item.pkg, interfaces: interfaces}, nodeSet)
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				results[item.index] = edges
			}
		}()
	}
	for i, pkg := range parsedPkgs {
		select {
		case err := <-errCh:
			close(jobs)
			wg.Wait()
			return nil, err
		case jobs <- job{index: i, pkg: pkg}:
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errCh:
		return nil, err
	default:
	}

	var allEdges []*codeast.Edge
	for _, edges := range results {
		allEdges = append(allEdges, edges...)
	}
	return allEdges, nil
}

func packageInterfaces(pkgs []*parsedPackage, nodeSet map[string]bool) []*interfaceType {
	var interfaces []*interfaceType
	seen := make(map[string]struct{})
	appendInterfaces := func(pkg *types.Package, external bool) {
		if pkg == nil {
			return
		}
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			typeName, ok := obj.(*types.TypeName)
			if !ok {
				continue
			}
			iface, ok := typeName.Type().Underlying().(*types.Interface)
			if !ok || iface.NumMethods() == 0 {
				continue
			}
			id := fmt.Sprintf("%s.%s", pkg.Path(), name)
			if _, ok := seen[id]; ok {
				continue
			}
			if !external && nodeSet != nil && !nodeSet[id] {
				continue
			}
			seen[id] = struct{}{}
			interfaces = append(interfaces, &interfaceType{id: id, iface: iface, external: external})
		}
	}
	for _, pkg := range pkgs {
		if pkg == nil || pkg.Types == nil {
			continue
		}
		appendInterfaces(pkg.Types, false)
		for _, imported := range pkg.Types.Imports() {
			appendInterfaces(imported, true)
		}
	}
	return interfaces
}

func (p *Parser) parseDirectoryDirectAST(absDir string) (*codeast.Result, error) {
	fset := token.NewFileSet()
	includeFiles := p.includeFileSet()
	pkgFiles := make(map[string][]*ast.File)
	pkgNames := make(map[string]string)
	fileInfos := make(map[string]*codeast.FileInfo)
	var orderedFiles []string

	err := filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != absDir {
				if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !info.Mode().IsRegular() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if len(includeFiles) > 0 {
			absPath, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if _, ok := includeFiles[filepath.Clean(absPath)]; !ok {
				return nil
			}
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

	sort.Strings(orderedFiles)
	dirs := make([]string, 0, len(pkgFiles))
	for dir := range pkgFiles {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	var allNodes []*codeast.Node
	for _, dir := range dirs {
		files := pkgFiles[dir]
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

func (p *Parser) loadPatterns(absDir string) []string {
	if len(p.includeFiles) == 0 {
		return []string{"./..."}
	}
	// Resolve the module that owns absDir (which may be a non-root subdirectory)
	// so we can exclude includeFiles that belong to a different module.
	baseModuleDir, _ := findNearestGoModule(absDir)
	dirs := make(map[string]struct{})
	for _, file := range p.includeFiles {
		if file == "" {
			continue
		}
		absFile, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(absDir, absFile)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			continue
		}
		if baseModuleDir != "" {
			nearest, _ := findNearestGoModule(filepath.Dir(absFile))
			if filepath.Clean(nearest) != filepath.Clean(baseModuleDir) {
				continue
			}
		}
		dirs[filepath.Dir(rel)] = struct{}{}
	}
	if len(dirs) == 0 {
		return nil
	}
	patterns := make([]string, 0, len(dirs))
	for dir := range dirs {
		if dir == "." {
			patterns = append(patterns, ".")
			continue
		}
		patterns = append(patterns, "./"+filepath.ToSlash(dir))
	}
	sort.Strings(patterns)
	return patterns
}

func (p *Parser) includeFileSet() map[string]struct{} {
	if len(p.includeFiles) == 0 {
		return nil
	}
	files := make(map[string]struct{}, len(p.includeFiles))
	for _, file := range p.includeFiles {
		if file == "" {
			continue
		}
		absFile, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		files[filepath.Clean(absFile)] = struct{}{}
	}
	return files
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
	ID        string
	Name      string
	Syntax    []*ast.File
	Fset      *token.FileSet
	Types     *types.Package
	TypesInfo *types.Info
	Imports   map[string]*parsedImport
}

type parsedImport struct {
	Name    string
	PkgPath string
}

func parsedPackageFromPackages(pkg *packages.Package, includeFiles map[string]struct{}) *parsedPackage {
	imports := make(map[string]*parsedImport, len(pkg.Imports))
	for importPath, imported := range pkg.Imports {
		if imported == nil {
			continue
		}
		imports[importPath] = &parsedImport{
			Name:    imported.Name,
			PkgPath: imported.PkgPath,
		}
	}
	return &parsedPackage{
		ID:        pkg.ID,
		Name:      pkg.Name,
		Syntax:    filterPackageSyntax(pkg, includeFiles),
		Fset:      pkg.Fset,
		Types:     pkg.Types,
		TypesInfo: pkg.TypesInfo,
		Imports:   imports,
	}
}

func filterPackageSyntax(pkg *packages.Package, includeFiles map[string]struct{}) []*ast.File {
	if len(includeFiles) == 0 {
		return pkg.Syntax
	}
	syntax := make([]*ast.File, 0, len(pkg.Syntax))
	for i, file := range pkg.Syntax {
		if file == nil {
			continue
		}
		name := ""
		if i < len(pkg.CompiledGoFiles) {
			name = pkg.CompiledGoFiles[i]
		}
		if name == "" && pkg.Fset != nil {
			name = pkg.Fset.Position(file.Package).Filename
		}
		if name == "" {
			continue
		}
		absName, err := filepath.Abs(name)
		if err != nil {
			continue
		}
		if _, ok := includeFiles[filepath.Clean(absName)]; ok {
			syntax = append(syntax, file)
		}
	}
	return syntax
}

func packageImports(pkgs []*parsedPackage) []string {
	seen := make(map[string]struct{})
	for _, pkg := range pkgs {
		for importPath := range pkg.Imports {
			seen[importPath] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func findGoModules(root string) ([]string, error) {
	var modules []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if info.Name() == ".git" {
			return filepath.SkipDir
		}
		goModPath := filepath.Join(path, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			modules = append(modules, path)
		}
		return nil
	})
	sort.Strings(modules)
	return modules, err
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
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
