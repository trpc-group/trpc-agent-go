//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package golang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

func TestParseContentBuildsNodesAndReservesEdges(t *testing.T) {
	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseContent("service.go", `package demo

import "context"

type Service struct{}

func (s *Service) Do(ctx context.Context) error {
	return nil
}
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if result.File == nil {
		t.Fatal("expected file info to be present")
	}
	if result.File.Package != "demo" {
		t.Fatalf("package = %s, want demo", result.File.Package)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(result.Nodes))
	}
	if result.Nodes[0].ID != "demo.Service" {
		t.Fatalf("first node id = %s, want demo.Service", result.Nodes[0].ID)
	}
	if len(result.Nodes[1].Imports) != 1 || result.Nodes[1].Imports[0] != "context" {
		t.Fatalf("method imports = %v, want [context]", result.Nodes[1].Imports)
	}
	if result.Nodes[1].ChunkIndex != 1 {
		t.Fatalf("method chunk index = %d, want 1", result.Nodes[1].ChunkIndex)
	}
	if result.Edges == nil {
		t.Fatal("expected edges slice to be initialized")
	}
	if len(result.Edges) != 1 {
		t.Fatalf("len(edges) = %d, want 1", len(result.Edges))
	}
	if result.Edges[0].Type != codeast.RelationMethod ||
		result.Edges[0].FromID != "demo.Service" ||
		result.Edges[0].ToID != "demo.Service.Do" {
		t.Fatalf("unexpected method edge: %+v", result.Edges[0])
	}
}

func TestParseContentReturnsErrorForInvalidGo(t *testing.T) {
	parser := NewParser()
	_, err := parser.ParseContent("broken.go", "package demo\nfunc Broken( {")
	if err == nil {
		t.Fatal("expected ParseContent() to return error for invalid Go source")
	}
}

func TestParseDirectoryRespectsGoModAndFindsNodes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "service.go"), `package demo

type Service struct{}

func helper() {}

func (s *Service) Do() error {
	helper()
	return nil
}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if result.File == nil || result.File.Package != "example.com/demo" {
		t.Fatalf("package = %v, want example.com/demo", result.File)
	}
	if len(result.Nodes) < 2 {
		t.Fatalf("len(nodes) = %d, want >= 2", len(result.Nodes))
	}
	if !hasCodeEdge(result.Edges, "example.com/demo.Service", "example.com/demo.Service.Do", codeast.RelationMethod) {
		t.Fatalf("expected METHOD edge, got %+v", result.Edges)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo.Service.Do", "example.com/demo.helper", codeast.RelationCalls) {
		t.Fatalf("expected CALLS edge, got %+v", result.Edges)
	}
}

func TestParseDirectoryIncludeFilesFiltersSamePackageFiles(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.go")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, mainPath, `package demo

func Target() {}
`)
	writeFile(t, filepath.Join(dir, "skip.pb.go"), `package demo

func Skipped() {}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir, codeast.WithParseIncludeFiles([]string{mainPath}))
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeNode(result.Nodes, "example.com/demo.Target") {
		t.Fatalf("expected Target node, got %+v", result.Nodes)
	}
	if hasCodeNode(result.Nodes, "example.com/demo.Skipped") {
		t.Fatalf("expected skipped file to be excluded, got %+v", result.Nodes)
	}
}

