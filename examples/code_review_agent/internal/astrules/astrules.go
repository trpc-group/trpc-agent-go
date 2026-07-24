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

// Package astrules provides AST-based code review rules that catch
// structural patterns regex cannot (e.g. an HTTP response body that is
// never closed, or a context.Context parameter that is shadowed by
// context.TODO() inside the function body).
//
// Borrowed from competitor PR #2243's internal/review/ast_rules.go.
//
// AST rules only run on files the diff treats as newly added
// (OldPath == "/dev/null"), because that is the only case where the
// added lines form a complete, parseable Go source file. For modified
// files the diff only carries fragments, and parsing those would
// produce noisy syntax errors — the regex rules in package rules already
// cover the line-oriented cases for modified files.
//
// When a file cannot be parsed (incomplete imports, syntax errors,
// build-tag-guarded code) the AST rules skip it silently. AST analysis
// is best-effort: it never blocks the pipeline, only augments it.
package astrules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
)

// Rule IDs for AST-based findings.
const (
	RuleHTTPBodyLeak            = "AST-001"
	RuleSQLRowsLeak             = "AST-002"
	RuleContextMisuse           = "AST-003"
	RuleGoroutineSharedMutation = "AST-004"
)

// Engine runs the AST rules against parsed diff files. It is separate
// from rules.Engine so the regex and AST layers can evolve independently.
type Engine struct{}

// NewEngine returns an Engine whose Run applies every AST rule.
func NewEngine() *Engine { return &Engine{} }

// Run applies every AST rule to the provided files. Files that are not
// new (OldPath != "/dev/null") or that cannot be parsed are skipped
// silently. The returned findings are deduplicated by the review layer
// later, so duplicate reports across rules are harmless.
func (e *Engine) Run(files []diffparse.DiffFile) []rules.Finding {
	var out []rules.Finding
	for _, f := range files {
		if f.OldPath != "/dev/null" {
			continue // AST rules only run on new files.
		}
		src := addedSource(f)
		if src == "" {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f.NewPath, src, parser.AllErrors)
		if err != nil || file == nil {
			continue // best-effort: skip unparseable fragments
		}
		out = append(out, scanHTTPBodyLeak(fset, file, f)...)
		out = append(out, scanSQLRowsLeak(fset, file, f)...)
		out = append(out, scanContextMisuse(fset, file, f)...)
		out = append(out, scanGoroutineSharedMutation(fset, file, f)...)
	}
	return out
}

// addedSource reconstructs a Go source string from the added lines of a
// new file. Because OldPath == "/dev/null", every added line is part of
// the new file's content. The result may still be unparseable if the
// fixture is intentionally partial; callers handle that by skipping.
func addedSource(f diffparse.DiffFile) string {
	var b strings.Builder
	for _, l := range f.AddedLinesNumbered() {
		b.WriteString(l.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// astFinding is the intermediate representation produced by each scan
// function. It carries the AST node (for line-number resolution) plus
// the finding fields. Each scan function returns a slice of astFinding
// which toFindings converts to rules.Finding.
type astFinding struct {
	node           ast.Node
	ruleID         string
	severity       string
	category       string
	title          string
	evidence       string
	recommendation string
	confidence     float64
	// _rv is the response/rows variable name, currently unused in the
	// finding text but retained for future rule-specific messages.
	_rv string
}

// toFindings converts intermediate astFinding records into rules.Finding,
// resolving line numbers from the token.FileSet.
func toFindings(df diffparse.DiffFile, fset *token.FileSet, in []astFinding) []rules.Finding {
	if len(in) == 0 {
		return nil
	}
	out := make([]rules.Finding, 0, len(in))
	for _, a := range in {
		out = append(out, rules.Finding{
			RuleID:         a.ruleID,
			Severity:       a.severity,
			Category:       a.category,
			File:           df.NewPath,
			Line:           fset.Position(a.node.Pos()).Line,
			Title:          a.title,
			Evidence:       a.evidence,
			Recommendation: a.recommendation,
			Confidence:     a.confidence,
			Source:         "ast:" + a.ruleID,
		})
	}
	return out
}
