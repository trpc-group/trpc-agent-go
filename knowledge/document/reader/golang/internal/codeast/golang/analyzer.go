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
	"go/types"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
)

type defaultAnalyzer struct {
	fset *token.FileSet
}

func newDefaultAnalyzer() *defaultAnalyzer {
	return &defaultAnalyzer{fset: token.NewFileSet()}
}

// Analyze derives graph edges for a package. Full directory parsing provides
// type information, which enables cross-package calls and interface edges.
func (a *defaultAnalyzer) Analyze(input *analyzeInput, nodeSet map[string]bool) ([]*codeast.Edge, error) {
	if input == nil || input.pkg == nil {
		return nil, nil
	}
	pkg := input.pkg
	a.fset = pkg.Fset
	if a.fset == nil {
		a.fset = token.NewFileSet()
	}

	var edges []*codeast.Edge
	for _, file := range pkg.Syntax {
		if file == nil {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				a.analyzeFunction(pkg, d, nodeSet, &edges)
			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						a.analyzeType(pkg, typeSpec, nodeSet, &edges)
					}
				}
			}
		}
	}

	edges = append(edges, a.analyzeImplements(pkg, input.interfaces, nodeSet)...)
	return edges, nil
}

func (a *defaultAnalyzer) analyzeFunction(
	pkg *parsedPackage,
	decl *ast.FuncDecl,
	nodeSet map[string]bool,
	edges *[]*codeast.Edge,
) {
	if decl == nil || decl.Name == nil {
		return
	}
	funcName := decl.Name.Name
	funcID := fmt.Sprintf("%s.%s", pkg.ID, funcName)

	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		receiverType := receiverBaseTypeName(a.fset, decl.Recv.List[0].Type)
		if receiverType == "" {
			receiverType = a.typeToString(decl.Recv.List[0].Type)
		}
		structName := strings.TrimPrefix(receiverType, "*")
		structID := fmt.Sprintf("%s.%s", pkg.ID, structName)

		fullMethodName := fmt.Sprintf("%s.%s", structName, funcName)
		funcID = fmt.Sprintf("%s.%s", pkg.ID, fullMethodName)

		*edges = append(*edges, &codeast.Edge{
			FromID: structID,
			ToID:   funcID,
			Type:   codeast.RelationMethod,
		})
	}

	if decl.Type.Params != nil {
		for _, field := range decl.Type.Params.List {
			a.extractTypeDeps(pkg, field.Type, funcID, codeast.RelationParam, nodeSet, edges)
		}
	}
	if decl.Type.Results != nil {
		for _, field := range decl.Type.Results.List {
			a.extractTypeDeps(pkg, field.Type, funcID, codeast.RelationReturns, nodeSet, edges)
		}
	}

	if decl.Body == nil {
		return
	}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			a.extractCall(pkg, call, funcID, nodeSet, edges)
		}
		return true
	})
}

func (a *defaultAnalyzer) analyzeType(
	pkg *parsedPackage,
	spec *ast.TypeSpec,
	nodeSet map[string]bool,
	edges *[]*codeast.Edge,
) {
	if spec == nil || spec.Name == nil {
		return
	}
	typeName := spec.Name.Name
	typeID := fmt.Sprintf("%s.%s", pkg.ID, typeName)

	if structType, ok := spec.Type.(*ast.StructType); ok {
		for _, field := range structType.Fields.List {
			a.extractTypeDeps(pkg, field.Type, typeID, codeast.RelationField, nodeSet, edges)
		}
	}

	if _, isStruct := spec.Type.(*ast.StructType); !isStruct {
		if _, isInterface := spec.Type.(*ast.InterfaceType); !isInterface {
			a.extractTypeDeps(pkg, spec.Type, typeID, codeast.RelationAliasOf, nodeSet, edges)
		}
	}
}