func TestParseDirectoryIncludeFilesKeepsNestedModulesSeparate(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "root.go")
	subPath := filepath.Join(dir, "sub", "sub.go")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, rootPath, `package root

type Service struct{}

func helper() {}

func (s *Service) Do() {
	helper()
}
`)
	writeFile(t, filepath.Join(dir, "sub", "go.mod"), "module example.com/sub\n\ngo 1.21\n")
	writeFile(t, subPath, `package sub

func Sub() {}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir, codeast.WithParseIncludeFiles([]string{rootPath, subPath}))
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/root.Service", "example.com/root.Service.Do", codeast.RelationMethod) {
		t.Fatalf("expected root module typed edge, got %+v", result.Edges)
	}
	if !hasCodeNode(result.Nodes, "example.com/sub.Sub") {
		t.Fatalf("expected nested module node, got %+v", result.Nodes)
	}
}

func TestParseDirectorySubdirWithParentModuleAndNestedModule(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(pkgDir, "parent.go"), `package pkg

func Parent() {}
`)
	writeFile(t, filepath.Join(pkgDir, "nested", "go.mod"), "module example.com/nested\n\ngo 1.21\n")
	writeFile(t, filepath.Join(pkgDir, "nested", "nested.go"), `package nested

func Nested() {}
`)

	parser := NewParser()
	result, err := parser.ParseDirectory(pkgDir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeNode(result.Nodes, "example.com/root/pkg.Parent") {
		t.Fatalf("expected parent module subdir node, got %+v", result.Nodes)
	}
	if !hasCodeNode(result.Nodes, "example.com/nested.Nested") {
		t.Fatalf("expected nested module node, got %+v", result.Nodes)
	}
}

func TestParseDirectoryFullModeAnalyzesTypedEdges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "lib", "lib.go"), `package lib

type Runner interface {
	Run()
}

type Worker struct{}

func (w Worker) Run() {}
`)
	writeFile(t, filepath.Join(dir, "app", "app.go"), `package app

import "example.com/demo/lib"

func Use(w lib.Worker) {
	w.Run()
}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/lib.Worker", "example.com/demo/lib.Runner", codeast.RelationImplements) {
		t.Fatalf("expected IMPLEMENTS edge, got %+v", result.Edges)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/app.Use", "example.com/demo/lib.Worker.Run", codeast.RelationCalls) {
		t.Fatalf("expected cross-package CALLS edge, got %+v", result.Edges)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/app.Use", "example.com/demo/lib.Worker", codeast.RelationParam) {
		t.Fatalf("expected cross-package PARAM edge, got %+v", result.Edges)
	}
}

func TestParseDirectoryFullModeAnalyzesGenericReceiverCalls(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "container.go"), `package demo

type Container[T any] struct{}

func (c *Container[T]) Get() {}

func Use() {
	c := &Container[int]{}
	c.Get()
}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo.Use", "example.com/demo.Container.Get", codeast.RelationCalls) {
		t.Fatalf("expected generic receiver CALLS edge, got %+v", result.Edges)
	}
}

func TestParseDirectoryFullModeAnalyzesCrossPackageImplements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "api", "api.go"), `package api

type Store interface {
	Add()
	Close() error
}
`)
	writeFile(t, filepath.Join(dir, "impl", "impl.go"), `package impl

type Store struct{}

func (s *Store) Add() {}
func (s *Store) Close() error { return nil }
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/impl.Store", "example.com/demo/api.Store", codeast.RelationImplements) {
		t.Fatalf("expected cross-package IMPLEMENTS edge, got %+v", result.Edges)
	}
}

func TestParseDirectoryFullModeAnalyzesCrossPackagePointerImplements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "api", "api.go"), `package api

type Store interface {
	Add()
}
`)
	writeFile(t, filepath.Join(dir, "impl", "impl.go"), `package impl

type Store struct{}

func (s *Store) Add() {}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/impl.Store", "example.com/demo/api.Store", codeast.RelationImplements) {
		t.Fatalf("expected pointer receiver cross-package IMPLEMENTS edge, got %+v", result.Edges)
	}
}

func TestParseDirectoryFullModeSkipsEmptyInterfaceImplements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "demo.go"), `package demo

type NodeResult any

type Store struct{}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if hasCodeEdge(result.Edges, "example.com/demo.Store", "example.com/demo.NodeResult", codeast.RelationImplements) {
		t.Fatalf("unexpected empty-interface IMPLEMENTS edge, got %+v", result.Edges)
	}
}

func TestParseDirectoryFullModeResolvesImportAliasesInTypeDeps(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "lib", "lib.go"), `package lib

type Runner interface {
	Run()
}

type Worker struct{}
`)
	writeFile(t, filepath.Join(dir, "app", "app.go"), `package app

import alias "example.com/demo/lib"

type Holder struct {
	Worker alias.Worker
}

type WorkerAlias = alias.Worker

func Use(w alias.Worker) alias.Runner {
	return nil
}
`)

	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/app.Use", "example.com/demo/lib.Worker", codeast.RelationParam) {
		t.Fatalf("expected aliased PARAM edge, got %+v", result.Edges)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/app.Use", "example.com/demo/lib.Runner", codeast.RelationReturns) {
		t.Fatalf("expected aliased RETURNS edge, got %+v", result.Edges)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/app.Holder", "example.com/demo/lib.Worker", codeast.RelationField) {
		t.Fatalf("expected aliased FIELD edge, got %+v", result.Edges)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/app.WorkerAlias", "example.com/demo/lib.Worker", codeast.RelationAliasOf) {
		t.Fatalf("expected aliased ALIAS_OF edge, got %+v", result.Edges)
	}
}

