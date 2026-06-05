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
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

// --- parser.go tests ---

func TestParseDirectoryWithConcurrencyOption(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/conc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package conc\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package conc\n\nfunc B() {}\n")

	parser := NewParser(WithConcurrency(100))
	result, err := parser.ParseDirectory(dir, codeast.WithParseConcurrency(2))
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) < 2 {
		t.Fatalf("len(nodes) = %d, want >= 2", len(result.Nodes))
	}
}

func TestParseDirectoryMultiModuleConcurrent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package root\n\nfunc Root() {}\n")
	writeFile(t, filepath.Join(dir, "sub1", "go.mod"), "module example.com/root/sub1\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sub1", "s1.go"), "package sub1\n\nfunc S1() {}\n")
	writeFile(t, filepath.Join(dir, "sub2", "go.mod"), "module example.com/root/sub2\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sub2", "s2.go"), "package sub2\n\nfunc S2() {}\n")

	parser := NewParser(WithConcurrency(10))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) < 3 {
		t.Fatalf("len(nodes) = %d, want >= 3", len(result.Nodes))
	}
	if result.File == nil || result.File.Package == "" {
		t.Fatal("expected file info with package")
	}
}

func TestParseDirectoryMultiModuleSequential(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package root\n\nfunc Root() {}\n")
	writeFile(t, filepath.Join(dir, "sub1", "go.mod"), "module example.com/root/sub1\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sub1", "s1.go"), "package sub1\n\nfunc S1() {}\n")

	parser := NewParser(WithConcurrency(1))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) < 2 {
		t.Fatalf("len(nodes) = %d, want >= 2", len(result.Nodes))
	}
}

func TestParseContentNoEdgeAnalysis(t *testing.T) {
	parser := NewParser(WithEdgeAnalysis(false))
	result, err := parser.ParseContent("svc.go", `package demo

type Handler struct{}

func (h *Handler) Handle() {}
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if len(result.Edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 with edge analysis disabled", len(result.Edges))
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(result.Nodes))
	}
}

func TestParseContentMultipleTypesAndFunctions(t *testing.T) {
	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseContent("multi.go", `package demo

type Foo struct{}
type Bar struct{}

func NewFoo() *Foo { return &Foo{} }
func NewBar() *Bar { return &Bar{} }
func (f *Foo) Do() {}
func (b *Bar) Do() {}
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if len(result.Nodes) != 6 {
		t.Fatalf("len(nodes) = %d, want 6", len(result.Nodes))
	}
	if !hasCodeEdge(result.Edges, "demo.Foo", "demo.Foo.Do", codeast.RelationMethod) {
		t.Fatal("expected METHOD edge for Foo.Do")
	}
	if !hasCodeEdge(result.Edges, "demo.Bar", "demo.Bar.Do", codeast.RelationMethod) {
		t.Fatal("expected METHOD edge for Bar.Do")
	}
}

func TestWithConcurrencyNegativeAndZero(t *testing.T) {
	parser := NewParser(WithConcurrency(5))
	p := parser.withConcurrency(0)
	if p.concurrency != 1 {
		t.Fatalf("withConcurrency(0) concurrency = %d, want 1", p.concurrency)
	}
	p = parser.withConcurrency(-1)
	if p.concurrency != 1 {
		t.Fatalf("withConcurrency(-1) concurrency = %d, want 1", p.concurrency)
	}
}

func TestBuildNodeEmbeddingTextNilNode(t *testing.T) {
	if got := BuildNodeEmbeddingText(nil); got != "" {
		t.Fatalf("BuildNodeEmbeddingText(nil) = %q, want empty", got)
	}
}

func TestBuildFileEmbeddingTextEmpty(t *testing.T) {
	result := BuildFileEmbeddingText("", "test.go", "demo", nil)
	if result == "" {
		t.Fatal("BuildFileEmbeddingText() returned empty string")
	}
	if strings.Contains(result, `"code"`) {
		t.Fatal("empty content should not have code field")
	}
}

func TestMergeModuleResultsWithNilResults(t *testing.T) {
	results := []*codeast.Result{
		nil,
		{
			File:  &codeast.FileInfo{Imports: []string{"fmt"}},
			Nodes: []*codeast.Node{{ID: "a.Foo"}},
			Edges: []*codeast.Edge{{FromID: "a", ToID: "b", Type: codeast.RelationCalls}},
		},
		{
			File:  nil,
			Nodes: []*codeast.Node{{ID: "b.Bar"}},
		},
		nil,
	}
	nodes, edges, imports := mergeModuleResults(results)
	if len(nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(nodes))
	}
	if len(edges) != 1 {
		t.Fatalf("len(edges) = %d, want 1", len(edges))
	}
	if _, ok := imports["fmt"]; !ok {
		t.Fatal("expected import 'fmt'")
	}
}

func TestModuleConcurrencyEdgeCases(t *testing.T) {
	parser := NewParser(WithConcurrency(0))
	mc, pc := parser.moduleConcurrency(5)
	if mc < 1 || pc < 1 {
		t.Fatalf("moduleConcurrency with 0 concurrency: got (%d, %d)", mc, pc)
	}
}

func TestFindGoModulesSkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gitDir, "go.mod"), "module fake\n")

	modules, err := findGoModules(dir)
	if err != nil {
		t.Fatalf("findGoModules() error = %v", err)
	}
	for _, m := range modules {
		if strings.Contains(m, ".git") {
			t.Fatalf("found .git module: %s", m)
		}
	}
}

func TestSortedKeysEmpty(t *testing.T) {
	result := sortedKeys(nil)
	if len(result) != 0 {
		t.Fatalf("sortedKeys(nil) = %v, want empty", result)
	}
	result = sortedKeys(map[string]struct{}{})
	if len(result) != 0 {
		t.Fatalf("sortedKeys(empty) = %v, want empty", result)
	}
}

func TestPackageImportsMultiplePackages(t *testing.T) {
	pkgs := []*parsedPackage{
		{Imports: map[string]*parsedImport{"fmt": {Name: "fmt", PkgPath: "fmt"}}},
		{Imports: map[string]*parsedImport{"os": {Name: "os", PkgPath: "os"}, "fmt": {Name: "fmt", PkgPath: "fmt"}}},
	}
	imports := packageImports(pkgs)
	if len(imports) != 2 {
		t.Fatalf("len(imports) = %d, want 2", len(imports))
	}
}

func TestParseDirectoryModulesWithConcurrencyEmptyModules(t *testing.T) {
	parser := NewParser()
	results, err := parser.parseDirectoryModulesWithConcurrency(nil, 4, 4)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if results != nil {
		t.Fatalf("results = %v, want nil", results)
	}
}

// --- analyzer.go tests ---

func TestAnalyzerNilInput(t *testing.T) {
	a := newDefaultAnalyzer()
	edges, err := a.Analyze(nil, nil)
	if err != nil {
		t.Fatalf("Analyze(nil) error = %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0", len(edges))
	}

	edges, err = a.Analyze(&analyzeInput{pkg: nil}, nil)
	if err != nil {
		t.Fatalf("Analyze(nil pkg) error = %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0", len(edges))
	}
}

func TestAnalyzerAnalyzeFunctionNilDecl(t *testing.T) {
	a := newDefaultAnalyzer()
	var edges []*codeast.Edge
	pkg := &parsedPackage{ID: "test"}
	a.analyzeFunction(pkg, nil, nil, &edges)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for nil decl", len(edges))
	}
}

func TestAnalyzerAnalyzeTypeNilSpec(t *testing.T) {
	a := newDefaultAnalyzer()
	var edges []*codeast.Edge
	pkg := &parsedPackage{ID: "test"}
	a.analyzeType(pkg, nil, nil, &edges)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for nil spec", len(edges))
	}
}

func TestAnalyzerBasicTypeCheck(t *testing.T) {
	a := newDefaultAnalyzer()
	basics := []string{"string", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"bool", "byte", "rune", "error", "float32", "float64",
		"complex64", "complex128", "interface", "any"}
	for _, name := range basics {
		if !a.isBasicType(name) {
			t.Fatalf("isBasicType(%q) = false, want true", name)
		}
	}
	if a.isBasicType("custom") {
		t.Fatal("isBasicType(custom) = true, want false")
	}
}

func TestAnalyzerBuiltinCheck(t *testing.T) {
	a := newDefaultAnalyzer()
	builtins := []string{"len", "cap", "make", "new", "append", "panic",
		"copy", "close", "delete", "recover", "print", "println"}
	for _, name := range builtins {
		if !a.isBuiltin(name) {
			t.Fatalf("isBuiltin(%q) = false, want true", name)
		}
	}
	if a.isBuiltin("custom") {
		t.Fatal("isBuiltin(custom) = true, want false")
	}
}

