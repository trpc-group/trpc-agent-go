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
		// Collect response variable names and which are closed via
		// defer <var>.Body.Close(). Only *ast.DeferStmt counts —
		// a plain resp.Body.Close() call (wrong path / too early)
		// must not suppress the finding.
		var respVars []string
		closed := map[string]bool{}
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
			case *ast.DeferStmt:
				if name, ok := deferBodyCloseVar(v.Call); ok {
					closed[name] = true
				}
			}
			return true
		})
		for _, rv := range respVars {
			if closed[rv] {
				continue
			}
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
// library functions that return a *http.Response. Package-level
// Get/Post/Head/PostForm must be on the `http` identifier to avoid
// flagging unrelated methods such as db.Get or cache.Post.
func isHTTPCallReturningResponse(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if !httpMethods[sel.Sel.Name] {
		return false
	}
	switch x := sel.X.(type) {
	case *ast.Ident:
		if x.Name == "http" {
			return true
		}
		// client.Do is the common pattern; other methods on plain idents
		// (db.Get, cache.Post) are too ambiguous and are ignored.
		return sel.Sel.Name == "Do"
	case *ast.SelectorExpr:
		// http.DefaultClient.Get / cli.Transport... rare; accept Get/Post/Do.
		return true
	default:
		return false
	}
}

// deferBodyCloseVar reports whether call is `<var>.Body.Close()` and
// returns the response variable name. Used only under *ast.DeferStmt.
func deferBodyCloseVar(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Close" {
		return "", false
	}
	// sel.X must be <var>.Body
	bodySel, ok := sel.X.(*ast.SelectorExpr)
	if !ok || bodySel.Sel.Name != "Body" {
		return "", false
	}
	return identName(bodySel.X)
}

// identName returns the identifier name if expr is an *ast.Ident.
func identName(expr ast.Expr) (string, bool) {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name, true
	}
	return "", false
}
