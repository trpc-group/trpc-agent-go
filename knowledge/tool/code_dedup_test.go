//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func newDedupTestCtx(invocationID string) context.Context {
	inv := &agent.Invocation{InvocationID: invocationID}
	return agent.NewInvocationContext(context.Background(), inv)
}

func newDedupDoc(fullName string) *DocumentResult {
	return &DocumentResult{
		Text: "body:" + fullName,
		Metadata: map[string]any{
			"trpc_ast_full_name": fullName,
		},
	}
}

// TestCodeDedupFilter_AllDuplicatesReturnContract verifies that when every
// top result was already returned in previous calls within the same
// invocation, the filter keeps the response non-nil, sets Documents to an
// empty slice, and rewrites Message so the LLM is explicitly told to vary
// its next call. The response MUST NOT be turned into an error and
// Documents MUST NOT be nil.
func TestCodeDedupFilter_AllDuplicatesReturnContract(t *testing.T) {
	store := newCodeDedupStore()
	ctx := newDedupTestCtx("inv-all-dup")

	// First call: all three documents are new and kept as-is.
	first := &KnowledgeSearchResponse{
		Documents: []*DocumentResult{
			newDedupDoc("pkg.A"),
			newDedupDoc("pkg.B"),
			newDedupDoc("pkg.C"),
		},
	}
	firstOut := store.filter(ctx, first)
	require.NotNil(t, firstOut)
	require.Len(t, firstOut.Documents, 3)

	// Second call: every doc was already returned. Contract requires an
	// empty-but-non-nil slice and a Message that signals the all-duplicates
	// case to the model.
	second := &KnowledgeSearchResponse{
		Documents: []*DocumentResult{
			newDedupDoc("pkg.A"),
			newDedupDoc("pkg.B"),
			newDedupDoc("pkg.C"),
		},
	}
	secondOut := store.filter(ctx, second)
	require.NotNil(t, secondOut)
	require.NotNil(t, secondOut.Documents, "Documents must be non-nil even when everything is filtered")
	require.Len(t, secondOut.Documents, 0)
	require.NotEmpty(t, secondOut.Message)
	require.True(t,
		strings.Contains(secondOut.Message, "already returned") ||
			strings.Contains(secondOut.Message, "Try a different"),
		"message should instruct the LLM to vary the next call, got %q", secondOut.Message,
	)
}

// TestCodeDedupFilter_PartialDuplicatesKeepNewOnes verifies the mixed case:
// some documents are new, some were returned previously; only the new ones
// are kept and Message reports the omitted count.
func TestCodeDedupFilter_PartialDuplicatesKeepNewOnes(t *testing.T) {
	store := newCodeDedupStore()
	ctx := newDedupTestCtx("inv-partial")

	first := &KnowledgeSearchResponse{
		Documents: []*DocumentResult{newDedupDoc("pkg.A"), newDedupDoc("pkg.B")},
	}
	store.filter(ctx, first)

	second := &KnowledgeSearchResponse{
		Documents: []*DocumentResult{
			newDedupDoc("pkg.A"), // duplicate
			newDedupDoc("pkg.C"), // new
		},
	}
	out := store.filter(ctx, second)
	require.NotNil(t, out)
	require.Len(t, out.Documents, 1)
	require.Equal(t, "pkg.C", out.Documents[0].Metadata["trpc_ast_full_name"])
	require.Contains(t, out.Message, "duplicate")
}

// TestCodeDedupFilter_NoInvocationIsNoOp verifies that a context without an
// invocation attached falls back to a pass-through rather than leaking state
// across unrelated callers.
func TestCodeDedupFilter_NoInvocationIsNoOp(t *testing.T) {
	store := newCodeDedupStore()
	resp := &KnowledgeSearchResponse{
		Documents: []*DocumentResult{newDedupDoc("pkg.A"), newDedupDoc("pkg.A")},
	}
	out := store.filter(context.Background(), resp)
	require.NotNil(t, out)
	// Both docs stay because dedup is disabled without invocation scope.
	require.Len(t, out.Documents, 2)
}

// TestCodeDedupFilter_StateIsScopedToInvocation ensures the dedup cache lives
// on the invocation runtime state instead of the tool instance, so separate
// invocations do not share suppression state even when their InvocationID
// strings happen to match.
func TestCodeDedupFilter_StateIsScopedToInvocation(t *testing.T) {
	store := newCodeDedupStore()

	invA := &agent.Invocation{InvocationID: "same-id"}
	ctxA := agent.NewInvocationContext(context.Background(), invA)
	store.filter(ctxA, &KnowledgeSearchResponse{
		Documents: []*DocumentResult{newDedupDoc("pkg.A")},
	})

	entry, ok := invA.RunOptions.RuntimeState[codeDedupRuntimeStateKey]
	require.True(t, ok, "dedup entry should be stored on invocation runtime state")
	require.NotNil(t, entry)

	invB := &agent.Invocation{InvocationID: "same-id"}
	ctxB := agent.NewInvocationContext(context.Background(), invB)
	out := store.filter(ctxB, &KnowledgeSearchResponse{
		Documents: []*DocumentResult{newDedupDoc("pkg.A")},
	})
	require.Len(t, out.Documents, 1, "separate invocations must not share dedup state")
}