func TestAnalyzerResolvePkgPathWithAliasAndDefault(t *testing.T) {
	a := newDefaultAnalyzer()
	pkg := &parsedPackage{
		ID: "test",
		Imports: map[string]*parsedImport{
			"github.com/foo/bar": {Name: "bar", PkgPath: "github.com/foo/bar"},
			"github.com/x/y":     {Name: "", PkgPath: "github.com/x/y"},
			"nil_entry":          nil,
		},
	}

	if got := a.resolvePkgPath(pkg, "bar"); got != "github.com/foo/bar" {
		t.Fatalf("resolvePkgPath(alias) = %q, want github.com/foo/bar", got)
	}
	if got := a.resolvePkgPath(pkg, "y"); got != "github.com/x/y" {
		t.Fatalf("resolvePkgPath(default) = %q, want github.com/x/y", got)
	}
	if got := a.resolvePkgPath(pkg, "unknown"); got != "" {
		t.Fatalf("resolvePkgPath(unknown) = %q, want empty", got)
	}
}

func TestAnalyzerExtractTypeNamesVariousKinds(t *testing.T) {
	a := newDefaultAnalyzer()

	identExpr := &ast.Ident{Name: "Foo"}
	names := a.extractTypeNames(identExpr)
	if len(names) != 1 || names[0] != "Foo" {
		t.Fatalf("extractTypeNames(ident) = %v, want [Foo]", names)
	}

	starExpr := &ast.StarExpr{X: &ast.Ident{Name: "Bar"}}
	names = a.extractTypeNames(starExpr)
	if len(names) != 1 || names[0] != "Bar" {
		t.Fatalf("extractTypeNames(star) = %v, want [Bar]", names)
	}

	arrayExpr := &ast.ArrayType{Elt: &ast.Ident{Name: "Baz"}}
	names = a.extractTypeNames(arrayExpr)
	if len(names) != 1 || names[0] != "Baz" {
		t.Fatalf("extractTypeNames(array) = %v, want [Baz]", names)
	}

	selectorExpr := &ast.SelectorExpr{X: &ast.Ident{Name: "pkg"}, Sel: &ast.Ident{Name: "Type"}}
	names = a.extractTypeNames(selectorExpr)
	if len(names) != 1 || !strings.Contains(names[0], "pkg") {
		t.Fatalf("extractTypeNames(selector) = %v, want contains pkg", names)
	}

	mapExpr := &ast.MapType{Key: &ast.Ident{Name: "string"}, Value: &ast.Ident{Name: "int"}}
	names = a.extractTypeNames(mapExpr)
	if len(names) != 0 {
		t.Fatalf("extractTypeNames(map) = %v, want empty", names)
	}
}

func TestAnalyzerVisitTupleTypesNil(t *testing.T) {
	a := newDefaultAnalyzer()
	var visited []types.Type
	a.visitTupleTypes(nil, func(t types.Type) {
		visited = append(visited, t)
	})
	if len(visited) != 0 {
		t.Fatalf("visitTupleTypes(nil) visited %d types, want 0", len(visited))
	}
}

func TestAnalyzerNamedTypeIDsVariousKinds(t *testing.T) {
	a := newDefaultAnalyzer()

	ids := a.namedTypeIDs(types.Typ[types.Int])
	if len(ids) != 0 {
		t.Fatalf("namedTypeIDs(basic) = %v, want empty", ids)
	}

	ids = a.namedTypeIDs(types.NewPointer(types.Typ[types.Int]))
	if len(ids) != 0 {
		t.Fatalf("namedTypeIDs(pointer to basic) = %v, want empty", ids)
	}

	ids = a.namedTypeIDs(types.NewSlice(types.Typ[types.Int]))
	if len(ids) != 0 {
		t.Fatalf("namedTypeIDs(slice of basic) = %v, want empty", ids)
	}

	ids = a.namedTypeIDs(types.NewMap(types.Typ[types.String], types.Typ[types.Int]))
	if len(ids) != 0 {
		t.Fatalf("namedTypeIDs(map of basics) = %v, want empty", ids)
	}

	ids = a.namedTypeIDs(types.NewChan(types.SendRecv, types.Typ[types.Int]))
	if len(ids) != 0 {
		t.Fatalf("namedTypeIDs(chan of basic) = %v, want empty", ids)
	}

	ids = a.namedTypeIDs(types.NewArray(types.Typ[types.Int], 10))
	if len(ids) != 0 {
		t.Fatalf("namedTypeIDs(array of basic) = %v, want empty", ids)
	}
}

func TestAnalyzerNamedTypeIDsWithNamedType(t *testing.T) {
	a := newDefaultAnalyzer()

	pkg := types.NewPackage("example.com/demo", "demo")
	typeName := types.NewTypeName(token.NoPos, pkg, "Foo", nil)
	named := types.NewNamed(typeName, types.NewStruct(nil, nil), nil)

	ids := a.namedTypeIDs(named)
	if len(ids) != 1 || ids[0] != "example.com/demo.Foo" {
		t.Fatalf("namedTypeIDs(named) = %v, want [example.com/demo.Foo]", ids)
	}

	ids = a.namedTypeIDs(types.NewPointer(named))
	if len(ids) != 1 || ids[0] != "example.com/demo.Foo" {
		t.Fatalf("namedTypeIDs(pointer to named) = %v, want [example.com/demo.Foo]", ids)
	}

	ids = a.namedTypeIDs(types.NewSlice(named))
	if len(ids) != 1 || ids[0] != "example.com/demo.Foo" {
		t.Fatalf("namedTypeIDs(slice of named) = %v, want [example.com/demo.Foo]", ids)
	}

	ids = a.namedTypeIDs(types.NewMap(types.Typ[types.String], named))
	if len(ids) != 1 || ids[0] != "example.com/demo.Foo" {
		t.Fatalf("namedTypeIDs(map val named) = %v, want [example.com/demo.Foo]", ids)
	}

	ids = a.namedTypeIDs(types.NewChan(types.SendRecv, named))
	if len(ids) != 1 || ids[0] != "example.com/demo.Foo" {
		t.Fatalf("namedTypeIDs(chan of named) = %v, want [example.com/demo.Foo]", ids)
	}
}

func TestAnalyzerImplementsWithNilTypesPackage(t *testing.T) {
	a := newDefaultAnalyzer()
	pkg := &parsedPackage{ID: "test", Types: nil}
	edges := a.analyzeImplements(pkg, nil, nil)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for nil Types", len(edges))
	}
}

func TestAnalyzerAnalyzeWithNilFilesInSyntax(t *testing.T) {
	a := newDefaultAnalyzer()
	pkg := &parsedPackage{
		ID:     "test",
		Syntax: []*ast.File{nil, nil},
		Fset:   token.NewFileSet(),
	}
	edges, err := a.Analyze(&analyzeInput{pkg: pkg}, nil)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0", len(edges))
	}
}

func TestAnalyzerExtractCallWithBuiltin(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

func Do() {
	s := make([]int, 0)
	_ = len(s)
	println("hello")
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset
	var edges []*codeast.Edge
	funcDecl := fileNode.Decls[0].(*ast.FuncDecl)
	a.analyzeFunction(pkg, funcDecl, nil, &edges)
	for _, e := range edges {
		if e.Type == codeast.RelationCalls {
			t.Fatalf("unexpected CALLS edge for builtins: %+v", e)
		}
	}
}

func TestAnalyzerExtractCallIdentNonBuiltin(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

func helper() {}
func Do() {
	helper()
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset
	nodeSet := map[string]bool{"demo.helper": true, "demo.Do": true}
	var edges []*codeast.Edge
	for _, decl := range fileNode.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "Do" {
			a.analyzeFunction(pkg, fd, nodeSet, &edges)
		}
	}
	found := false
	for _, e := range edges {
		if e.Type == codeast.RelationCalls && e.ToID == "demo.helper" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected CALLS edge to demo.helper")
	}
}

func TestAnalyzerExtractCallSelectorWithoutTypesInfo(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

import "fmt"

func Do() {
	fmt.Println("hi")
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset
	var edges []*codeast.Edge
	for _, decl := range fileNode.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			a.analyzeFunction(pkg, fd, nil, &edges)
		}
	}
	found := false
	for _, e := range edges {
		if e.Type == codeast.RelationCalls && e.ToID == "fmt.Println" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CALLS edge to fmt.Println, got %+v", edges)
	}
}

func TestAnalyzerAnalyzeTypeStructFields(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

type Inner struct{}
type Outer struct {
	Field Inner
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset
	nodeSet := map[string]bool{"demo.Inner": true, "demo.Outer": true}
	var edges []*codeast.Edge
	for _, decl := range fileNode.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					a.analyzeType(pkg, ts, nodeSet, &edges)
				}
			}
		}
	}
	found := false
	for _, e := range edges {
		if e.Type == codeast.RelationField && e.FromID == "demo.Outer" && e.ToID == "demo.Inner" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected FIELD edge from Outer to Inner, got %+v", edges)
	}
}