func TestParseDirectoryFallsBackToBaseDirWhenNoGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "service.go"), "package demo\n\nfunc Do() {}\n")

	parser := NewParser()
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if result.File == nil {
		t.Fatal("expected file info")
	}
	if result.File.Package != filepath.Base(dir) {
		t.Fatalf("package = %s, want %s", result.File.Package, filepath.Base(dir))
	}
}

func TestParseDirectoryWithoutGoFilesReturnsEmptyResult(t *testing.T) {
	dir := t.TempDir()
	parser := NewParser()
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if result == nil || result.File == nil {
		t.Fatal("expected non-nil result and file info")
	}
	if len(result.Nodes) != 0 {
		t.Fatalf("len(nodes) = %d, want 0", len(result.Nodes))
	}
}

func TestParseDirectoryIgnoresInvalidAndTestGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "ok.go"), "package demo\n\nfunc OK() {}\n")
	writeFile(t, filepath.Join(dir, "broken.go"), "package demo\nfunc Broken( {\n")
	writeFile(t, filepath.Join(dir, "ok_test.go"), "package demo\n\nfunc TestX(){}\n")

	parser := NewParser()
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least one node from ok.go")
	}
}

func TestResolvePackagePathAndModuleHelpers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n")
	child := filepath.Join(dir, "pkg", "sub")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	pkg := resolvePackagePath(filepath.Join(child, "x.go"), "sub")
	if pkg != "example.com/root/pkg/sub" {
		t.Fatalf("resolvePackagePath() = %s, want example.com/root/pkg/sub", pkg)
	}
	if resolvePackagePath("remote.go", "demo") != "demo" {
		t.Fatal("resolvePackagePath for non-local name should keep package name")
	}
	if parseGoModulePath(filepath.Join(dir, "go.mod")) != "example.com/root" {
		t.Fatal("parseGoModulePath() mismatch")
	}
	if got := modulePathForDir(dir, child); got != "example.com/root/pkg/sub" {
		t.Fatalf("modulePathForDir() = %s, want example.com/root/pkg/sub", got)
	}
	moduleDir, module := findNearestGoModule(filepath.Join(child, "deep"))
	if moduleDir != dir {
		t.Fatalf("findNearestGoModule dir = %s, want %s", moduleDir, dir)
	}
	if module != "example.com/root" {
		t.Fatalf("findNearestGoModule module = %s, want example.com/root", module)
	}

	if got := modulePathForDir(dir, dir); got != "example.com/root" {
		t.Fatalf("modulePathForDir(base, base) = %s, want example.com/root", got)
	}

	if got := parseGoModulePath(filepath.Join(t.TempDir(), "go.mod")); got != "" {
		t.Fatalf("parseGoModulePath(non-existent) = %q, want empty", got)
	}

	quotedMod := filepath.Join(dir, "quoted.mod")
	writeFile(t, quotedMod, "module \"example.com/quoted\" // comment\n")
	if got := parseGoModulePath(quotedMod); got != "example.com/quoted" {
		t.Fatalf("parseGoModulePath(quoted) = %s, want example.com/quoted", got)
	}

	if got := resolvePackagePath("/abs/path/x.go", ""); got != "" {
		t.Fatalf("resolvePackagePath(empty package) = %q, want empty", got)
	}
	if got := resolvePackagePath("/abs/path/x.go", "demo"); got != "demo" {
		t.Fatalf("resolvePackagePath(no module) = %q, want demo", got)
	}
}

