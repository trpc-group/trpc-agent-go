//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestGoroutineContextEvidenceIsScoped(t *testing.T) {
	tests := []struct {
		name         string
		source       string
		wantEvidence []string
	}{
		{
			name: "enclosing context is ignored",
			source: `package review

import "context"

func refreshCache() {}

func handle(ctx context.Context) {
	go refreshCache()
}
`,
			wantEvidence: []string{"go refreshCache()"},
		},
		{
			name: "unrelated text and function do not suppress",
			source: `package review

import "context"

func refreshCache() {}

func start() {
	_ = "ctx context.Context"
	// ctx belongs to unrelated text.
	go refreshCache()
}

func unrelated(ctx context.Context) {}
`,
			wantEvidence: []string{"go refreshCache()"},
		},
		{
			name: "named calls receive context",
			source: `package review

import (
	"context"
	"net/http"
)

func worker(context.Context) {}

func handle(ctx context.Context, request *http.Request) {
	go worker(ctx)
	go worker(request.Context())
}
`,
		},
		{
			name: "non-cancellable named argument does not override cancellable evidence",
			source: `package review

import "context"

func worker(context.Context, context.Context) {}

func handle(ctx context.Context) {
	go worker(ctx, context.Background())
}
`,
		},
		{
			name: "closures observe or pass context",
			source: `package review

import "context"

func worker(context.Context) {}

func handle(ctx context.Context) {
	go func(ctx context.Context) { <-ctx.Done() }(ctx)
	go func() { _ = ctx.Err() }()
	go func() { _, _ = ctx.Deadline() }()
	go func() { _ = context.Cause(ctx) }()
	go func() { worker(ctx) }()
}
`,
		},
		{
			name: "unrelated non-cancellable closure call does not override observation",
			source: `package review

import "context"

func flush(context.Context) {}

func handle(ctx context.Context) {
	go func() {
		<-ctx.Done()
		flush(context.Background())
	}()
}
`,
		},
		{
			name: "unused closure parameter is unsafe",
			source: `package review

import "context"

func work() {}

func handle(ctx context.Context) {
	go func(ctx context.Context) { work() }(ctx)
}
`,
			wantEvidence: []string{"go func(ctx context.Context) { work() }(ctx)"},
		},
		{
			name: "shadowed non-context parameter is unsafe",
			source: `package review

import "context"

func workerString(string) {}

func handle(ctx context.Context) {
	go func(ctx string) { workerString(ctx) }("shadow")
}
`,
			wantEvidence: []string{`go func(ctx string) { workerString(ctx) }("shadow")`},
		},
		{
			name: "non-cancellable context roots are unsafe",
			source: `package review

import "context"

func worker(context.Context) {}

func handle(ctx context.Context) {
	go worker(context.Background())
	go worker(context.TODO())
	go worker(context.WithoutCancel(ctx))
}
`,
			wantEvidence: []string{
				"go worker(context.Background())",
				"go worker(context.TODO())",
				"go worker(context.WithoutCancel(ctx))",
			},
		},
		{
			name: "derived contexts and aliases preserve provenance",
			source: `package review

import (
	stdctx "context"
	"net/http"
)

func worker(stdctx.Context) {}

func handle(parent stdctx.Context, request *http.Request) {
	child, cancel := stdctx.WithCancel(parent)
	_ = cancel
	alias := child
	go worker(alias)
	requestCtx := request.Context()
	go worker(requestCtx)
	background := stdctx.Background()
	go worker(background)
}
`,
			wantEvidence: []string{"go worker(background)"},
		},
		{
			name: "context-shaped declarations require assignability",
			source: `package review

import (
	"context"
	"net/http"
)

func worker(context.Context) {}


func handle(request *http.Request) {
	type Ctx context.Context
	type MyAlias = context.Context
	type Context struct{}
	type FakeCtx interface{ Foo() int }
	var (
		ctx Ctx
		alias MyAlias
		custom Context
		fake FakeCtx
	)
	go worker(ctx)
	go worker(alias)
	go worker(custom)
	go worker(fake)
	go worker(request.Context())
}
`,
			wantEvidence: []string{
				"go worker(custom)",
				"go worker(fake)",
			},
		},
		{
			name: "custom context method result is typed",
			source: `package review

import "context"

func worker(context.Context) {}

func handle(
	obj interface{ Context() string },
	valid interface{ Context() context.Context },
) {
	go worker(obj.Context())
	go worker(valid.Context())
}
`,
			wantEvidence: []string{"go worker(obj.Context())"},
		},
		{
			name: "multi-return context method results are typed",
			source: `package review

import "context"

func worker(any) {}

func handle(
	valid interface{ Context() (context.Context, error) },
	invalid interface{ Context() (string, error) },
) {
	ctx, _ := valid.Context()
	go worker(ctx)

	var declared, declaredErr = valid.Context()
	_ = declaredErr
	go worker(declared)

	bad, _ := invalid.Context()
	go worker(bad)

	var badDeclared, badErr = invalid.Context()
	_ = badErr
	go worker(badDeclared)
}
`,
			wantEvidence: []string{
				"go worker(bad)",
				"go worker(badDeclared)",
			},
		},
		{
			name: "tuple results preserve every position and closure expansion",
			source: `package review

import "context"

func worker(any) {}

type reverseProvider interface {
	Context() (error, context.Context)
}

func getCtxAndErr(parent context.Context) (context.Context, error) {
	child, cancel := context.WithCancel(parent)
	defer cancel()
	return child, nil
}

func getErrAndCtx(parent context.Context) (error, context.Context) {
	child, cancel := context.WithCancel(parent)
	defer cancel()
	return nil, child
}

func handle(parent context.Context, provider reverseProvider) {
	ctx, err := getCtxAndErr(parent)
	_ = err
	go worker(ctx)
	helperErr, helperCtx := getErrAndCtx(parent)
	go worker(helperErr)
	go worker(helperCtx)

	bad, good := provider.Context()
	go worker(bad)
	go worker(good)
	var declaredBad, declaredGood = provider.Context()
	go worker(declaredBad)
	go worker(declaredGood)

	_, second := provider.Context()
	go worker(second)

	go func(ctx context.Context, err error) { <-ctx.Done() }(getCtxAndErr(parent))
	go func(ctx context.Context, err error) { <-ctx.Done() }(getErrAndCtx(parent))
}
`,
			wantEvidence: []string{
				"go worker(helperErr)",
				"go worker(bad)",
				"go worker(declaredBad)",
				"go func(ctx context.Context, err error) { <-ctx.Done() }(getErrAndCtx(parent))",
			},
		},
		{
			name: "mixed statements preserve unsafe lines",
			source: `package review

import "context"

func worker(context.Context) {}
func work() {}

func handle(ctx context.Context) {
	go worker(ctx)
	go work()
	go worker(ctx); go work()
}
`,
			wantEvidence: []string{
				"go work()",
				"go worker(ctx); go work()",
			},
		},
		{
			name:   "legal token separators are classified",
			source: "package review\n\nimport \"context\"\n\nfunc worker(context.Context) {}\nfunc refreshCache() {}\n\nfunc\thandle(ctx context.Context) {\n\tgo\tworker(ctx)\n\tgo/*comment*/worker(ctx)\n\tgo\trefreshCache()\n\tgo/*comment*/refreshCache()\n\twork(); go refreshCache()\n}\n",
			wantEvidence: []string{
				"go\trefreshCache()",
				"go/*comment*/refreshCache()",
				"work(); go refreshCache()",
			},
		},
		{
			name: "comments and strings do not create go candidates",
			source: `package review

func start() {
	_ = "go refreshCache()"
	// go refreshCache()
	/*
	go refreshCache()
	*/
}
`,
		},
		{
			name: "multiline go statement uses keyword line",
			source: `package review

func refreshCache() {}

func start() {
	go
		refreshCache()
}
`,
			wantEvidence: []string{"go"},
		},
	}

	for _, mode := range []string{"diff-only", "repository"} {
		for _, test := range tests {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				parsed := parseUnifiedDiff([]byte(goroutineNewFileDiff(test.source)))
				if len(parsed.Warnings) != 0 {
					t.Fatalf("parse warnings = %+v", parsed.Warnings)
				}
				repoRoot := ""
				if mode == "repository" {
					repoRoot = t.TempDir()
					mustWriteFile(t, filepath.Join(repoRoot, "review.go"), test.source)
				}
				got := goroutineFindingEvidence(runRules(parsed, repoRoot))
				want := append([]string(nil), test.wantEvidence...)
				sort.Strings(want)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("goroutine evidence = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestDiffOnlyNegativeControlsUsesBoundClosureContext(t *testing.T) {
	fixture, err := readFixture("negative_controls")
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseUnifiedDiff([]byte(fixture.Diff))
	if len(parsed.Warnings) != 0 {
		t.Fatalf("parse warnings = %+v", parsed.Warnings)
	}
	if got := goroutineFindingEvidence(runRules(parsed, "")); len(got) != 0 {
		t.Fatalf("negative_controls goroutine evidence = %q, want none", got)
	}
}

func TestDiffOnlyGoroutineFallback(t *testing.T) {
	t.Run("single-line candidates are batched", func(t *testing.T) {
		diff := strings.Join([]string{
			"diff --git a/review.go b/review.go",
			"index 1111111..2222222 100644",
			"--- a/review.go",
			"+++ b/review.go",
			"@@ -10 +10,3 @@",
			" \twork()",
			"+\tgo worker(ctx)",
			"+\tgo refreshCache()",
		}, "\n")
		parsed := parseUnifiedDiff([]byte(diff))
		if len(parsed.Warnings) != 0 {
			t.Fatalf("parse warnings = %+v", parsed.Warnings)
		}
		want := []string{"go refreshCache()"}
		if got := goroutineFindingEvidence(runRules(parsed, "")); !reflect.DeepEqual(got, want) {
			t.Fatalf("goroutine evidence = %q, want %q", got, want)
		}
	})

	t.Run("unresolved Context method retains conservative fallback", func(t *testing.T) {
		diff := strings.Join([]string{
			"diff --git a/review.go b/review.go",
			"index 1111111..2222222 100644",
			"--- a/review.go",
			"+++ b/review.go",
			"@@ -10 +10,2 @@",
			" \twork()",
			"+\tgo worker(request.Context())",
		}, "\n")
		parsed := parseUnifiedDiff([]byte(diff))
		if len(parsed.Warnings) != 0 {
			t.Fatalf("parse warnings = %+v", parsed.Warnings)
		}
		if got := goroutineFindingEvidence(runRules(parsed, "")); len(got) != 0 {
			t.Fatalf("goroutine evidence = %q, want none", got)
		}
	})

	t.Run("token-separated orphan candidates are classified", func(t *testing.T) {
		diff := strings.Join([]string{
			"diff --git a/review.go b/review.go",
			"index 1111111..2222222 100644",
			"--- a/review.go",
			"+++ b/review.go",
			"@@ -10 +10,4 @@",
			" \twork()",
			"+\tgo\tworker(ctx)",
			"+\tgo/*comment*/refreshCache()",
			"+\twork(); go refreshCache()",
		}, "\n")
		parsed := parseUnifiedDiff([]byte(diff))
		if len(parsed.Warnings) != 0 {
			t.Fatalf("parse warnings = %+v", parsed.Warnings)
		}
		want := []string{"go/*comment*/refreshCache()", "work(); go refreshCache()"}
		if got := goroutineFindingEvidence(runRules(parsed, "")); !reflect.DeepEqual(got, want) {
			t.Fatalf("goroutine evidence = %q, want %q", got, want)
		}
	})

	t.Run("multi-line orphan fails closed", func(t *testing.T) {
		diff := strings.Join([]string{
			"diff --git a/review.go b/review.go",
			"index 1111111..2222222 100644",
			"--- a/review.go",
			"+++ b/review.go",
			"@@ -10 +10,4 @@",
			" \twork()",
			"+\tgo func() {",
			"+\t\t<-ctx.Done()",
			"+\t}()",
		}, "\n")
		parsed := parseUnifiedDiff([]byte(diff))
		if len(parsed.Warnings) != 0 {
			t.Fatalf("parse warnings = %+v", parsed.Warnings)
		}
		want := []string{"go func() {"}
		if got := goroutineFindingEvidence(runRules(parsed, "")); !reflect.DeepEqual(got, want) {
			t.Fatalf("goroutine evidence = %q, want %q", got, want)
		}
	})
}

func TestGoroutineAnalysisOperationsGrowLinearly(t *testing.T) {
	for _, mode := range []string{"diff-only", "repository"} {
		t.Run(mode, func(t *testing.T) {
			measurements := make(map[int]goroutineAnalysisStats)
			for _, count := range []int{32, 128, 512, 1024} {
				source := highCandidateGoroutineSource(count, 0)
				parsed := parseUnifiedDiff([]byte(goroutineNewFileDiff(source)))
				if len(parsed.Warnings) != 0 {
					t.Fatalf("count %d parse warnings = %+v", count, parsed.Warnings)
				}
				repoRoot := ""
				if mode == "repository" {
					repoRoot = t.TempDir()
					mustWriteFile(t, filepath.Join(repoRoot, "review.go"), source)
				}
				candidates := parsed.candidateLines()
				leaks, stats := analyzeGoroutineCandidates(parsed.Files, repoRoot, candidates)
				if stats.CandidatesClassified != count || stats.ParsedSourceUnits == 0 ||
					stats.ASTNodesVisited == 0 {
					t.Fatalf("count %d stats = %+v", count, stats)
				}
				leakCount := 0
				for _, leak := range leaks {
					if leak {
						leakCount++
					}
				}
				if leakCount != count {
					t.Fatalf("count %d leak count = %d, want %d", count, leakCount, count)
				}
				measurements[count] = stats
			}
			for _, pair := range [][2]int{{32, 128}, {128, 512}} {
				assertGoroutineOperationGrowth(t, measurements, pair[0], pair[1], 5)
			}
			assertGoroutineOperationGrowth(t, measurements, 512, 1024, 3)
		})
	}
}

func assertGoroutineOperationGrowth(
	t *testing.T,
	measurements map[int]goroutineAnalysisStats,
	from int,
	to int,
	maximumMultiplier int,
) {
	t.Helper()
	before := measurements[from]
	after := measurements[to]
	checks := []struct {
		name   string
		before int
		after  int
	}{
		{name: "source lines", before: before.SourceLinesVisited, after: after.SourceLinesVisited},
		{name: "AST nodes", before: before.ASTNodesVisited, after: after.ASTNodesVisited},
		{name: "candidates", before: before.CandidatesClassified, after: after.CandidatesClassified},
	}
	for _, check := range checks {
		if check.before == 0 {
			continue
		}
		if check.after > check.before*maximumMultiplier {
			t.Fatalf(
				"%s grew from %d at %d candidates to %d at %d candidates; maximum multiplier %d",
				check.name,
				check.before,
				from,
				check.after,
				to,
				maximumMultiplier,
			)
		}
	}
}

func BenchmarkGoroutineAnalysisHighCandidateCount(b *testing.B) {
	for _, mode := range []string{"diff-only", "repository"} {
		for _, count := range []int{32, 128, 512, 1024} {
			b.Run(fmt.Sprintf("%s/%d", mode, count), func(b *testing.B) {
				benchmarkGoroutineAnalysis(b, mode, highCandidateGoroutineSource(count, 0))
			})
		}
	}
	b.Run("diff-only/near-max-diff", func(b *testing.B) {
		benchmarkGoroutineAnalysis(b, "diff-only", highCandidateGoroutineSource(4096, 1024))
	})
}

func benchmarkGoroutineAnalysis(b *testing.B, mode string, source string) {
	parsed := parseUnifiedDiff([]byte(goroutineNewFileDiff(source)))
	candidates := parsed.candidateLines()
	repoRoot := ""
	if mode == "repository" {
		repoRoot = b.TempDir()
		if err := os.WriteFile(filepath.Join(repoRoot, "review.go"), []byte(source), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		goroutineBenchmarkLeaks, goroutineBenchmarkStats =
			analyzeGoroutineCandidates(parsed.Files, repoRoot, candidates)
	}
}

var (
	goroutineBenchmarkLeaks []bool
	goroutineBenchmarkStats goroutineAnalysisStats
)

func goroutineFindingEvidence(matches []ruleMatch) []string {
	var evidence []string
	for _, match := range matches {
		if match.RuleID == ruleGoroutineContextLeak {
			evidence = append(evidence, match.Evidence)
		}
	}
	sort.Strings(evidence)
	return evidence
}

func TestContextResultsValueClassifiesKnownTuples(t *testing.T) {
	contextPackage, err := importer.Default().Import("context")
	if err != nil {
		t.Fatal(err)
	}
	contextObject := contextPackage.Scope().Lookup("Context")
	if contextObject == nil {
		t.Fatal("context.Context is unavailable")
	}
	contextType := contextObject.Type()
	contextVar := types.NewVar(token.NoPos, nil, "ctx", contextType)
	stringVar := types.NewVar(token.NoPos, nil, "value", types.Typ[types.String])
	intVar := types.NewVar(token.NoPos, nil, "err", types.Typ[types.Int])
	unknownVar := types.NewVar(token.NoPos, nil, "unknown", nil)
	tests := []struct {
		name    string
		results *types.Tuple
		want    goroutineContextValue
		known   bool
	}{
		{
			name:    "context plus second result",
			results: types.NewTuple(contextVar, intVar),
			want:    goroutineContextCancellable,
			known:   true,
		},
		{
			name:    "non-context results",
			results: types.NewTuple(stringVar, intVar),
			want:    goroutineContextNonCancellable,
			known:   true,
		},
		{
			name:    "empty tuple",
			results: types.NewTuple(),
			want:    goroutineContextNonCancellable,
			known:   true,
		},
		{
			name:    "unknown element type",
			results: types.NewTuple(unknownVar),
			want:    goroutineContextUnknown,
			known:   false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, known := contextResultsValue(test.results, contextType)
			if got != test.want || known != test.known {
				t.Fatalf(
					"contextResultsValue() = (%v, %v), want (%v, %v)",
					got,
					known,
					test.want,
					test.known,
				)
			}
		})
	}
	t.Run("known signature without results", func(t *testing.T) {
		got, known := contextSignatureResultValue(
			types.NewSignature(nil, nil, nil, false),
			contextType,
		)
		if got != goroutineContextNonCancellable || !known {
			t.Fatalf("contextSignatureResultValue() = (%v, %v), want (%v, true)", got, known, goroutineContextNonCancellable)
		}
	})
}

func TestContextResultValueAtPreservesTuplePositions(t *testing.T) {
	contextPackage, err := importer.Default().Import("context")
	if err != nil {
		t.Fatal(err)
	}
	contextObject := contextPackage.Scope().Lookup("Context")
	if contextObject == nil {
		t.Fatal("context.Context is unavailable")
	}
	contextType := contextObject.Type()
	contextVar := types.NewVar(token.NoPos, nil, "ctx", contextType)
	errorVar := types.NewVar(token.NoPos, nil, "err", types.Universe.Lookup("error").Type())
	results := types.NewTuple(errorVar, contextVar)
	if got, known := contextResultValueAt(results, 0, contextType); got != goroutineContextNonCancellable || !known {
		t.Fatalf("first tuple result = (%v, %v), want non-cancellable, true", got, known)
	}
	if got, known := contextResultValueAt(results, 1, contextType); got != goroutineContextCancellable || !known {
		t.Fatalf("second tuple result = (%v, %v), want cancellable, true", got, known)
	}
	if got, known := contextResultValueAt(results, 2, contextType); got != goroutineContextUnknown || known {
		t.Fatalf("out-of-range tuple result = (%v, %v), want unknown, false", got, known)
	}
}

func TestContextCallResultValuesUsesIdentifierSignatureFallback(t *testing.T) {
	const source = `package review

import "context"

func getCtxAndErr() (context.Context, error) { return nil, nil }

func use() {
	getCtxAndErr()
}
`
	fset := token.NewFileSet()
	parsedFile, err := parser.ParseFile(fset, "review.go", source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	info, contextType := goroutineTypeInfo(fset, parsedFile)
	var call *ast.CallExpr
	ast.Inspect(parsedFile, func(node ast.Node) bool {
		candidate, ok := node.(*ast.CallExpr)
		if ok {
			if identifier, ok := unparenthesizedExpression(candidate.Fun).(*ast.Ident); ok &&
				identifier.Name == "getCtxAndErr" {
				call = candidate
				return false
			}
		}
		return true
	})
	if call == nil {
		t.Fatal("getCtxAndErr call not found")
	}
	delete(info.Types, call)
	values, known, multi := contextCallResultValues(call, info, contextType)
	if !known || !multi || len(values) != 2 ||
		values[0] != goroutineContextCancellable || values[1] != goroutineContextNonCancellable {
		t.Fatalf("identifier fallback values = (%v, %v, %v), want context/error tuple", values, known, multi)
	}
}

func TestUnknownTupleAssignmentDoesNotReuseCancellableBinding(t *testing.T) {
	const source = `package review

import "context"

func worker(context.Context) {}

func handle(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	_ = cancel
	ctx, _ = getCtxAndErr(parent)
	go worker(ctx)
}
`
	parsed := parseUnifiedDiff([]byte(goroutineNewFileDiff(source)))
	if got := goroutineFindingEvidence(runRules(parsed, "")); len(got) != 1 {
		t.Fatalf("unknown tuple assignment evidence = %q, want conservative finding", got)
	}
}

func TestContextCallTypeValueUsesMultiResultSignatureFallback(t *testing.T) {
	const source = `package review

import "context"

type validProvider interface {
	Context() (context.Context, error)
}

type invalidProvider interface {
	Context() (string, error)
}

func use(valid validProvider, invalid invalidProvider) {
	valid.Context()
	invalid.Context()
}
`
	fset := token.NewFileSet()
	parsedFile, err := parser.ParseFile(fset, "review.go", source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	info, contextType := goroutineTypeInfo(fset, parsedFile)
	type callInfo struct {
		call     *ast.CallExpr
		selector *ast.SelectorExpr
		want     goroutineContextValue
	}
	var calls []callInfo
	ast.Inspect(parsedFile, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := unparenthesizedExpression(call.Fun).(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Context" {
			return true
		}
		want := goroutineContextNonCancellable
		if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "valid" {
			want = goroutineContextCancellable
		}
		calls = append(calls, callInfo{call: call, selector: selector, want: want})
		return true
	})
	if len(calls) != 2 {
		t.Fatalf("found %d Context calls, want 2", len(calls))
	}
	for _, test := range calls {
		delete(info.Types, test.call)
		got, known := contextCallTypeValue(test.call, info, contextType)
		if got != test.want || !known {
			t.Fatalf("selection fallback = (%v, %v), want (%v, true)", got, known, test.want)
		}
	}
	valid := calls[0]
	delete(info.Selections, valid.selector)
	got, known := contextCallTypeValue(valid.call, info, contextType)
	if got != goroutineContextCancellable || !known {
		t.Fatalf("uses fallback = (%v, %v), want (%v, true)", got, known, goroutineContextCancellable)
	}
}

func goroutineNewFileDiff(source string) string {
	lines := strings.Split(strings.TrimSuffix(source, "\n"), "\n")
	var diff strings.Builder
	diff.WriteString("diff --git a/review.go b/review.go\n")
	diff.WriteString("index 1111111..2222222 100644\n")
	diff.WriteString("--- a/review.go\n")
	diff.WriteString("+++ b/review.go\n")
	fmt.Fprintf(&diff, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		diff.WriteByte('+')
		diff.WriteString(line)
		diff.WriteByte('\n')
	}
	return diff.String()
}

func highCandidateGoroutineSource(count int, padding int) string {
	var source strings.Builder
	source.WriteString("package review\n\nfunc review() {\n")
	payload := strings.Repeat("x", padding)
	for index := 0; index < count; index++ {
		if padding == 0 {
			fmt.Fprintf(&source, "\tgo worker%d()\n", index)
			continue
		}
		fmt.Fprintf(&source, "\tgo worker(%q)\n", payload)
	}
	source.WriteString("}\n")
	return source.String()
}