func TestAnalyzerAnalyzeTypeAliasOf(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

type Original struct{}
type Alias Original
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset
	nodeSet := map[string]bool{"demo.Original": true, "demo.Alias": true}
	var edges []*codeast.Edge
	for _, decl := range fileNode.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.TYPE {
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					a.analyzeType(pkg, ts, nodeSet, &edges)
				}
			}
		}
	}
	found := false
	for _, e := range edges {
		if e.Type == codeast.RelationAliasOf && e.FromID == "demo.Alias" && e.ToID == "demo.Original" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ALIAS_OF edge from Alias to Original, got %+v", edges)
	}
}

func TestAnalyzerFunctionWithParamsAndReturns(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

type Request struct{}
type Response struct{}

func Handle(req Request) Response {
	return Response{}
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset
	nodeSet := map[string]bool{"demo.Request": true, "demo.Response": true, "demo.Handle": true}
	var edges []*codeast.Edge
	for _, decl := range fileNode.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			a.analyzeFunction(pkg, fd, nodeSet, &edges)
		}
	}
	hasParam := false
	hasReturns := false
	for _, e := range edges {
		if e.Type == codeast.RelationParam && e.FromID == "demo.Handle" && e.ToID == "demo.Request" {
			hasParam = true
		}
		if e.Type == codeast.RelationReturns && e.FromID == "demo.Handle" && e.ToID == "demo.Response" {
			hasReturns = true
		}
	}
	if !hasParam {
		t.Fatalf("expected PARAM edge, got %+v", edges)
	}
	if !hasReturns {
		t.Fatalf("expected RETURNS edge, got %+v", edges)
	}
}

func TestAnalyzerFunctionNoBody(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

type iface interface {
	Do()
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset
	edges, err := a.Analyze(&analyzeInput{pkg: pkg}, nil)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	for _, e := range edges {
		if e.Type == codeast.RelationCalls {
			t.Fatalf("unexpected CALLS edge from interface method: %+v", e)
		}
	}
}

func TestAnalyzerExtractTypeIDsWithTypesInfo(t *testing.T) {
	a := newDefaultAnalyzer()

	demoPkg := types.NewPackage("example.com/demo", "demo")
	fooTypeName := types.NewTypeName(token.NoPos, demoPkg, "Foo", nil)
	fooNamed := types.NewNamed(fooTypeName, types.NewStruct(nil, nil), nil)

	pkg := &parsedPackage{
		ID:   "example.com/demo",
		Name: "demo",
		TypesInfo: &types.Info{
			Types: map[ast.Expr]types.TypeAndValue{},
		},
	}

	expr := &ast.Ident{Name: "Foo"}
	pkg.TypesInfo.Types[expr] = types.TypeAndValue{Type: fooNamed}

	ids := a.extractTypeIDs(pkg, expr)
	if len(ids) != 1 || ids[0] != "example.com/demo.Foo" {
		t.Fatalf("extractTypeIDs(with TypesInfo) = %v, want [example.com/demo.Foo]", ids)
	}
}

func TestAnalyzerExtractTypeIDsQualifiedNameWithImport(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	pkg := &parsedPackage{
		ID:   "example.com/app",
		Name: "app",
		Imports: map[string]*parsedImport{
			"example.com/lib": {Name: "lib", PkgPath: "example.com/lib"},
		},
	}

	expr := &ast.SelectorExpr{X: &ast.Ident{Name: "lib"}, Sel: &ast.Ident{Name: "Foo"}}
	ids := a.extractTypeIDs(pkg, expr)
	if len(ids) != 1 || ids[0] != "example.com/lib.Foo" {
		t.Fatalf("extractTypeIDs(qualified) = %v, want [example.com/lib.Foo]", ids)
	}
}

func TestAnalyzerTypeToString(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()
	expr := &ast.Ident{Name: "int"}
	if got := a.typeToString(expr); got != "int" {
		t.Fatalf("typeToString() = %q, want int", got)
	}
}

// --- extractor.go tests ---

func TestExtractorExtractTypeGenericStruct(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

type Container[T any] struct {
	Value T
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if !strings.Contains(nodes[0].Signature, "[T any]") {
		t.Fatalf("signature = %q, want contains [T any]", nodes[0].Signature)
	}
}

func TestExtractorExtractTypeInterface(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

// Handler handles requests.
type Handler interface {
	Handle() error
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Type != codeast.EntityInterface {
		t.Fatalf("type = %s, want Interface", nodes[0].Type)
	}
	if !strings.Contains(nodes[0].Signature, "interface") {
		t.Fatalf("signature = %q, want contains interface", nodes[0].Signature)
	}
	if nodes[0].Comment != "Handler handles requests." {
		t.Fatalf("comment = %q, want 'Handler handles requests.'", nodes[0].Comment)
	}
}

func TestExtractorExtractTypeAlias(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

type StringAlias = string
type IntDef int
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, false)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(nodes))
	}
	for _, n := range nodes {
		if n.Type != codeast.EntityAlias {
			t.Fatalf("type = %s, want Alias for %s", n.Type, n.Name)
		}
	}
	aliasMeta := nodes[0].Metadata["go_type_kind"]
	defMeta := nodes[1].Metadata["go_type_kind"]
	if aliasMeta != "alias" {
		t.Fatalf("StringAlias go_type_kind = %v, want alias", aliasMeta)
	}
	if defMeta != "definition" {
		t.Fatalf("IntDef go_type_kind = %v, want definition", defMeta)
	}
}

func TestExtractorExtractFunctionWithReceiverNames(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

type Svc struct{}

func (s *Svc) Do(ctx int) (result string, err error) {
	return "", nil
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	var method *codeast.Node
	for _, n := range nodes {
		if n.Type == codeast.EntityMethod {
			method = n
		}
	}
	if method == nil {
		t.Fatal("expected a method node")
	}
	if method.ID != "demo.Svc.Do" {
		t.Fatalf("id = %s, want demo.Svc.Do", method.ID)
	}
	if !strings.Contains(method.Signature, "(s *Svc)") {
		t.Fatalf("signature = %q, want contains (s *Svc)", method.Signature)
	}
	if !strings.Contains(method.Signature, "(result string, err error)") {
		t.Fatalf("signature = %q, want contains result return", method.Signature)
	}
	if method.Metadata[codeast.MetadataKeyReceiverType] != "*Svc" {
		t.Fatalf("receiver_type = %v, want *Svc", method.Metadata[codeast.MetadataKeyReceiverType])
	}
}

func TestExtractorExtractVariableMultipleNames(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

var X, Y int
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 2 {
		t.Fatalf("len(nodes) = %d, want 2", len(nodes))
	}
	if nodes[0].ID != "demo.X" || nodes[1].ID != "demo.Y" {
		t.Fatalf("ids = [%s, %s], want [demo.X, demo.Y]", nodes[0].ID, nodes[1].ID)
	}
}

func TestExtractorExtractWithNilFset(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

func F() {}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes, err := e.Extract(&extractInput{pkg: pkg, fset: nil})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least one node")
	}
}

func TestExtractorExtractConstBlock(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

const (
	A = 1
	B = 2
	C = 3
)
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}
	for _, n := range nodes {
		if n.Metadata["go_value_kind"] != "const" {
			t.Fatalf("go_value_kind = %v, want const", n.Metadata["go_value_kind"])
		}
	}
}

func TestExtractorConcurrentMultipleFiles(t *testing.T) {
	fset := token.NewFileSet()
	file1, _ := goparser.ParseFile(fset, "a.go", "package demo\n\nfunc A() {}\n", goparser.ParseComments)
	file2, _ := goparser.ParseFile(fset, "b.go", "package demo\n\nfunc B() {}\n", goparser.ParseComments)
	file3, _ := goparser.ParseFile(fset, "c.go", "package demo\n\nfunc C() {}\n", goparser.ParseComments)

	e := newDefaultExtractor(2, true)
	pkg := &parsedPackage{
		ID:     "demo",
		Name:   "demo",
		Syntax: []*ast.File{file1, file2, file3},
		Fset:   fset,
	}
	nodes, err := e.Extract(&extractInput{pkg: pkg, fset: fset})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("len(nodes) = %d, want 3", len(nodes))
	}
}

// --- integration: ParseDirectory edge cases ---

func TestParseDirectorySkipsSubModulesInDirectAST(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Main() {}\n")
	writeFile(t, filepath.Join(dir, "sub", "go.mod"), "module example.com/sub\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "sub", "sub.go"), "package sub\n\nfunc Sub() {}\n")

	parser := NewParser()
	result, err := parser.parseDirectoryDirectAST(dir)
	if err != nil {
		t.Fatalf("parseDirectoryDirectAST() error = %v", err)
	}
	for _, n := range result.Nodes {
		if strings.Contains(n.ID, "sub.Sub") {
			t.Fatal("expected sub-module to be skipped in direct AST")
		}
	}
}

func TestParseDirectoryFullModeFallsBackToDirectAST(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Main() {}\n")

	parser := NewParser()
	result, err := parser.parseDirectoryModule(dir)
	if err != nil {
		t.Fatalf("parseDirectoryModule() error = %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least one node after fallback")
	}
}

func TestAnalyzePackagesWithConcurrency(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a", "a.go"), "package a\n\ntype A struct{}\n")
	writeFile(t, filepath.Join(dir, "b", "b.go"), "package b\n\ntype B struct{}\n")
	writeFile(t, filepath.Join(dir, "c", "c.go"), "package c\n\ntype C struct{}\n")

	parser := NewParser(WithConcurrency(3), WithEdgeAnalysis(true))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) < 3 {
		t.Fatalf("len(nodes) = %d, want >= 3", len(result.Nodes))
	}
}