func (a *defaultAnalyzer) extractTypeDeps(
	pkg *parsedPackage,
	expr ast.Expr,
	fromID string,
	relation codeast.RelationType,
	nodeSet map[string]bool,
	edges *[]*codeast.Edge,
) {
	for _, toID := range a.extractTypeIDs(pkg, expr) {
		if toID != "" && (nodeSet == nil || nodeSet[toID]) {
			*edges = append(*edges, &codeast.Edge{
				FromID: fromID,
				ToID:   toID,
				Type:   relation,
			})
		}
	}
}

func (a *defaultAnalyzer) extractTypeIDs(pkg *parsedPackage, expr ast.Expr) []string {
	if pkg.TypesInfo != nil {
		if typeAndValue, ok := pkg.TypesInfo.Types[expr]; ok {
			ids := a.namedTypeIDs(typeAndValue.Type)
			if len(ids) > 0 {
				return ids
			}
		}
	}

	typeNames := a.extractTypeNames(expr)
	ids := make([]string, 0, len(typeNames))
	for _, typeName := range typeNames {
		var toID string
		if strings.Contains(typeName, ".") {
			parts := strings.Split(typeName, ".")
			if len(parts) == 2 {
				pkgPath := a.resolvePkgPath(pkg, parts[0])
				if pkgPath != "" {
					toID = fmt.Sprintf("%s.%s", pkgPath, parts[1])
				}
			}
		} else if !a.isBasicType(typeName) {
			toID = fmt.Sprintf("%s.%s", pkg.ID, typeName)
		}
		if toID != "" {
			ids = append(ids, toID)
		}
	}
	return ids
}

func (a *defaultAnalyzer) namedTypeIDs(t types.Type) []string {
	seen := make(map[string]struct{})
	var ids []string
	var visit func(types.Type)
	visit = func(t types.Type) {
		switch typed := t.(type) {
		case *types.Named:
			obj := typed.Obj()
			if obj == nil || obj.Pkg() == nil {
				return
			}
			id := fmt.Sprintf("%s.%s", obj.Pkg().Path(), obj.Name())
			if _, ok := seen[id]; ok {
				return
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		case *types.Pointer:
			visit(typed.Elem())
		case *types.Slice:
			visit(typed.Elem())
		case *types.Array:
			visit(typed.Elem())
		case *types.Map:
			visit(typed.Key())
			visit(typed.Elem())
		case *types.Chan:
			visit(typed.Elem())
		case *types.Signature:
			a.visitTupleTypes(typed.Params(), visit)
			a.visitTupleTypes(typed.Results(), visit)
		}
	}
	visit(t)
	return ids
}

func (a *defaultAnalyzer) visitTupleTypes(tuple *types.Tuple, visit func(types.Type)) {
	if tuple == nil {
		return
	}
	for i := 0; i < tuple.Len(); i++ {
		visit(tuple.At(i).Type())
	}
}

func (a *defaultAnalyzer) extractCall(
	pkg *parsedPackage,
	call *ast.CallExpr,
	fromID string,
	nodeSet map[string]bool,
	edges *[]*codeast.Edge,
) {
	var calleeID string

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if !a.isBuiltin(fun.Name) {
			calleeID = fmt.Sprintf("%s.%s", pkg.ID, fun.Name)
		}
	case *ast.SelectorExpr:
		if xIdent, ok := fun.X.(*ast.Ident); ok {
			if pkg.TypesInfo == nil {
				calleeID = fmt.Sprintf("%s.%s", xIdent.Name, fun.Sel.Name)
			} else if pkgName, ok := pkg.TypesInfo.Uses[xIdent].(*types.PkgName); ok {
				calleeID = fmt.Sprintf("%s.%s", pkgName.Imported().Path(), fun.Sel.Name)
			} else if typeAndValue, ok := pkg.TypesInfo.Types[fun.X]; ok {
				if typeID := receiverTypeID(pkg, typeAndValue.Type); typeID != "" {
					calleeID = fmt.Sprintf("%s.%s", typeID, fun.Sel.Name)
				} else {
					typeName := a.typeToStringFromType(typeAndValue.Type)
					typeName = strings.TrimPrefix(typeName, "*")
					if strings.Contains(typeName, ".") {
						calleeID = fmt.Sprintf("%s.%s", typeName, fun.Sel.Name)
					} else {
						calleeID = fmt.Sprintf("%s.%s.%s", pkg.ID, typeName, fun.Sel.Name)
					}
				}
			}
		}
	}

	if calleeID != "" && (nodeSet == nil || nodeSet[calleeID]) {
		*edges = append(*edges, &codeast.Edge{
			FromID: fromID,
			ToID:   calleeID,
			Type:   codeast.RelationCalls,
		})
	}
}

