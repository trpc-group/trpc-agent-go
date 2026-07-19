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

// scanSQLRowsLeak (AST-002) flags `rows, err := db.Query(...)` whose
// enclosing function does not defer `rows.Close()`. Forgetting to close
// *sql.Rows leaks database connections.
func scanSQLRowsLeak(fset *token.FileSet, file *ast.File, df diffparse.DiffFile) []rules.Finding {
	var findings []astFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var rowsVars []string
		hasRowsClose := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				for i, expr := range v.Rhs {
					if !isQueryCall(expr) {
						continue
					}
					if i < len(v.Lhs) {
						if name, ok := identName(v.Lhs[i]); ok {
							rowsVars = append(rowsVars, name)
						}
					}
				}
			case *ast.DeferStmt:
				if isCloseCall(v.Call) {
					hasRowsClose = true
				}
			}
			return true
		})
		if hasRowsClose {
			continue
		}
		for range rowsVars {
			findings = append(findings, astFinding{
				node:           fn,
				ruleID:         RuleSQLRowsLeak,
				severity:       "high",
				category:       "reliability",
				title:          "SQL rows not closed",
				evidence:       "rows := db.Query(...) without defer rows.Close()",
				recommendation: "Add 'defer rows.Close()' immediately after Query to release the database connection",
				confidence:     0.85,
			})
		}
	}
	return toFindings(df, fset, findings)
}

// isQueryCall reports whether expr is a call to <x>.Query(...) — the
// database/sql method that returns (*sql.Rows, error).
func isQueryCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "Query"
}

// isCloseCall reports whether call is `<x>.Close()`.
func isCloseCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Close"
}