func TestPackageInterfacesEmptyAndNilPackages(t *testing.T) {
	result := packageInterfaces(nil, nil)
	if len(result) != 0 {
		t.Fatalf("len(interfaces) = %d, want 0", len(result))
	}
	result = packageInterfaces([]*parsedPackage{nil, {Types: nil}}, nil)
	if len(result) != 0 {
		t.Fatalf("len(interfaces) = %d, want 0", len(result))
	}
}

func TestAnalyzerImplementsNilInterfaceEntries(t *testing.T) {
	a := newDefaultAnalyzer()
	pkg := types.NewPackage("example.com/demo", "demo")
	scope := pkg.Scope()
	structType := types.NewStruct(nil, nil)
	typeName := types.NewTypeName(token.NoPos, pkg, "Foo", nil)
	_ = types.NewNamed(typeName, structType, nil)
	scope.Insert(typeName)

	parsedPkg := &parsedPackage{
		ID:    "example.com/demo",
		Name:  "demo",
		Types: pkg,
	}
	interfaces := []*interfaceType{
		nil,
		{id: "", iface: nil},
		{id: "some.iface", iface: nil},
	}
	edges := a.analyzeImplements(parsedPkg, interfaces, nil)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for nil interface entries", len(edges))
	}
}

func TestExtractImportsFromASTFile(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, _ := goparser.ParseFile(fset, "demo.go", `package demo

import (
	"fmt"
	"os"
	"context"
)
`, goparser.ParseComments)

	imports := extractImportsFromASTFile(fileNode)
	if len(imports) != 3 {
		t.Fatalf("len(imports) = %d, want 3", len(imports))
	}
}

func TestParseContentWithNilNodeInResult(t *testing.T) {
	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseContent("empty.go", `package demo
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if len(result.Nodes) != 0 {
		t.Fatalf("len(nodes) = %d, want 0", len(result.Nodes))
	}
}

func TestAnalyzerExtractTypeIDsWithEmptyTypesInfoNoMatch(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	pkg := &parsedPackage{
		ID:   "demo",
		Name: "demo",
		TypesInfo: &types.Info{
			Types: map[ast.Expr]types.TypeAndValue{},
		},
	}

	expr := &ast.Ident{Name: "CustomType"}
	ids := a.extractTypeIDs(pkg, expr)
	if len(ids) != 1 || ids[0] != "demo.CustomType" {
		t.Fatalf("extractTypeIDs(no typesinfo match) = %v, want [demo.CustomType]", ids)
	}
}

func TestAnalyzerNamedTypeIDsSignatureType(t *testing.T) {
	a := newDefaultAnalyzer()

	demoPkg := types.NewPackage("example.com/demo", "demo")
	fooTypeName := types.NewTypeName(token.NoPos, demoPkg, "Foo", nil)
	fooNamed := types.NewNamed(fooTypeName, types.NewStruct(nil, nil), nil)

	params := types.NewTuple(types.NewVar(token.NoPos, nil, "x", fooNamed))
	results := types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.Int]))
	sig := types.NewSignatureType(nil, nil, nil, params, results, false)

	ids := a.namedTypeIDs(sig)
	if len(ids) != 1 || ids[0] != "example.com/demo.Foo" {
		t.Fatalf("namedTypeIDs(signature) = %v, want [example.com/demo.Foo]", ids)
	}
}

func TestLooksLikeLocalPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"absolute", "/foo/bar.go", true},
		{"relative with sep", "foo/bar.go", true},
		{"bare filename", "bar.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeLocalPath(tt.path); got != tt.want {
				t.Fatalf("looksLikeLocalPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestParsedPackageFromPackagesNilImports(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, _ := goparser.ParseFile(fset, "demo.go", "package demo\n", goparser.ParseComments)

	pkg := &parsedPackage{
		ID:     "demo",
		Name:   "demo",
		Syntax: []*ast.File{fileNode},
		Fset:   fset,
		Imports: map[string]*parsedImport{
			"nil_pkg": nil,
		},
	}
	if pkg.Imports["nil_pkg"] != nil {
		t.Fatal("expected nil import entry")
	}
}

func TestExtractorExtractFunctionWithDocComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.go")
	content := `package demo

// Do performs the main operation.
// It does multiple things.
func Do() {}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Comment == "" {
		t.Fatal("expected doc comment on function")
	}
	if !strings.Contains(nodes[0].Code, "// Do performs") {
		t.Fatalf("code = %q, want contains doc comment", nodes[0].Code)
	}
	if nodes[0].LineStart != 3 {
		t.Fatalf("lineStart = %d, want 3 (doc comment start)", nodes[0].LineStart)
	}
}

func TestExtractorExtractFunctionSingleReturn(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

func Get() string { return "" }
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if !strings.Contains(nodes[0].Signature, " string") {
		t.Fatalf("signature = %q, want contains ' string' (no parens for single result)", nodes[0].Signature)
	}
	if strings.Contains(nodes[0].Signature, "(string)") {
		t.Fatal("single return should not be parenthesized")
	}
}

func TestAnalyzerExtractCallSelectorWithTypesInfoAndPkgName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "lib", "lib.go"), `package lib

func Helper() {}
`)
	writeFile(t, filepath.Join(dir, "app", "app.go"), `package app

import "example.com/demo/lib"

func Do() {
	lib.Helper()
}
`)

	parser := NewParser(WithEdgeAnalysis(true), WithConcurrency(1))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo/app.Do", "example.com/demo/lib.Helper", codeast.RelationCalls) {
		t.Fatalf("expected cross-pkg CALLS edge via PkgName resolution, got %+v", result.Edges)
	}
}

func TestAnalyzerExtractCallMethodOnTypedReceiver(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "svc.go"), `package demo

type Svc struct{}

func (s *Svc) Run() {}

func Do() {
	s := &Svc{}
	s.Run()
}
`)

	parser := NewParser(WithEdgeAnalysis(true), WithConcurrency(1))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo.Do", "example.com/demo.Svc.Run", codeast.RelationCalls) {
		t.Fatalf("expected method call edge via typed receiver, got %+v", result.Edges)
	}
}

func TestReceiverBaseTypeNameIndexListExpr(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

type Container[K comparable, V any] struct{}

func (c *Container[K, V]) Get() {}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	var method *codeast.Node
	for _, n := range nodes {
		if n.Type == codeast.EntityMethod {
			method = n
		}
	}
	if method == nil {
		t.Fatal("expected method node")
	}
	if method.ID != "demo.Container.Get" {
		t.Fatalf("id = %s, want demo.Container.Get", method.ID)
	}
}

func TestParseDirectoryReturnsImportsFromMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package demo\n\nimport \"fmt\"\n\nfunc A() { fmt.Println() }\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package demo\n\nimport \"os\"\n\nfunc B() { os.Exit(0) }\n")

	parser := NewParser()
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if result.File == nil {
		t.Fatal("expected file info")
	}
	hasFmt := false
	hasOS := false
	for _, imp := range result.File.Imports {
		if imp == "fmt" {
			hasFmt = true
		}
		if imp == "os" {
			hasOS = true
		}
	}
	if !hasFmt || !hasOS {
		t.Fatalf("imports = %v, want contains fmt and os", result.File.Imports)
	}
}

func TestFieldListToStringWithNames(t *testing.T) {
	fset := token.NewFileSet()
	fields := &ast.FieldList{
		List: []*ast.Field{
			{
				Names: []*ast.Ident{{Name: "x"}, {Name: "y"}},
				Type:  &ast.Ident{Name: "int"},
			},
			{
				Type: &ast.Ident{Name: "error"},
			},
		},
	}
	result := fieldListToString(fset, fields)
	if !strings.Contains(result, "x, y int") {
		t.Fatalf("fieldListToString = %q, want contains 'x, y int'", result)
	}
	if !strings.Contains(result, "error") {
		t.Fatalf("fieldListToString = %q, want contains 'error'", result)
	}
}

// --- parser.go additional coverage ---

func TestParseFileInfoInvalidGoReturnsError(t *testing.T) {
	parser := NewParser()
	_, err := parser.ParseFileInfo("bad.go", "package demo\nfunc Bad( {")
	if err == nil {
		t.Fatal("expected ParseFileInfo() to return error for invalid Go")
	}
}

