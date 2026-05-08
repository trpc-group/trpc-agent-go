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
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

type defaultExtractor struct {
	concurrency    int
	extractImports bool
}

func newDefaultExtractor(concurrency int, extractImports bool) *defaultExtractor {
	if concurrency <= 0 {
		concurrency = 100
	}
	return &defaultExtractor{
		concurrency:    concurrency,
		extractImports: extractImports,
	}
}

func (e *defaultExtractor) Extract(input *extractInput) ([]*codeast.Node, error) {
	if input == nil || input.pkg == nil {
		return nil, nil
	}
	pkg := input.pkg
	fset := input.fset
	if fset == nil {
		fset = pkg.Fset
	}
	if fset == nil {
		fset = token.NewFileSet()
	}

	var nodes []*codeast.Node
	tasks := make(chan *ast.File, len(pkg.Syntax))
	results := make(chan []*codeast.Node, len(pkg.Syntax))

	var wg sync.WaitGroup
	workerCount := min(e.concurrency, len(pkg.Syntax))
	if workerCount == 0 {
		workerCount = 1
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range tasks {
				results <- e.extractFile(pkg, fset, file)
			}
		}()
	}

	for _, file := range pkg.Syntax {
		tasks <- file
	}
	close(tasks)

	go func() {
		wg.Wait()
		close(results)
	}()

	for fileNodes := range results {
		nodes = append(nodes, fileNodes...)
	}
	return nodes, nil
}

func (e *defaultExtractor) extractFile(pkg *parsedPackage, fset *token.FileSet, file *ast.File) []*codeast.Node {
	var nodes []*codeast.Node
	chunkIndex := 0

	var imports []string
	if e.extractImports {
		imports = extractImportsFromASTFile(file)
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			node := e.extractFunction(pkg, fset, d, chunkIndex)
			if node != nil {
				node.Imports = append([]string(nil), imports...)
				nodes = append(nodes, node)
				chunkIndex++
			}
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					doc := d.Doc
					if typeSpec.Doc != nil {
						doc = typeSpec.Doc
					}
					extracted := e.extractType(pkg, fset, typeSpec, d, doc, chunkIndex)
					for _, n := range extracted {
						n.Imports = append([]string(nil), imports...)
					}
					nodes = append(nodes, extracted...)
					chunkIndex += len(extracted)
				}
			case token.VAR, token.CONST:
				for _, spec := range d.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					doc := d.Doc
					if valueSpec.Doc != nil {
						doc = valueSpec.Doc
					}
					extracted := e.extractVariable(pkg, fset, valueSpec, d, doc, chunkIndex)
					for _, n := range extracted {
						n.Imports = append([]string(nil), imports...)
					}
					nodes = append(nodes, extracted...)
					chunkIndex += len(extracted)
				}
			}
		}
	}

	return nodes
}

func (e *defaultExtractor) extractFunction(pkg *parsedPackage, fset *token.FileSet, decl *ast.FuncDecl, chunkIndex int) *codeast.Node {
	name := decl.Name.Name
	entityType := codeast.EntityFunction
	id := fmt.Sprintf("%s.%s", pkg.ID, name)
	fullName := id

	receiverType := ""
	receiverTypeBase := ""
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		entityType = codeast.EntityMethod
		receiverType = typeToString(fset, decl.Recv.List[0].Type)
		receiverTypeBase = receiverBaseTypeName(fset, decl.Recv.List[0].Type)
		if receiverTypeBase == "" {
			receiverTypeBase = receiverType
		}
		name = fmt.Sprintf("%s.%s", receiverTypeBase, decl.Name.Name)
		id = fmt.Sprintf("%s.%s", pkg.ID, name)
		fullName = id
	}

	comment := ""
	if decl.Doc != nil {
		comment = strings.TrimSpace(decl.Doc.Text())
	}

	code := getCodeWithComment(fset, decl, decl.Doc)
	startPos := fset.Position(decl.Pos())
	endPos := fset.Position(decl.End())
	lineStart := startPos.Line
	if decl.Doc != nil {
		docPos := fset.Position(decl.Doc.Pos())
		if docPos.IsValid() {
			lineStart = docPos.Line
		}
	}

	signature := e.buildFunctionSignature(fset, decl, receiverType)
	node := newNode(entityType, decl.Name.Name, id, fullName, code, signature, comment, startPos.Filename, lineStart, endPos.Line, chunkIndex)
	node.Package = pkg.ID
	if receiverType != "" {
		node.Metadata[codeast.MetadataKeyReceiverType] = receiverType
	}
	return node
}

