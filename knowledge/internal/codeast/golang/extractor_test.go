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
	"bytes"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultExtractorExtractFileCoversTypeAndValueDecls(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, "demo.go", `package demo

import "context"

type Service struct{}
type Store interface{ Load(context.Context) error }
type ID = string

var A int
const B = 1

func NewService() *Service { return &Service{} }
func (s *Service) Do(ctx context.Context) error { return nil }
`, goparser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	e := newDefaultExtractor(2, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*goast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) < 6 {
		t.Fatalf("len(nodes) = %d, want >= 6", len(nodes))
	}

	var hasVar, hasConst, hasStruct, hasInterface, hasAlias bool
	for _, n := range nodes {
		switch n.FullName {
		case "demo.A":
			hasVar = true
		case "demo.B":
			hasConst = true
		case "demo.Service":
			hasStruct = true
		case "demo.Store":
			hasInterface = true
		case "demo.ID":
			hasAlias = true
		}
	}
	if !hasVar || !hasConst || !hasStruct || !hasInterface || !hasAlias {
		t.Fatalf("missing extracted kinds var=%v const=%v struct=%v interface=%v alias=%v", hasVar, hasConst, hasStruct, hasInterface, hasAlias)
	}
}

func TestHelperFunctionsCoverage(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, "generic.go", `package demo

func Generic[T any](x T) (y T, err error) { return x, nil }
`, goparser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	decl, ok := fileNode.Decls[0].(*goast.FuncDecl)
	if !ok {
		t.Fatal("expected first decl to be *ast.FuncDecl")
	}

	e := newDefaultExtractor(1, false)
	sig := e.buildFunctionSignature(fset, decl, "")
	if !strings.Contains(sig, "Generic[") {
		t.Fatalf("signature = %q, want generic type parameter", sig)
	}

	if got := fieldListToString(fset, decl.Type.Params); !strings.Contains(got, "x T") {
		t.Fatalf("fieldListToString(params) = %q, want contains 'x T'", got)
	}

	if !requiresResultParens(decl.Type.Results) {
		t.Fatal("requiresResultParens(results) = false, want true")
	}
	if requiresResultParens(nil) {
		t.Fatal("requiresResultParens(nil) = true, want false")
	}

	if got := receiverBaseTypeName(fset, mustParseExpr(t, "*demo.Service")); got != "Service" {
		t.Fatalf("receiverBaseTypeName(star selector) = %q, want Service", got)
	}
	if got := receiverBaseTypeName(fset, mustParseExpr(t, "(Service)")); got != "Service" {
		t.Fatalf("receiverBaseTypeName(paren) = %q, want Service", got)
	}
	if got := receiverBaseTypeName(fset, mustParseExpr(t, "S[T]")); got != "S" {
		t.Fatalf("receiverBaseTypeName(index) = %q, want S", got)
	}

	if code := getCodeWithComment(nil, decl, nil); code != "" {
		t.Fatalf("getCodeWithComment(nil, ...) = %q, want empty", code)
	}
	if code := getCodeWithGenDecl(nil, decl, nil, nil); code != "" {
		t.Fatalf("getCodeWithGenDecl(nil, ...) = %q, want empty", code)
	}

	n := newNode("Function", "F", "demo.F", "demo.F", "func F(){}", "func F()", "", filepath.Join("examples", "demo", "f.go"), 1, 1, 0)
	if n.Scope != "example" {
		t.Fatalf("newNode scope = %q, want example", n.Scope)
	}
}