func TestParseContentWithNilIDNodeFiltering(t *testing.T) {
	parser := NewParser(WithEdgeAnalysis(true))
	result, err := parser.ParseContent("svc.go", `package demo

type Svc struct{}
func (s *Svc) Do() {}
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	for _, n := range result.Nodes {
		if n.ID == "" {
			t.Fatal("node with empty ID should be filtered from nodeSet")
		}
	}
}

func TestParseDirectorySameConcurrencyNoRecreation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/same\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package same\n\nfunc A() {}\n")

	parser := NewParser(WithConcurrency(5))
	result, err := parser.ParseDirectory(dir, codeast.WithParseConcurrency(5))
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least one node")
	}
}

func TestParseDirectoryZeroConcurrencyOption(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/zc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package zc\n\nfunc A() {}\n")

	parser := NewParser(WithConcurrency(5))
	result, err := parser.ParseDirectory(dir, codeast.WithParseConcurrency(0))
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) == 0 {
		t.Fatal("expected at least one node")
	}
}

func TestParseDirectoryNonExistentDir(t *testing.T) {
	parser := NewParser()
	_, err := parser.ParseDirectory("/nonexistent/path/to/dir")
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

func TestModuleConcurrencyWithOneModule(t *testing.T) {
	parser := NewParser(WithConcurrency(100))
	mc, pc := parser.moduleConcurrency(1)
	if mc != 1 {
		t.Fatalf("moduleConcurrency(1) module = %d, want 1", mc)
	}
	if pc != 100 {
		t.Fatalf("moduleConcurrency(1) per = %d, want 100", pc)
	}
}

func TestModuleConcurrencyNegativeTotal(t *testing.T) {
	parser := &Parser{concurrency: -5}
	mc, pc := parser.moduleConcurrency(3)
	if mc < 1 || pc < 1 {
		t.Fatalf("moduleConcurrency with negative: got (%d, %d)", mc, pc)
	}
}

func TestParseDirectoryModulesWithConcurrencySequential(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/seq\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package seq\n\nfunc A() {}\n")
	sub := filepath.Join(dir, "sub")
	writeFile(t, filepath.Join(sub, "go.mod"), "module example.com/seq/sub\n\ngo 1.21\n")
	writeFile(t, filepath.Join(sub, "b.go"), "package sub\n\nfunc B() {}\n")

	parser := NewParser(WithConcurrency(1))
	results, err := parser.parseDirectoryModulesWithConcurrency([]string{dir, sub}, 1, 1)
	if err != nil {
		t.Fatalf("parseDirectoryModulesWithConcurrency() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
}

func TestAnalyzePackagesEmpty(t *testing.T) {
	parser := NewParser(WithEdgeAnalysis(true))
	edges, err := parser.analyzePackages(nil, nil)
	if err != nil {
		t.Fatalf("analyzePackages(nil) error = %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0", len(edges))
	}
}

func TestAnalyzePackagesSequentialSinglePkg(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "svc.go"), `package demo

type Runner interface{ Run() }
type Worker struct{}
func (w Worker) Run() {}
`)

	parser := NewParser(WithEdgeAnalysis(true), WithConcurrency(1))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/demo.Worker", "example.com/demo.Runner", codeast.RelationImplements) {
		t.Fatalf("expected IMPLEMENTS edge with single-pkg sequential analysis, got %+v", result.Edges)
	}
}

func TestParseDirectoryDirectASTWithNonGoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package demo\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(dir, "readme.txt"), "not a go file")
	writeFile(t, filepath.Join(dir, "data.json"), "{}")

	parser := NewParser()
	result, err := parser.parseDirectoryDirectAST(dir)
	if err != nil {
		t.Fatalf("parseDirectoryDirectAST() error = %v", err)
	}
	if len(result.Nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(result.Nodes))
	}
}

func TestParseDirectoryDirectASTWithMultipleDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/multi\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Main() {}\n")
	writeFile(t, filepath.Join(dir, "pkg", "svc.go"), "package pkg\n\nfunc Svc() {}\n")

	parser := NewParser()
	result, err := parser.parseDirectoryDirectAST(dir)
	if err != nil {
		t.Fatalf("parseDirectoryDirectAST() error = %v", err)
	}
	if len(result.Nodes) < 2 {
		t.Fatalf("len(nodes) = %d, want >= 2", len(result.Nodes))
	}
	if result.File.Package != "example.com/multi" {
		t.Fatalf("package = %s, want example.com/multi", result.File.Package)
	}
}

func TestParseDirectoryDirectASTNoGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package demo\n\nfunc A() {}\n")

	parser := NewParser()
	result, err := parser.parseDirectoryDirectAST(dir)
	if err != nil {
		t.Fatalf("parseDirectoryDirectAST() error = %v", err)
	}
	if result.File.Package != filepath.Base(dir) {
		t.Fatalf("package = %s, want %s", result.File.Package, filepath.Base(dir))
	}
}

func TestParseDirectoryDirectASTMergesImportsFromMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package demo\n\nimport \"fmt\"\n\nfunc A() { fmt.Println() }\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package demo\n\nimport \"os\"\n\nfunc B() { os.Exit(0) }\n")
	writeFile(t, filepath.Join(dir, "c.go"), "package demo\n\nimport \"fmt\"\n\nfunc C() { fmt.Println() }\n")

	parser := NewParser()
	result, err := parser.parseDirectoryDirectAST(dir)
	if err != nil {
		t.Fatalf("parseDirectoryDirectAST() error = %v", err)
	}
	fmtCount := 0
	for _, imp := range result.File.Imports {
		if imp == "fmt" {
			fmtCount++
		}
	}
	if fmtCount != 1 {
		t.Fatalf("duplicate import 'fmt' found %d times, want 1", fmtCount)
	}
}

func TestResolvePackagePathRelPathStartsWithDotDot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n")
	outsideDir := filepath.Join(dir, "..", "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatal(err)
	}

	got := resolvePackagePath(filepath.Join(outsideDir, "x.go"), "outside")
	if got != "outside" {
		t.Fatalf("resolvePackagePath(outside module) = %q, want outside", got)
	}
}

func TestModulePathForDirWithNoGoModule(t *testing.T) {
	dir := t.TempDir()
	got := modulePathForDir(dir, dir)
	if got != filepath.Base(dir) {
		t.Fatalf("modulePathForDir(no module) = %s, want %s", got, filepath.Base(dir))
	}
}

func TestParseGoModulePathIgnoresNonModuleLines(t *testing.T) {
	dir := t.TempDir()
	modPath := filepath.Join(dir, "go.mod")
	writeFile(t, modPath, "go 1.21\nrequire something v1.0.0\nmodule example.com/late\n")
	if got := parseGoModulePath(modPath); got != "example.com/late" {
		t.Fatalf("parseGoModulePath() = %q, want example.com/late", got)
	}
}

func TestWithConcurrencyIgnoresNegative(t *testing.T) {
	cfg := &parserConfig{concurrency: 10}
	WithConcurrency(-5)(cfg)
	if cfg.concurrency != 10 {
		t.Fatalf("concurrency = %d, want 10 (unchanged)", cfg.concurrency)
	}
	WithConcurrency(0)(cfg)
	if cfg.concurrency != 10 {
		t.Fatalf("concurrency = %d, want 10 (unchanged)", cfg.concurrency)
	}
}

func TestFindNearestGoModuleAtRoot(t *testing.T) {
	moduleDir, modulePath := findNearestGoModule("/")
	if moduleDir != "" || modulePath != "" {
		t.Fatalf("findNearestGoModule(/) = (%s, %s), want empty", moduleDir, modulePath)
	}
}

// --- analyzer.go additional coverage ---

func TestAnalyzerAnalyzeWithNilFsetInPkg(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, "demo.go", "package demo\n\nfunc Do() {}\n", goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	pkg := &parsedPackage{
		ID:     "demo",
		Name:   "demo",
		Syntax: []*ast.File{fileNode},
		Fset:   nil,
	}
	edges, err := a.Analyze(&analyzeInput{pkg: pkg}, nil)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	_ = edges
}

func TestAnalyzerAnalyzeFunctionWithReceiverTypeToStringFallback(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	decl := &ast.FuncDecl{
		Name: &ast.Ident{Name: "Do"},
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{Type: &ast.MapType{
					Key:   &ast.Ident{Name: "string"},
					Value: &ast.Ident{Name: "int"},
				}},
			},
		},
		Type: &ast.FuncType{},
		Body: &ast.BlockStmt{},
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo"}
	var edges []*codeast.Edge
	a.analyzeFunction(pkg, decl, nil, &edges)
	if len(edges) == 0 {
		t.Fatal("expected at least one METHOD edge")
	}
}

func TestAnalyzerAnalyzeFunctionWithNilName(t *testing.T) {
	a := newDefaultAnalyzer()
	decl := &ast.FuncDecl{
		Name: nil,
		Type: &ast.FuncType{},
	}
	pkg := &parsedPackage{ID: "test"}
	var edges []*codeast.Edge
	a.analyzeFunction(pkg, decl, nil, &edges)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for nil func name", len(edges))
	}
}

func TestAnalyzerExtractCallNonIdentSelectorX(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X: &ast.CompositeLit{
				Type: &ast.Ident{Name: "Foo"},
			},
			Sel: &ast.Ident{Name: "Bar"},
		},
	}
	pkg := &parsedPackage{ID: "demo"}
	var edges []*codeast.Edge
	a.extractCall(pkg, call, "demo.Caller", nil, &edges)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for non-ident selector X", len(edges))
	}
}

func TestAnalyzerExtractCallFuncExprType(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	call := &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{},
			Body: &ast.BlockStmt{},
		},
	}
	pkg := &parsedPackage{ID: "demo"}
	var edges []*codeast.Edge
	a.extractCall(pkg, call, "demo.Caller", nil, &edges)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for func literal call", len(edges))
	}
}

func TestAnalyzerExtractCallFilteredByNodeSet(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()
	fset := token.NewFileSet()
	src := `package demo

func target() {}
func caller() { target() }
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	a.fset = fset

	nodeSet := map[string]bool{"demo.caller": true}
	var edges []*codeast.Edge
	for _, decl := range fileNode.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "caller" {
			a.analyzeFunction(pkg, fd, nodeSet, &edges)
		}
	}
	for _, e := range edges {
		if e.Type == codeast.RelationCalls && e.ToID == "demo.target" {
			t.Fatal("CALLS edge to demo.target should be filtered by nodeSet")
		}
	}
}