func (a *defaultAnalyzer) analyzeImplements(
	pkg *parsedPackage,
	interfaces []*interfaceType,
	nodeSet map[string]bool,
) []*codeast.Edge {
	if pkg.Types == nil {
		return nil
	}

	var edges []*codeast.Edge
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		typeName, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		if _, ok := typeName.Type().Underlying().(*types.Struct); !ok {
			continue
		}

		fromID := fmt.Sprintf("%s.%s", pkg.ID, name)
		for _, ifaceType := range interfaces {
			if ifaceType == nil || ifaceType.iface == nil || ifaceType.id == "" {
				continue
			}
			if !types.Implements(typeName.Type(), ifaceType.iface) && !types.Implements(types.NewPointer(typeName.Type()), ifaceType.iface) {
				continue
			}

			if nodeSet == nil || (nodeSet[fromID] && (nodeSet[ifaceType.id] || ifaceType.external)) {
				edges = append(edges, &codeast.Edge{
					FromID: fromID,
					ToID:   ifaceType.id,
					Type:   codeast.RelationImplements,
				})
			}
		}
	}
	return edges
}

func receiverTypeID(pkg *parsedPackage, t types.Type) string {
	switch typed := t.(type) {
	case *types.Pointer:
		return receiverTypeID(pkg, typed.Elem())
	case *types.Named:
		obj := typed.Obj()
		if obj == nil {
			return ""
		}
		if obj.Pkg() != nil {
			return fmt.Sprintf("%s.%s", obj.Pkg().Path(), obj.Name())
		}
		if pkg != nil && pkg.ID != "" {
			return fmt.Sprintf("%s.%s", pkg.ID, obj.Name())
		}
	}
	return ""
}

func (a *defaultAnalyzer) typeToString(expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, a.fset, expr)
	return buf.String()
}

func (a *defaultAnalyzer) extractTypeNames(expr ast.Expr) []string {
	switch t := expr.(type) {
	case *ast.Ident:
		return []string{t.Name}
	case *ast.StarExpr:
		return a.extractTypeNames(t.X)
	case *ast.SelectorExpr:
		return []string{a.typeToString(t)}
	case *ast.ArrayType:
		return a.extractTypeNames(t.Elt)
	}
	return nil
}

func (a *defaultAnalyzer) resolvePkgPath(pkg *parsedPackage, alias string) string {
	for _, imp := range pkg.Imports {
		if imp == nil {
			continue
		}
		if imp.Name == alias {
			return imp.PkgPath
		}
		if imp.Name == "" {
			parts := strings.Split(imp.PkgPath, "/")
			if parts[len(parts)-1] == alias {
				return imp.PkgPath
			}
		}
	}
	return ""
}

func (a *defaultAnalyzer) isBasicType(name string) bool {
	basics := map[string]bool{
		"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
		"bool": true, "byte": true, "rune": true, "error": true,
		"float32": true, "float64": true, "complex64": true, "complex128": true,
		"interface": true, "any": true,
	}
	return basics[name]
}

func (a *defaultAnalyzer) isBuiltin(name string) bool {
	builtins := map[string]bool{
		"len": true, "cap": true, "make": true, "new": true, "append": true, "panic": true,
		"copy": true, "close": true, "delete": true, "recover": true, "print": true, "println": true,
	}
	return builtins[name]
}

func (a *defaultAnalyzer) typeToStringFromType(t types.Type) string {
	return t.String()
}
