//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package astrules

import (
	"go/ast"
	"go/token"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// scanContextMisuse (AST-003) flags functions that accept a ctx
// parameter but call context.TODO() or context.Background() inside the
// body instead of passing the received ctx. This drops cancellation
// signals and breaks deadline propagation.
func scanContextMisuse(fset *token.FileSet, file *ast.File, df diffparse.DiffFile) []rules.Finding {
	var findings []astFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !hasContextParam(fn.Type.Params) {
			continue
		}
		// Find context.TODO()/context.Background() call sites in the
		// function body. Each is a misuse because the function already
		// has a ctx parameter it should be propagating.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); !ok || ident.Name != "context" {
				return true
			}
			if sel.Sel.Name != "TODO" && sel.Sel.Name != "Background" {
				return true
			}
			findings = append(findings, astFinding{
				node:           call,
				ruleID:         RuleContextMisuse,
				severity:       "medium",
				category:       "correctness",
				title:          "context.TODO()/Background() used while function accepts a ctx",
				evidence:       "context." + sel.Sel.Name + "() called inside a function with a context.Context parameter",
				recommendation: "Pass the received ctx to the child call so cancellation and deadlines propagate",
				confidence:     0.80,
			})
			return true
		})
	}
	return toFindings(df, fset, findings)
}

// hasContextParam reports whether the parameter list contains a
// parameter of type context.Context. The type is recognized by the
// selector expression "context.Context" — the standard import.
func hasContextParam(params *ast.FieldList) bool {
	if params == nil {
		return false
	}
	for _, field := range params.List {
		sel, ok := field.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "context" && sel.Sel.Name == "Context" {
			return true
		}
	}
	return false
}