func TestAnalyzerNamedTypeIDsNilObjPkg(t *testing.T) {
	a := newDefaultAnalyzer()
	tn := types.NewTypeName(token.NoPos, nil, "Orphan", nil)
	named := types.NewNamed(tn, types.NewStruct(nil, nil), nil)
	ids := a.namedTypeIDs(named)
	if len(ids) != 0 {
		t.Fatalf("namedTypeIDs(nil pkg) = %v, want empty", ids)
	}
}

func TestAnalyzerNamedTypeIDsDedup(t *testing.T) {
	a := newDefaultAnalyzer()
	pkg := types.NewPackage("example.com/demo", "demo")
	tn := types.NewTypeName(token.NoPos, pkg, "Foo", nil)
	named := types.NewNamed(tn, types.NewStruct(nil, nil), nil)

	mapType := types.NewMap(named, named)
	ids := a.namedTypeIDs(mapType)
	if len(ids) != 1 {
		t.Fatalf("namedTypeIDs(map[Foo]Foo) = %v, want 1 unique id", ids)
	}
}

func TestAnalyzerExtractTypeDepsWithNilNodeSet(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()
	pkg := &parsedPackage{ID: "demo", Name: "demo"}
	var edges []*codeast.Edge
	a.extractTypeDeps(pkg, &ast.Ident{Name: "Foo"}, "demo.Bar", codeast.RelationField, nil, &edges)
	if len(edges) != 1 {
		t.Fatalf("len(edges) = %d, want 1 with nil nodeSet", len(edges))
	}
}

func TestAnalyzerExtractTypeDepsEmptyToID(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()
	pkg := &parsedPackage{ID: "demo", Name: "demo"}
	var edges []*codeast.Edge
	a.extractTypeDeps(pkg, &ast.Ident{Name: "int"}, "demo.Bar", codeast.RelationField, nil, &edges)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for basic type", len(edges))
	}
}

func TestAnalyzerAnalyzeTypeInterfaceNoAliasEdge(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	spec := &ast.TypeSpec{
		Name: &ast.Ident{Name: "Doer"},
		Type: &ast.InterfaceType{
			Methods: &ast.FieldList{},
		},
	}
	pkg := &parsedPackage{ID: "demo", Name: "demo"}
	var edges []*codeast.Edge
	a.analyzeType(pkg, spec, nil, &edges)
	for _, e := range edges {
		if e.Type == codeast.RelationAliasOf {
			t.Fatal("interface should not produce ALIAS_OF edge")
		}
	}
}

func TestAnalyzerImplementsNodeSetFiltering(t *testing.T) {
	a := newDefaultAnalyzer()

	pkg := types.NewPackage("example.com/demo", "demo")
	scope := pkg.Scope()

	doMethod := types.NewFunc(token.NoPos, pkg, "Do", types.NewSignatureType(nil, nil, nil, nil, nil, false))
	ifaceMethods := []*types.Func{doMethod}
	iface := types.NewInterfaceType(ifaceMethods, nil)
	iface.Complete()

	ifaceTypeName := types.NewTypeName(token.NoPos, pkg, "Doer", nil)
	_ = types.NewNamed(ifaceTypeName, iface, nil)
	scope.Insert(ifaceTypeName)

	structType := types.NewStruct(nil, nil)
	structTypeName := types.NewTypeName(token.NoPos, pkg, "Impl", nil)
	structNamed := types.NewNamed(structTypeName, structType, nil)
	structNamed.AddMethod(types.NewFunc(token.NoPos, pkg, "Do",
		types.NewSignatureType(types.NewVar(token.NoPos, pkg, "i", structNamed), nil, nil, nil, nil, false)))
	scope.Insert(structTypeName)

	parsedPkg := &parsedPackage{ID: "example.com/demo", Types: pkg}
	interfaces := []*interfaceType{{id: "example.com/demo.Doer", iface: iface, external: false}}

	nodeSet := map[string]bool{"example.com/demo.Impl": true}
	edges := a.analyzeImplements(parsedPkg, interfaces, nodeSet)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 when interface not in nodeSet", len(edges))
	}

	nodeSet["example.com/demo.Doer"] = true
	edges = a.analyzeImplements(parsedPkg, interfaces, nodeSet)
	if len(edges) != 1 {
		t.Fatalf("len(edges) = %d, want 1 when both in nodeSet", len(edges))
	}
}

func TestAnalyzerImplementsExternalInterface(t *testing.T) {
	a := newDefaultAnalyzer()

	pkg := types.NewPackage("example.com/demo", "demo")
	scope := pkg.Scope()

	extPkg := types.NewPackage("example.com/ext", "ext")
	doMethod := types.NewFunc(token.NoPos, extPkg, "Do", types.NewSignatureType(nil, nil, nil, nil, nil, false))
	iface := types.NewInterfaceType([]*types.Func{doMethod}, nil)
	iface.Complete()

	structType := types.NewStruct(nil, nil)
	structTypeName := types.NewTypeName(token.NoPos, pkg, "Impl", nil)
	structNamed := types.NewNamed(structTypeName, structType, nil)
	structNamed.AddMethod(types.NewFunc(token.NoPos, pkg, "Do",
		types.NewSignatureType(types.NewVar(token.NoPos, pkg, "i", structNamed), nil, nil, nil, nil, false)))
	scope.Insert(structTypeName)

	parsedPkg := &parsedPackage{ID: "example.com/demo", Types: pkg}
	interfaces := []*interfaceType{{id: "example.com/ext.Doer", iface: iface, external: true}}

	nodeSet := map[string]bool{"example.com/demo.Impl": true}
	edges := a.analyzeImplements(parsedPkg, interfaces, nodeSet)
	if len(edges) != 1 {
		t.Fatalf("len(edges) = %d, want 1 for external interface implementation", len(edges))
	}
}

func TestAnalyzerImplementsSkipsNonStruct(t *testing.T) {
	a := newDefaultAnalyzer()

	pkg := types.NewPackage("example.com/demo", "demo")
	scope := pkg.Scope()

	tn := types.NewTypeName(token.NoPos, pkg, "MyInt", nil)
	_ = types.NewNamed(tn, types.Typ[types.Int], nil)
	scope.Insert(tn)

	parsedPkg := &parsedPackage{ID: "example.com/demo", Types: pkg}
	edges := a.analyzeImplements(parsedPkg, nil, nil)
	if len(edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 for non-struct type", len(edges))
	}
}

func TestAnalyzerExtractTypeIDsQualifiedNameTwoPartsWithImport(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	pkg := &parsedPackage{
		ID:   "demo",
		Name: "demo",
		Imports: map[string]*parsedImport{
			"example.com/lib": {Name: "", PkgPath: "example.com/lib"},
		},
	}

	expr := &ast.SelectorExpr{X: &ast.Ident{Name: "lib"}, Sel: &ast.Ident{Name: "Thing"}}
	ids := a.extractTypeIDs(pkg, expr)
	if len(ids) != 1 || ids[0] != "example.com/lib.Thing" {
		t.Fatalf("extractTypeIDs(default import) = %v, want [example.com/lib.Thing]", ids)
	}
}