func (e *defaultExtractor) buildFunctionSignature(fset *token.FileSet, decl *ast.FuncDecl, receiverType string) string {
	var sig strings.Builder
	writeFunctionPrefix(&sig, decl, receiverType)
	writeFunctionTypeParams(&sig, fset, decl)
	writeFunctionParams(&sig, fset, decl)
	writeResultSignature(&sig, fset, decl.Type.Results)
	return sig.String()
}

func writeFunctionPrefix(sig *strings.Builder, decl *ast.FuncDecl, receiverType string) {
	sig.WriteString("func ")
	writeReceiverSignature(sig, decl, receiverType)
	sig.WriteString(decl.Name.Name)
}

func writeFunctionTypeParams(sig *strings.Builder, fset *token.FileSet, decl *ast.FuncDecl) {
	if decl.Type.TypeParams == nil || len(decl.Type.TypeParams.List) == 0 {
		return
	}
	sig.WriteString("[")
	sig.WriteString(fieldListToString(fset, decl.Type.TypeParams))
	sig.WriteString("]")
}

func writeFunctionParams(sig *strings.Builder, fset *token.FileSet, decl *ast.FuncDecl) {
	sig.WriteString("(")
	sig.WriteString(formatFieldList(fset, decl.Type.Params))
	sig.WriteString(")")
}

func writeReceiverSignature(sig *strings.Builder, decl *ast.FuncDecl, receiverType string) {
	if receiverType == "" {
		return
	}
	sig.WriteString("(")
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		recv := decl.Recv.List[0]
		if len(recv.Names) > 0 {
			sig.WriteString(recv.Names[0].Name)
			sig.WriteString(" ")
		}
		sig.WriteString(receiverType)
	}
	sig.WriteString(") ")
}

func writeResultSignature(sig *strings.Builder, fset *token.FileSet, results *ast.FieldList) {
	if results == nil || len(results.List) == 0 {
		return
	}
	formatted := formatFieldList(fset, results)
	if formatted == "" {
		return
	}
	sig.WriteString(" ")
	if requiresResultParens(results) {
		sig.WriteString("(")
		sig.WriteString(formatted)
		sig.WriteString(")")
		return
	}
	sig.WriteString(formatted)
}

func requiresResultParens(results *ast.FieldList) bool {
	if results == nil || len(results.List) == 0 {
		return false
	}
	return len(results.List) > 1 || len(results.List[0].Names) > 0
}

func formatFieldList(fset *token.FileSet, fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		parts = append(parts, formatField(fset, field))
	}
	return strings.Join(parts, ", ")
}

func formatField(fset *token.FileSet, field *ast.Field) string {
	var part strings.Builder
	for i, name := range field.Names {
		if i > 0 {
			part.WriteString(", ")
		}
		part.WriteString(name.Name)
	}
	if len(field.Names) > 0 {
		part.WriteString(" ")
	}
	part.WriteString(typeToString(fset, field.Type))
	return part.String()
}