func TestDefaultExtractorConfigAndEmptyInput(t *testing.T) {
	e := newDefaultExtractor(0, false)
	if e.concurrency != 100 {
		t.Fatalf("concurrency = %d, want 100", e.concurrency)
	}

	nodes, err := e.Extract(nil)
	if err != nil {
		t.Fatalf("Extract(nil) error = %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("len(nodes) = %d, want 0", len(nodes))
	}

	nodes, err = e.Extract(&extractInput{pkg: &parsedPackage{Syntax: nil}, fset: token.NewFileSet()})
	if err != nil {
		t.Fatalf("Extract(empty syntax) error = %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("len(nodes) = %d, want 0", len(nodes))
	}
}

func TestGetCodeWithGenDeclReadFileFailureFallback(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, filepath.Join(t.TempDir(), "missing.go"), "package demo\n\ntype T struct{}\n", goparser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}
	gen := fileNode.Decls[0].(*goast.GenDecl)
	spec := gen.Specs[0]
	code := getCodeWithGenDecl(fset, spec, gen, nil)
	if !strings.Contains(code, "type T struct{}") {
		t.Fatalf("getCodeWithGenDecl() fallback code = %q, want contains type T", code)
	}

	code = getCodeWithComment(fset, gen, nil)
	if !strings.Contains(code, "type T struct{}") {
		t.Fatalf("getCodeWithComment() fallback code = %q, want contains type T", code)
	}
}

func TestGetCodeWithGenDeclAndCommentFromRealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	content := `package demo

// DemoType docs.
type DemoType struct{}

type (
	// GroupType docs.
	GroupType struct{}
)
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	// Single type declaration branch: genDecl without lparen.
	genSingle, ok := fileNode.Decls[0].(*goast.GenDecl)
	if !ok {
		t.Fatal("expected first decl to be *ast.GenDecl")
	}
	specSingle := genSingle.Specs[0].(*goast.TypeSpec)
	codeSingle := getCodeWithGenDecl(fset, specSingle, genSingle, specSingle.Doc)
	if !strings.Contains(codeSingle, "type DemoType struct{}") {
		t.Fatalf("single decl code = %q, want contains type DemoType", codeSingle)
	}

	// Group declaration branch: genDecl with lparen.
	genGroup, ok := fileNode.Decls[1].(*goast.GenDecl)
	if !ok {
		t.Fatal("expected second decl to be *ast.GenDecl")
	}
	specGroup := genGroup.Specs[0].(*goast.TypeSpec)
	codeGroup := getCodeWithGenDecl(fset, specGroup, genGroup, specGroup.Doc)
	if !strings.Contains(codeGroup, "type (") || !strings.Contains(codeGroup, "GroupType") {
		t.Fatalf("group decl code = %q, want grouped type block", codeGroup)
	}

	funcCode := getCodeWithComment(fset, genSingle, genSingle.Doc)
	if !strings.Contains(funcCode, "DemoType") {
		t.Fatalf("getCodeWithComment() = %q, want contains DemoType", funcCode)
	}
}

func TestGetCodeWithHelpersFallbackBranches(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, "", "package demo\n\ntype T struct{}\n", goparser.ParseComments)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	gen, ok := fileNode.Decls[0].(*goast.GenDecl)
	if !ok {
		t.Fatal("expected decl to be *ast.GenDecl")
	}
	spec := gen.Specs[0]

	// filename empty -> fallback printer branch.
	if got := getCodeWithGenDecl(fset, spec, gen, nil); got == "" {
		t.Fatal("expected fallback printer output for getCodeWithGenDecl")
	}
	if got := getCodeWithComment(fset, gen, nil); got == "" {
		t.Fatal("expected fallback printer output for getCodeWithComment")
	}

	// Nil node and nil expr guards.
	if got := getCodeWithComment(fset, nil, nil); got != "" {
		t.Fatalf("getCodeWithComment(nil node) = %q, want empty", got)
	}
	if got := typeToString(fset, nil); got != "" {
		t.Fatalf("typeToString(nil) = %q, want empty", got)
	}

	// formatFieldList / formatField empty cases.
	if got := formatFieldList(fset, nil); got != "" {
		t.Fatalf("formatFieldList(nil) = %q, want empty", got)
	}
	if got := formatField(fset, &goast.Field{
		Names: []*goast.Ident{{Name: "a"}, {Name: "b"}},
		Type:  &goast.Ident{Name: "int"}}); !strings.Contains(got, "a, b int") {
		t.Fatalf("formatField() = %q, want 'a, b int'", got)
	}

	// Ensure typeToString non-empty path is covered.
	var b bytes.Buffer
	_ = b
	if got := typeToString(fset, &goast.Ident{Name: "int"}); got != "int" {
		t.Fatalf("typeToString(ident) = %q, want int", got)
	}
}

func mustParseExpr(t *testing.T, src string) goast.Expr {
	t.Helper()
	expr, err := goparser.ParseExpr(src)
	if err != nil {
		t.Fatalf("ParseExpr(%q) error = %v", src, err)
	}
	return expr
}