// TestCodeDedupKey_RepoPrefixIsolatesSameSymbolAcrossRepos verifies that the
// dedup key includes trpc_ast_repo_name, so that the same full_name coming
// from different repositories is NOT mistakenly collapsed.
func TestCodeDedupKey_RepoPrefixIsolatesSameSymbolAcrossRepos(t *testing.T) {
	docRepoA := &DocumentResult{Metadata: map[string]any{
		"trpc_ast_repo_name": "repo-a",
		"trpc_ast_full_name": "pkg.Shared",
	}}
	docRepoB := &DocumentResult{Metadata: map[string]any{
		"trpc_ast_repo_name": "repo-b",
		"trpc_ast_full_name": "pkg.Shared",
	}}
	keyA := codeDedupKey(docRepoA)
	keyB := codeDedupKey(docRepoB)
	require.NotEmpty(t, keyA)
	require.NotEmpty(t, keyB)
	require.NotEqual(t, keyA, keyB, "dedup keys across repos must differ")
}

// TestCodeDedupFilter_CustomCapEvictsOldest verifies the per-invocation cap
// Option path: when the cap is 2 and three distinct keys are inserted across
// two calls, the oldest key is evicted and can reappear in a later call.
func TestCodeDedupFilter_CustomCapEvictsOldest(t *testing.T) {
	store := newCodeDedupStoreWithCap(2)
	ctx := newDedupTestCtx("inv-cap")

	store.filter(ctx, &KnowledgeSearchResponse{
		Documents: []*DocumentResult{newDedupDoc("pkg.A"), newDedupDoc("pkg.B")},
	})
	// Inserting pkg.C triggers eviction of pkg.A (oldest).
	store.filter(ctx, &KnowledgeSearchResponse{
		Documents: []*DocumentResult{newDedupDoc("pkg.C")},
	})

	// Now pkg.A should look new again (it was evicted).
	out := store.filter(ctx, &KnowledgeSearchResponse{
		Documents: []*DocumentResult{newDedupDoc("pkg.A")},
	})
	require.Len(t, out.Documents, 1)
	require.Equal(t, "pkg.A", out.Documents[0].Metadata["trpc_ast_full_name"])
}

// TestScalarFromMeta_FloatPrecisionPreserved verifies that scalarFromMeta
// does not collapse two distinct float values that differ only beyond %g's
// default precision, which would otherwise cause spurious dedup hits.
func TestScalarFromMeta_FloatPrecisionPreserved(t *testing.T) {
	md1 := map[string]any{"trpc_ast_line_start": 1.0000000001}
	md2 := map[string]any{"trpc_ast_line_start": 1.0000000002}
	s1, ok1 := scalarFromMeta(md1, "trpc_ast_line_start")
	s2, ok2 := scalarFromMeta(md2, "trpc_ast_line_start")
	require.True(t, ok1)
	require.True(t, ok2)
	require.NotEqual(t, s1, s2, "distinct float64 values must map to distinct keys")
}

func TestCodeDedupFilter_NilAndKeylessResponses(t *testing.T) {
	store := newCodeDedupStore()
	ctx := newDedupTestCtx("inv-empty")

	require.Nil(t, store.filter(ctx, nil))

	empty := &KnowledgeSearchResponse{}
	require.Same(t, empty, store.filter(ctx, empty))

	resp := &KnowledgeSearchResponse{
		Message: "unchanged",
		Documents: []*DocumentResult{
			{Text: "doc-a"},
			{Text: "doc-b", Metadata: map[string]any{}},
		},
	}
	out := store.filter(ctx, resp)
	require.Same(t, resp, out)
	require.Len(t, out.Documents, 2)
	require.Equal(t, "unchanged", out.Message)
}

func TestCodeDedupKeyFallbacksAndScalarBranches(t *testing.T) {
	require.Empty(t, codeDedupKey(nil))
	require.Empty(t, codeDedupKey(&DocumentResult{}))

	require.Equal(t, "repo:repo-a|full_name:pkg.F", codeDedupKey(&DocumentResult{
		Metadata: map[string]any{
			"trpc_ast_repo_name": "repo-a",
			"trpc_ast_full_name": "pkg.F",
		},
	}))

	require.Equal(t, "span:path/to/file.go:10-12", codeDedupKey(&DocumentResult{
		Metadata: map[string]any{
			"trpc_ast_file_path":  "path/to/file.go",
			"trpc_ast_line_start": int64(10),
			"trpc_ast_line_end":   float64(12),
		},
	}))

	require.Equal(t, "repo:repo-a|file:path/to/file.go", codeDedupKey(&DocumentResult{
		Metadata: map[string]any{
			"trpc_ast_repo_name": "repo-a",
			"trpc_ast_file_path": "path/to/file.go",
		},
	}))
}

func TestScalarFromMeta_TypeCoverage(t *testing.T) {
	md := map[string]any{
		"string":        "v",
		"int":           7,
		"int64":         int64(9),
		"float_int":     float64(3),
		"float_precise": 3.25,
		"bool":          true,
		"nil":           nil,
	}

	_, ok := scalarFromMeta(md, "missing")
	require.False(t, ok)

	_, ok = scalarFromMeta(md, "nil")
	require.False(t, ok)

	val, ok := scalarFromMeta(md, "string")
	require.True(t, ok)
	require.Equal(t, "v", val)

	val, ok = scalarFromMeta(md, "int")
	require.True(t, ok)
	require.Equal(t, "7", val)

	val, ok = scalarFromMeta(md, "int64")
	require.True(t, ok)
	require.Equal(t, "9", val)

	val, ok = scalarFromMeta(md, "float_int")
	require.True(t, ok)
	require.Equal(t, "3", val)

	val, ok = scalarFromMeta(md, "float_precise")
	require.True(t, ok)
	require.Equal(t, "3.25", val)

	val, ok = scalarFromMeta(md, "bool")
	require.True(t, ok)
	require.Equal(t, "true", val)
}