func (e *defaultExtractor) extractType(pkg *parsedPackage, fset *token.FileSet, spec *ast.TypeSpec, genDecl *ast.GenDecl, doc *ast.CommentGroup, chunkIndex int) []*codeast.Node {
	name := spec.Name.Name
	id := fmt.Sprintf("%s.%s", pkg.ID, name)
	fullName := id
	startPos := fset.Position(spec.Pos())
	endPos := fset.Position(spec.End())

	var entityType codeast.EntityType
	var signature string
	goTypeKind := "definition"
	if spec.Assign.IsValid() {
		goTypeKind = "alias"
	}

	typeParams := ""
	if spec.TypeParams != nil && len(spec.TypeParams.List) > 0 {
		typeParams = "[" + fieldListToString(fset, spec.TypeParams) + "]"
	}

	switch t := spec.Type.(type) {
	case *ast.StructType:
		entityType = codeast.EntityStruct
		signature = fmt.Sprintf("type %s%s struct", name, typeParams)
	case *ast.InterfaceType:
		entityType = codeast.EntityInterface
		signature = fmt.Sprintf("type %s%s interface", name, typeParams)
	default:
		entityType = codeast.EntityAlias
		signature = fmt.Sprintf("type %s%s %s", name, typeParams, typeToString(fset, t))
	}

	comment := ""
	if doc != nil {
		comment = strings.TrimSpace(doc.Text())
	}

	code := getCodeWithGenDecl(fset, spec, genDecl, doc)
	lineStart := startPos.Line
	if doc != nil {
		docPos := fset.Position(doc.Pos())
		if docPos.IsValid() {
			lineStart = docPos.Line
		}
	}

	node := newNode(entityType, name, id, fullName, code, signature, comment, startPos.Filename, lineStart, endPos.Line, chunkIndex)
	node.Package = pkg.ID
	node.Metadata["go_type_kind"] = goTypeKind
	return []*codeast.Node{node}
}

func (e *defaultExtractor) extractVariable(pkg *parsedPackage, fset *token.FileSet, spec *ast.ValueSpec, genDecl *ast.GenDecl, doc *ast.CommentGroup, chunkIndex int) []*codeast.Node {
	var nodes []*codeast.Node
	startPos := fset.Position(spec.Pos())
	endPos := fset.Position(spec.End())

	comment := ""
	if doc != nil {
		comment = strings.TrimSpace(doc.Text())
	}

	code := getCodeWithGenDecl(fset, spec, genDecl, doc)
	lineStart := startPos.Line
	if doc != nil {
		docPos := fset.Position(doc.Pos())
		if docPos.IsValid() {
			lineStart = docPos.Line
		}
	}

	typeStr := ""
	if spec.Type != nil {
		typeStr = typeToString(fset, spec.Type)
	}

	keyword := "var"
	if genDecl != nil && genDecl.Tok == token.CONST {
		keyword = "const"
	}

	for i, name := range spec.Names {
		id := fmt.Sprintf("%s.%s", pkg.ID, name.Name)
		fullName := id
		signature := fmt.Sprintf("%s %s", keyword, name.Name)
		if typeStr != "" {
			signature = fmt.Sprintf("%s %s %s", keyword, name.Name, typeStr)
		}

		node := newNode(codeast.EntityVariable, name.Name, id, fullName, code, signature, comment, startPos.Filename, lineStart, endPos.Line, chunkIndex+i)
		node.Package = pkg.ID
		node.Metadata["go_value_kind"] = keyword
		nodes = append(nodes, node)
	}
	return nodes
}

func newNode(entityType codeast.EntityType, shortName, id, fullName, code, signature, comment, filePath string,
	lineStart, lineEnd, chunkIndex int,
) *codeast.Node {
	scope := codeast.ScopeCode
	if codeast.IsExamplePath(filePath, "") {
		scope = codeast.ScopeExample
	}
	return &codeast.Node{
		ID:         id,
		Type:       entityType,
		Name:       shortName,
		FullName:   fullName,
		Scope:      scope,
		Language:   codeast.LanguageGo,
		Signature:  signature,
		Comment:    comment,
		Code:       code,
		FilePath:   filePath,
		LineStart:  lineStart,
		LineEnd:    lineEnd,
		ChunkIndex: chunkIndex,
		Metadata:   make(map[string]any),
	}
}