func TestParseFileInfoAndBuildFileEmbeddingText(t *testing.T) {
	parser := NewParser()
	fileInfo, err := parser.ParseFileInfo("service.go", `package demo
import "context"
`)
	if err != nil {
		t.Fatalf("ParseFileInfo() error = %v", err)
	}
	if fileInfo.Package != "demo" {
		t.Fatalf("package = %s, want demo", fileInfo.Package)
	}
	if len(fileInfo.Imports) != 1 || fileInfo.Imports[0] != "context" {
		t.Fatalf("imports = %v, want [context]", fileInfo.Imports)
	}

	emb := BuildFileEmbeddingText("code", "service.go", fileInfo.Package, fileInfo.Imports)
	if emb == "" {
		t.Fatal("BuildFileEmbeddingText() returned empty string")
	}
}

func TestParserOptionsAndVariableExtraction(t *testing.T) {
	parser := NewParser(WithConcurrency(2), WithExtractImports(false))
	result, err := parser.ParseContent("v.go", `package demo

import "context"

var A int
const B = 1

func Generic[T any](a T) (x T, err error) {
	return a, nil
}
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if len(result.Nodes) < 3 {
		t.Fatalf("len(nodes) = %d, want >= 3", len(result.Nodes))
	}

	var foundVar, foundConst, foundGeneric bool
	for _, n := range result.Nodes {
		if n.FullName == "demo.A" {
			foundVar = true
			if n.Metadata["go_value_kind"] != "var" {
				t.Fatalf("go_value_kind for var = %v, want var", n.Metadata["go_value_kind"])
			}
		}
		if n.FullName == "demo.B" {
			foundConst = true
			if n.Metadata["go_value_kind"] != "const" {
				t.Fatalf("go_value_kind for const = %v, want const", n.Metadata["go_value_kind"])
			}
		}
		if n.FullName == "demo.Generic" {
			foundGeneric = true
			if n.Signature == "" {
				t.Fatal("expected generic function signature")
			}
			if len(n.Imports) != 0 {
				t.Fatalf("imports = %v, want empty when extractImports disabled", n.Imports)
			}
		}
	}
	if !foundVar || !foundConst || !foundGeneric {
		t.Fatalf("expected var/const/generic nodes, got var=%v const=%v generic=%v", foundVar, foundConst, foundGeneric)
	}
}

func TestBuildNodeEmbeddingText(t *testing.T) {
	node := &codeast.Node{
		ID:        "demo.F",
		Type:      codeast.EntityFunction,
		Name:      "F",
		FullName:  "demo.F",
		Package:   "demo",
		FilePath:  "f.go",
		Signature: "func F()",
		Comment:   "comment",
	}
	payload := BuildNodeEmbeddingText(node)
	if payload == "" {
		t.Fatal("BuildNodeEmbeddingText() returned empty payload")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded["id"] != node.ID {
		t.Fatalf("payload id = %v, want %s", decoded["id"], node.ID)
	}
}

func TestParserModuleConcurrencyUsesTotalBudget(t *testing.T) {
	tests := []struct {
		name        string
		concurrency int
		modules     int
		wantModules int
		wantPer     int
	}{
		{
			name:        "default four modules",
			concurrency: 100,
			modules:     10,
			wantModules: 4,
			wantPer:     25,
		},
		{
			name:        "small total caps modules",
			concurrency: 3,
			modules:     10,
			wantModules: 3,
			wantPer:     1,
		},
		{
			name:        "few modules",
			concurrency: 300,
			modules:     2,
			wantModules: 2,
			wantPer:     150,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser(WithConcurrency(tt.concurrency))
			gotModules, gotPer := parser.moduleConcurrency(tt.modules)
			if gotModules != tt.wantModules || gotPer != tt.wantPer {
				t.Fatalf("moduleConcurrency() = (%d, %d), want (%d, %d)",
					gotModules, gotPer, tt.wantModules, tt.wantPer)
			}
			if gotModules*gotPer > tt.concurrency {
				t.Fatalf("moduleConcurrency product = %d, want <= %d", gotModules*gotPer, tt.concurrency)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func hasCodeEdge(edges []*codeast.Edge, fromID, toID string, edgeType codeast.RelationType) bool {
	for _, edge := range edges {
		if edge.FromID == fromID && edge.ToID == toID && edge.Type == edgeType {
			return true
		}
	}
	return false
}

func hasCodeNode(nodes []*codeast.Node, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}