func TestAnalyzerExtractTypeIDsQualifiedNameUnresolvablePkg(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	pkg := &parsedPackage{
		ID:      "demo",
		Name:    "demo",
		Imports: map[string]*parsedImport{},
	}

	expr := &ast.SelectorExpr{X: &ast.Ident{Name: "unknown"}, Sel: &ast.Ident{Name: "Foo"}}
	ids := a.extractTypeIDs(pkg, expr)
	if len(ids) != 0 {
		t.Fatalf("extractTypeIDs(unresolvable) = %v, want empty", ids)
	}
}

// --- extractor.go additional coverage ---

func TestPrintNodeWithGenDeclPrefixNilGenDecl(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, "", "package demo\n\ntype T struct{}\n", goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	gen := fileNode.Decls[0].(*ast.GenDecl)
	spec := gen.Specs[0]
	got := printNodeWithGenDeclPrefix(fset, spec, nil)
	if got == "" {
		t.Fatal("printNodeWithGenDeclPrefix(nil genDecl) returned empty")
	}
	if strings.HasPrefix(got, "type ") {
		t.Fatalf("printNodeWithGenDeclPrefix(nil genDecl) = %q, should not have 'type ' prefix", got)
	}
}

func TestCodeStartLineWithDocAfterFallback(t *testing.T) {
	fset := token.NewFileSet()
	path := filepath.Join(t.TempDir(), "demo.go")
	content := `package demo

func Do() {}

// Late comment.
func After() {}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fileNode, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	afterDecl := fileNode.Decls[1].(*ast.FuncDecl)
	docPos := fset.Position(afterDecl.Doc.Pos())
	funcPos := fset.Position(afterDecl.Pos())
	got := codeStartLineWithDoc(fset, afterDecl.Doc, funcPos.Line)
	if got != docPos.Line {
		t.Fatalf("codeStartLineWithDoc = %d, want %d", got, docPos.Line)
	}

	got = codeStartLineWithDoc(fset, afterDecl.Doc, 1)
	if got != 1 {
		t.Fatalf("codeStartLineWithDoc(fallback < doc) = %d, want 1", got)
	}
}

func TestWriteResultSignatureEmptyFormatted(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

func NoReturn() {}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	decl := fileNode.Decls[0].(*ast.FuncDecl)
	var sig strings.Builder
	sig.WriteString("func NoReturn()")
	writeResultSignature(&sig, fset, decl.Type.Results)
	if strings.Contains(sig.String(), " ") && strings.Count(sig.String(), " ") > 1 {
		t.Fatalf("writeResultSignature(nil results) added extra space: %q", sig.String())
	}
}

func TestExtractFileImportDeclSkipped(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

import "fmt"

func Do() { fmt.Println() }
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	for _, n := range nodes {
		if n.Type == codeast.EntityVariable && n.Name == "fmt" {
			t.Fatal("import declaration should not be extracted as a variable")
		}
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1 (only the function)", len(nodes))
	}
}

func TestExtractVariableNoTypeNoDoc(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

var X = 42
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, false)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Comment != "" {
		t.Fatalf("comment = %q, want empty", nodes[0].Comment)
	}
	if !strings.HasPrefix(nodes[0].Signature, "var X") {
		t.Fatalf("signature = %q, want starts with 'var X'", nodes[0].Signature)
	}
}

func TestExtractVariableWithTypeAnnotation(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

// Count tracks items.
var Count int = 0
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if !strings.Contains(nodes[0].Signature, "int") {
		t.Fatalf("signature = %q, want contains 'int'", nodes[0].Signature)
	}
	if nodes[0].Comment == "" {
		t.Fatal("expected doc comment")
	}
}

func TestReceiverBaseTypeNameDefaultCase(t *testing.T) {
	fset := token.NewFileSet()
	expr := &ast.MapType{
		Key:   &ast.Ident{Name: "string"},
		Value: &ast.Ident{Name: "int"},
	}
	got := receiverBaseTypeName(fset, expr)
	if got == "" {
		t.Fatal("receiverBaseTypeName(map) returned empty")
	}
	if !strings.Contains(got, "map") {
		t.Fatalf("receiverBaseTypeName(map) = %q, want contains 'map'", got)
	}
}

func TestGetCodeWithCommentBadLineRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.go")
	writeFile(t, path, "package demo\n\nfunc F() {}\n")

	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	decl := fileNode.Decls[0].(*ast.FuncDecl)
	code := getCodeWithComment(fset, decl, nil)
	if code == "" {
		t.Fatal("getCodeWithComment returned empty for valid file")
	}
}

func TestGetCodeWithGenDeclGroupedWithDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grouped.go")
	content := `package demo

var (
	// A is the first.
	A = 1
	B = 2
)
`
	writeFile(t, path, content)

	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, path, nil, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	genDecl := fileNode.Decls[0].(*ast.GenDecl)
	spec := genDecl.Specs[0].(*ast.ValueSpec)
	code := getCodeWithGenDecl(fset, spec, genDecl, spec.Doc)
	if !strings.Contains(code, "var (") {
		t.Fatalf("getCodeWithGenDecl(grouped var) = %q, want contains 'var ('", code)
	}
	if !strings.Contains(code, "A = 1") {
		t.Fatalf("getCodeWithGenDecl(grouped var) = %q, want contains 'A = 1'", code)
	}
}

func TestExtractorExtractFunctionNoDocComment(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

func Bare() {}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Comment != "" {
		t.Fatalf("comment = %q, want empty", nodes[0].Comment)
	}
}

func TestExtractorExtractTypeDocOnSpec(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

type (
	// SpecDoc for Foo.
	Foo struct{}
)
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if !strings.Contains(nodes[0].Comment, "SpecDoc") {
		t.Fatalf("comment = %q, want contains 'SpecDoc'", nodes[0].Comment)
	}
}

func TestExtractorExtractVariableDocOnSpec(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

var (
	// SpecVarDoc for X.
	X = 1
)
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if !strings.Contains(nodes[0].Comment, "SpecVarDoc") {
		t.Fatalf("comment = %q, want contains 'SpecVarDoc'", nodes[0].Comment)
	}
}

func TestExtractorExtractFunctionNoReceiver(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

func Plain(a int, b string) (bool, error) { return false, nil }
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Type != codeast.EntityFunction {
		t.Fatalf("type = %s, want Function", nodes[0].Type)
	}
	if nodes[0].ID != "demo.Plain" {
		t.Fatalf("id = %s, want demo.Plain", nodes[0].ID)
	}
	if _, ok := nodes[0].Metadata[codeast.MetadataKeyReceiverType]; ok {
		t.Fatal("plain function should not have receiver_type metadata")
	}
}

func TestParseContentEmptyPackageID(t *testing.T) {
	parser := NewParser()
	result, err := parser.ParseContent("test.go", `package mypackage
`)
	if err != nil {
		t.Fatalf("ParseContent() error = %v", err)
	}
	if result.File.Package != "mypackage" {
		t.Fatalf("package = %s, want mypackage", result.File.Package)
	}
}

func TestParseDirectoryFullNoGoModReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Main() {}\n")

	parser := NewParser()
	_, err := parser.parseDirectoryFull(dir)
	if err == nil {
		t.Fatal("expected parseDirectoryFull to fail without go.mod")
	}
}

func TestParseDirectoryFullEmptyPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/empty\n\ngo 1.21\n")

	parser := NewParser()
	_, err := parser.parseDirectoryFull(dir)
	if err == nil {
		t.Fatal("expected parseDirectoryFull to return error for directory with no packages")
	}
}

func TestParseDirectoryFullWithEdgeAnalysis(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/full\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "svc.go"), `package full

type Runner interface{ Run() }
type Worker struct{}
func (w Worker) Run() {}
func helper() {}
func Do() { helper() }
`)

	parser := NewParser(WithEdgeAnalysis(true), WithConcurrency(1))
	result, err := parser.parseDirectoryFull(dir)
	if err != nil {
		t.Fatalf("parseDirectoryFull() error = %v", err)
	}
	if len(result.Nodes) < 4 {
		t.Fatalf("len(nodes) = %d, want >= 4", len(result.Nodes))
	}
	if len(result.Edges) == 0 {
		t.Fatal("expected edges with edge analysis enabled")
	}
}

func TestParseDirectoryFullNoEdgeAnalysis(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/noedge\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "svc.go"), `package noedge

type Svc struct{}
func (s Svc) Do() {}
`)

	parser := NewParser(WithEdgeAnalysis(false))
	result, err := parser.parseDirectoryFull(dir)
	if err != nil {
		t.Fatalf("parseDirectoryFull() error = %v", err)
	}
	if len(result.Edges) != 0 {
		t.Fatalf("len(edges) = %d, want 0 with edge analysis disabled", len(result.Edges))
	}
}

func TestAnalyzePackagesConcurrentMultiple(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/conc\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a", "a.go"), `package a

type A struct{}
func (a A) Do() {}
`)
	writeFile(t, filepath.Join(dir, "b", "b.go"), `package b

type B struct{}
func (b B) Do() {}
`)
	writeFile(t, filepath.Join(dir, "c", "c.go"), `package c

type C struct{}
func (c C) Do() {}
`)

	parser := NewParser(WithEdgeAnalysis(true), WithConcurrency(10))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Edges) == 0 {
		t.Fatal("expected edges with concurrent analyzePackages")
	}
}

func TestParseDirectoryModulesError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package root\n\nfunc Root() {}\n")
	sub := filepath.Join(dir, "sub")
	writeFile(t, filepath.Join(sub, "go.mod"), "module example.com/root/sub\n\ngo 1.21\n")
	writeFile(t, filepath.Join(sub, "s.go"), "package sub\n\nfunc S() {}\n")

	parser := NewParser(WithConcurrency(10))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if result.File == nil {
		t.Fatal("expected file info")
	}
}

func TestAnalyzerFunctionWithReturnTypesNonLocal(t *testing.T) {
	a := newDefaultAnalyzer()
	fset := token.NewFileSet()
	src := `package demo

import "fmt"

func Do() fmt.Stringer { return nil }
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	pkg := &parsedPackage{
		ID:     "demo",
		Name:   "demo",
		Syntax: []*ast.File{fileNode},
		Fset:   fset,
		Imports: map[string]*parsedImport{
			"fmt": {Name: "fmt", PkgPath: "fmt"},
		},
	}
	a.fset = fset
	var edges []*codeast.Edge
	for _, decl := range fileNode.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			a.analyzeFunction(pkg, fd, nil, &edges)
		}
	}
	hasReturn := false
	for _, e := range edges {
		if e.Type == codeast.RelationReturns {
			hasReturn = true
		}
	}
	if !hasReturn {
		t.Fatalf("expected RETURNS edge, got %+v", edges)
	}
}

