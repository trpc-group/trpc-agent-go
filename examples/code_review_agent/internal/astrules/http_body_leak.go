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

// httpMethods returns the set of net/http functions/methods that return
// a *http.Response whose Body must be closed.
var httpMethods = map[string]bool{
	"Get":      true,
	"Post":     true,
	"Head":     true,
	"PostForm": true,
	"Do":       true,
}

// scanHTTPBodyLeak (AST-001) flags `resp, err := http.Get(...)` /
// `client.Do(...)` assignments whose enclosing function does not defer
// `resp.Body.Close()`. Regex rules cannot detect this because the defer
// may appear many lines later and the response variable name is
// arbitrary.
func scanHTTPBodyLeak(fset *token.FileSet, file *ast.File, df diffparse.DiffFile) []rules.Finding {
	var findings []astFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		// Collect response variable names and whether a deferred
		// Body.Close() appears in the same function.
		var respVars []string
		hasBodyClose := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				for i, expr := range v.Rhs {
					if !isHTTPCallReturningResponse(expr) {
						continue
					}
					if i < len(v.Lhs) {
						if name, ok := identName(v.Lhs[i]); ok {
							respVars = append(respVars, name)
						}
					}
				}
			case *ast.CallExpr:
				if isDeferBodyClose(v) {
					hasBodyClose = true
				}
			}
			return true
		})
		if hasBodyClose {
			continue
		}
		for _, rv := range respVars {
			findings = append(findings, astFinding{
				node:           fn,
				ruleID:         RuleHTTPBodyLeak,
				severity:       "high",
				category:       "reliability",
				title:          "HTTP response body not closed",
				evidence:       "resp := http.Get/Post/Do(...) without defer resp.Body.Close()",
				recommendation: "Add 'defer resp.Body.Close()' immediately after the call to release the connection",
				confidence:     0.85,
				_rv:            rv,
			})
		}
	}
	return toFindings(df, fset, findings)
}

// isHTTPCallReturningResponse reports whether expr is a call to
// http.Get/Post/Head/PostForm or (*http.Client).Do — the standard
// library functions that return a *http.Response.
func isHTTPCallReturningResponse(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return httpMethods[sel.Sel.Name]
}

// isDeferBodyClose reports whether call is `<var>.Body.Close()`. It is
// used to detect the compensating defer that suppresses the finding.
func isDeferBodyClose(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Close" {
		return false
	}
	// sel.X must be <var>.Body
	bodySel, ok := sel.X.(*ast.SelectorExpr)
	return ok && bodySel.Sel.Name == "Body"
}

// identName returns the identifier name if expr is an *ast.Ident.
func identName(expr ast.Expr) (string, bool) {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name, true
	}
	return "", false
}