func extractImportsFromASTFile(file *ast.File) []string {
	imports := make([]string, 0, len(file.Imports))
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, "\"")
		if path != "" {
			imports = append(imports, path)
		}
	}
	return imports
}

func getCodeWithComment(fset *token.FileSet, node ast.Node, doc *ast.CommentGroup) string {
	if fset == nil || node == nil {
		return ""
	}

	startPos := fset.Position(node.Pos())
	endPos := fset.Position(node.End())
	if !startPos.IsValid() || startPos.Filename == "" {
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, node); err != nil {
			return ""
		}
		return buf.String()
	}

	actualStartLine := startPos.Line
	if doc != nil {
		docPos := fset.Position(doc.Pos())
		if docPos.IsValid() {
			actualStartLine = docPos.Line
		}
	}

	content, err := os.ReadFile(startPos.Filename)
	if err != nil {
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, node); err != nil {
			return ""
		}
		return buf.String()
	}

	lines := bytes.Split(content, []byte("\n"))
	if actualStartLine <= 0 || endPos.Line > len(lines) {
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, node)
		return buf.String()
	}

	startLine := actualStartLine - 1
	endLine := endPos.Line
	if endLine > len(lines) {
		endLine = len(lines)
	}
	return string(bytes.Join(lines[startLine:endLine], []byte("\n")))
}

func getCodeWithGenDecl(fset *token.FileSet, spec ast.Node, genDecl *ast.GenDecl, doc *ast.CommentGroup) string {
	if fset == nil || spec == nil {
		return ""
	}
	if genDecl != nil && !genDecl.Lparen.IsValid() {
		return getCodeWithComment(fset, genDecl, doc)
	}

	specPos := fset.Position(spec.Pos())
	specEnd := fset.Position(spec.End())
	if !specPos.IsValid() || specPos.Filename == "" {
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, spec); err != nil {
			return ""
		}
		if genDecl != nil {
			return genDecl.Tok.String() + " " + buf.String()
		}
		return buf.String()
	}

	content, err := os.ReadFile(specPos.Filename)
	if err != nil {
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, spec); err != nil {
			return ""
		}
		if genDecl != nil {
			return genDecl.Tok.String() + " " + buf.String()
		}
		return buf.String()
	}

	lines := bytes.Split(content, []byte("\n"))
	startLine := specPos.Line
	if doc != nil {
		docPos := fset.Position(doc.Pos())
		if docPos.IsValid() && docPos.Line < startLine {
			startLine = docPos.Line
		}
	}
	endLine := specEnd.Line
	if startLine <= 0 || endLine > len(lines) {
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, spec)
		if genDecl != nil {
			return genDecl.Tok.String() + " " + buf.String()
		}
		return buf.String()
	}

	specCode := string(bytes.Join(lines[startLine-1:endLine], []byte("\n")))
	if genDecl != nil && genDecl.Lparen.IsValid() {
		return fmt.Sprintf("%s (\n\t%s\n)", genDecl.Tok.String(), strings.TrimSpace(specCode))
	}
	return specCode
}

func typeToString(fset *token.FileSet, expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var buf bytes.Buffer
	printer.Fprint(&buf, fset, expr)
	return buf.String()
}

func fieldListToString(fset *token.FileSet, fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}

	var parts []string
	for _, field := range fields.List {
		var names []string
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
		typeStr := typeToString(fset, field.Type)
		if len(names) > 0 {
			parts = append(parts, strings.Join(names, ", ")+" "+typeStr)
		} else {
			parts = append(parts, typeStr)
		}
	}
	return strings.Join(parts, ", ")
}

func receiverBaseTypeName(fset *token.FileSet, expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverBaseTypeName(fset, t.X)
	case *ast.IndexExpr:
		return receiverBaseTypeName(fset, t.X)
	case *ast.IndexListExpr:
		return receiverBaseTypeName(fset, t.X)
	case *ast.ParenExpr:
		return receiverBaseTypeName(fset, t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return typeToString(fset, expr)
	}
}
