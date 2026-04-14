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
	parser := NewParser()
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
	if len(result.Edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0", len(result.Edges))
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

func (s *Service) Do() error { return nil }
`)

	parser := NewParser()
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