func TestGetCodeWithCommentPrinterFallbackOnReadError(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, "/nonexistent/dir/fake.go", "package demo\n\nfunc F() {}\n", goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	decl := fileNode.Decls[0].(*ast.FuncDecl)
	code := getCodeWithComment(fset, decl, nil)
	if code == "" {
		t.Fatal("expected printer fallback output")
	}
	if !strings.Contains(code, "func F()") {
		t.Fatalf("code = %q, want contains 'func F()'", code)
	}
}

func TestGetCodeWithGenDeclPrinterFallbackOnReadError(t *testing.T) {
	fset := token.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, "/nonexistent/dir/fake.go", "package demo\n\ntype (\n\tT struct{}\n)\n", goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	gen := fileNode.Decls[0].(*ast.GenDecl)
	spec := gen.Specs[0]
	code := getCodeWithGenDecl(fset, spec, gen, nil)
	if code == "" {
		t.Fatal("expected printer fallback output")
	}
}

func TestFieldListToStringEmpty(t *testing.T) {
	fset := token.NewFileSet()
	got := fieldListToString(fset, &ast.FieldList{List: nil})
	if got != "" {
		t.Fatalf("fieldListToString(empty list) = %q, want empty", got)
	}
}

func TestExtractorExtractFunctionReceiverWithNoNames(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

type Svc struct{}

func (*Svc) Handle() {}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	var method *codeast.Node
	for _, n := range nodes {
		if n.Type == codeast.EntityMethod {
			method = n
		}
	}
	if method == nil {
		t.Fatal("expected method node")
	}
	if method.ID != "demo.Svc.Handle" {
		t.Fatalf("id = %s, want demo.Svc.Handle", method.ID)
	}
	if strings.Contains(method.Signature, "(*Svc) ") && !strings.Contains(method.Signature, "*Svc") {
		t.Fatalf("signature = %q, unexpected format", method.Signature)
	}
}

func TestParseDirectoryDirectASTWithFilesButNilFileInfo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/dast\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package dast\n\nimport \"fmt\"\n\nfunc A() { fmt.Println() }\n")

	parser := NewParser(WithExtractImports(true))
	result, err := parser.parseDirectoryDirectAST(dir)
	if err != nil {
		t.Fatalf("parseDirectoryDirectAST() error = %v", err)
	}
	if len(result.File.Imports) == 0 {
		t.Fatal("expected imports in result")
	}
}

func TestAnalyzerAnalyzeFunctionBodyNil(t *testing.T) {
	a := newDefaultAnalyzer()
	a.fset = token.NewFileSet()

	decl := &ast.FuncDecl{
		Name: &ast.Ident{Name: "ExtFn"},
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					{Type: &ast.Ident{Name: "Foo"}},
				},
			},
			Results: &ast.FieldList{
				List: []*ast.Field{
					{Type: &ast.Ident{Name: "Bar"}},
				},
			},
		},
		Body: nil,
	}

	pkg := &parsedPackage{ID: "demo", Name: "demo"}
	nodeSet := map[string]bool{"demo.Foo": true, "demo.Bar": true, "demo.ExtFn": true}
	var edges []*codeast.Edge
	a.analyzeFunction(pkg, decl, nodeSet, &edges)
	hasParam := false
	hasReturns := false
	for _, e := range edges {
		if e.Type == codeast.RelationParam {
			hasParam = true
		}
		if e.Type == codeast.RelationReturns {
			hasReturns = true
		}
		if e.Type == codeast.RelationCalls {
			t.Fatal("should not have CALLS edge for bodyless function")
		}
	}
	if !hasParam {
		t.Fatal("expected PARAM edge")
	}
	if !hasReturns {
		t.Fatal("expected RETURNS edge")
	}
}

func TestExtractorExtractBothFsetNil(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

func F() {}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: nil}
	nodes, err := e.Extract(&extractInput{pkg: pkg, fset: nil})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least one node with both fset nil")
	}
}

func TestAnalyzerExtractCallSelectorWithTypesInfoMethodOnLocalType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/meth\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "svc.go"), `package meth

type A struct{}

func (a A) DoA() {}

func Do() {
	var a A
	a.DoA()
}
`)

	parser := NewParser(WithEdgeAnalysis(true), WithConcurrency(1))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if !hasCodeEdge(result.Edges, "example.com/meth.Do", "example.com/meth.A.DoA", codeast.RelationCalls) {
		t.Fatalf("expected method call edge via typed receiver, got %+v", result.Edges)
	}
}

func TestParseDirectoryWithMultipleModulesAndConcurrencyGtModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package root\n\nfunc Root() {}\n")
	for i := 0; i < 5; i++ {
		sub := filepath.Join(dir, fmt.Sprintf("sub%d", i))
		writeFile(t, filepath.Join(sub, "go.mod"), fmt.Sprintf("module example.com/root/sub%d\n\ngo 1.21\n", i))
		writeFile(t, filepath.Join(sub, fmt.Sprintf("s%d.go", i)), fmt.Sprintf("package sub%d\n\nfunc S%d() {}\n", i, i))
	}

	parser := NewParser(WithConcurrency(100))
	result, err := parser.ParseDirectory(dir)
	if err != nil {
		t.Fatalf("ParseDirectory() error = %v", err)
	}
	if len(result.Nodes) < 6 {
		t.Fatalf("len(nodes) = %d, want >= 6", len(result.Nodes))
	}
}

func TestParseDirectoryModulesWithConcurrencyUsesFullBudget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/root\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package root\n\nfunc Root() {}\n")
	sub := filepath.Join(dir, "sub")
	writeFile(t, filepath.Join(sub, "go.mod"), "module example.com/root/sub\n\ngo 1.21\n")
	writeFile(t, filepath.Join(sub, "s.go"), "package sub\n\nfunc S() {}\n")

	parser := NewParser(WithConcurrency(4))
	results, err := parser.parseDirectoryModulesWithConcurrency([]string{dir, sub}, 2, 2)
	if err != nil {
		t.Fatalf("parseDirectoryModulesWithConcurrency() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
}

func TestExtractorExtractGenDeclNonTypeNotProcessed(t *testing.T) {
	fset := token.NewFileSet()
	src := `package demo

import (
	"fmt"
	"os"
)

func Do() {
	fmt.Println()
	os.Exit(0)
}
`
	fileNode, err := goparser.ParseFile(fset, "demo.go", src, goparser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	e := newDefaultExtractor(1, true)
	pkg := &parsedPackage{ID: "demo", Name: "demo", Syntax: []*ast.File{fileNode}, Fset: fset}
	nodes := e.extractFile(pkg, fset, fileNode)
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1 (only Do function)", len(nodes))
	}
}

func TestParsedPackageFromPackagesFiltersNilImports(t *testing.T) {
	fset := token.NewFileSet()
	pkg := &packages.Package{
		ID:      "test",
		Name:    "test",
		PkgPath: "test",
		Fset:    fset,
		Imports: map[string]*packages.Package{
			"fmt": {Name: "fmt", PkgPath: "fmt"},
			"nil": nil,
		},
	}
	pp := parsedPackageFromPackages(pkg, nil)
	if _, ok := pp.Imports["nil"]; ok {
		t.Fatal("nil imports should be filtered out")
	}
	if _, ok := pp.Imports["fmt"]; !ok {
		t.Fatal("valid imports should be kept")
	}
}
