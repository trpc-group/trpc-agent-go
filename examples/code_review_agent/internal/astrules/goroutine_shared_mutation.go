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

// scanGoroutineSharedMutation (AST-004) flags `go func() { ... }()`
// literals whose body assigns to an identifier declared outside the
// goroutine (a captured variable). Such writes race with the caller
// unless synchronized via a channel or mutex.
//
// This is a heuristic: it reports any assignment to a free variable
// inside a goroutine literal. False positives are possible when the
// captured variable is read-only by design; the confidence is set
// accordingly (0.70).
func scanGoroutineSharedMutation(fset *token.FileSet, file *ast.File, df diffparse.DiffFile) []rules.Finding {
	var findings []astFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			goStmt, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			lit, ok := goStmt.Call.Fun.(*ast.FuncLit)
			if !ok {
				return true
			}
			// Collect identifiers declared inside the goroutine literal
			// (params + local vars). Assignments to these are safe; only
			// assignments to OUTER (captured) identifiers are flagged.
			localIDs := collectLocalIdentifiers(lit)
			var capturedWrite ast.Node
			ast.Inspect(lit.Body, func(inner ast.Node) bool {
				if capturedWrite != nil {
					return false
				}
				// Both regular assignments (= / :=) and inc/dec (++)
				// mutate the target variable. count++ is *ast.IncDecStmt,
				// not *ast.AssignStmt, so it must be handled separately.
				var lhs []ast.Expr
				switch stmt := inner.(type) {
				case *ast.AssignStmt:
					lhs = stmt.Lhs
				case *ast.IncDecStmt:
					lhs = []ast.Expr{stmt.X}
				default:
					return true
				}
				for _, expr := range lhs {
					id, ok := expr.(*ast.Ident)
					if !ok {
						continue
					}
					// Skip assignments to locals declared inside the
					// goroutine. Only captured outer variables are risky.
					if localIDs[id.Name] {
						continue
					}
					// "_" is not a real capture.
					if id.Name == "_" {
						continue
					}
					capturedWrite = inner
					break
				}
				return true
			})
			if capturedWrite != nil {
				findings = append(findings, astFinding{
					node:           goStmt,
					ruleID:         RuleGoroutineSharedMutation,
					severity:       "high",
					category:       "correctness",
					title:          "Goroutine writes to captured variable (data race)",
					evidence:       "go func() { ... <captured> = ... }() writes to a variable declared outside the goroutine",
					recommendation: "Synchronize the write with a channel or mutex, or pass the value as a parameter to the goroutine",
					confidence:     0.70,
				})
			}
			return true
		})
	}
	return toFindings(df, fset, findings)
}

// collectLocalIdentifiers returns the set of identifier names declared
// inside a goroutine literal (parameters + local := assignments + var
// declarations). These are safe to assign; only assignments to OUTER
// captured variables are flagged by scanGoroutineSharedMutation.
func collectLocalIdentifiers(lit *ast.FuncLit) map[string]bool {
	locals := make(map[string]bool)
	if lit.Type.Params != nil {
		for _, field := range lit.Type.Params.List {
			for _, name := range field.Names {
				locals[name.Name] = true
			}
		}
	}
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			// Only short declarations (:=) introduce new locals. Plain
			// assignments (=) write to existing (possibly captured) vars.
			if v.Tok != token.DEFINE {
				return true
			}
			for _, lhs := range v.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					locals[id.Name] = true
				}
			}
		case *ast.DeclStmt:
			// `var n int` and `var n = 1` introduce new locals too.
			gd, ok := v.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if name.Name != "_" {
						locals[name.Name] = true
					}
				}
			}
		}
		return true
	})
	return locals
}
