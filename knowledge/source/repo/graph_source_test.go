//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package repo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func TestRepoGraphNamespace(t *testing.T) {
	tests := []struct {
		name string
		info *repoInfo
		want string
	}{
		{
			name: "nil info returns unknown default",
			info: nil,
			want: "repo:unknown#default",
		},
		{
			name: "info with url and branch",
			info: &repoInfo{
				url:        "https://github.com/example/repo",
				branch:     "main",
				targetKind: checkoutTargetBranch,
			},
			want: "repo:https://github.com/example/repo#branch:main",
		},
		{
			name: "info with name only and no url",
			info: &repoInfo{
				name: "my-repo",
			},
			want: "repo:my-repo#default",
		},
		{
			name: "info with empty targetKind uses default",
			info: &repoInfo{
				url: "https://github.com/example/repo",
			},
			want: "repo:https://github.com/example/repo#default",
		},
		{
			name: "url takes precedence over name",
			info: &repoInfo{
				name: "my-repo",
				url:  "https://github.com/example/repo",
			},
			want: "repo:https://github.com/example/repo#default",
		},
		{
			name: "empty url and name falls back to unknown",
			info: &repoInfo{},
			want: "repo:unknown#default",
		},
		{
			name: "tag target kind without branch",
			info: &repoInfo{
				url:        "https://github.com/example/repo",
				targetKind: checkoutTargetTag,
			},
			want: "repo:https://github.com/example/repo#tag",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := repoGraphNamespace(tc.info)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRepoGraphNodeKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		kind      string
		value     string
		want      string
	}{
		{
			name:      "with value",
			namespace: "repo:example#default",
			kind:      "symbol",
			value:     "pkg.Func",
			want:      "repo:example#default#symbol:pkg.Func",
		},
		{
			name:      "without value",
			namespace: "repo:example#default",
			kind:      "symbol",
			value:     "",
			want:      "repo:example#default#symbol",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := repoGraphNodeKey(tc.namespace, tc.kind, tc.value)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestGeneratedGraphID(t *testing.T) {
	tests := []struct {
		name string
		kind string
		key  string
	}{
		{name: "node kind", kind: "node", key: "some-key"},
		{name: "edge kind", kind: "edge", key: "another-key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := generatedGraphID(tc.kind, tc.key)
			require.Contains(t, id, tc.kind+":")
			require.Len(t, id, len(tc.kind)+1+24) // kind + ":" + 12 bytes hex = 24 chars
		})
	}

	t.Run("deterministic for same input", func(t *testing.T) {
		id1 := generatedGraphID("node", "same-key")
		id2 := generatedGraphID("node", "same-key")
		require.Equal(t, id1, id2)
	})

	t.Run("different input produces different output", func(t *testing.T) {
		id1 := generatedGraphID("node", "key-a")
		id2 := generatedGraphID("node", "key-b")
		require.NotEqual(t, id1, id2)
	})
}

func TestRepoGraphEdgeKey(t *testing.T) {
	got := repoGraphEdgeKey("from-node", "calls", "to-node")
	require.Equal(t, "from-node::calls::to-node", got)
}

func TestCloneAnyMap(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		require.Nil(t, cloneAnyMap(nil))
	})

	t.Run("clones map correctly", func(t *testing.T) {
		original := map[string]any{"a": 1, "b": "two"}
		cloned := cloneAnyMap(original)
		require.Equal(t, original, cloned)
	})

	t.Run("mutation of clone does not affect original", func(t *testing.T) {
		original := map[string]any{"key": "value"}
		cloned := cloneAnyMap(original)
		cloned["key"] = "changed"
		cloned["extra"] = "new"
		require.Equal(t, "value", original["key"])
		require.NotContains(t, original, "extra")
	})

	t.Run("empty map returns empty map", func(t *testing.T) {
		original := map[string]any{}
		cloned := cloneAnyMap(original)
		require.NotNil(t, cloned)
		require.Empty(t, cloned)
	})
}

func TestGraphNodeFromCodeAST(t *testing.T) {
	tests := []struct {
		name         string
		astNode      *codeast.Node
		nodeID       string
		relPath      string
		baseMetadata map[string]any
	}{
		{
			name: "basic fields mapped correctly",
			astNode: &codeast.Node{
				ID:        "pkg.MyFunc",
				Name:      "MyFunc",
				FullName:  "pkg.MyFunc",
				Type:      codeast.EntityFunction,
				Code:      "func MyFunc() {}",
				Language:  codeast.LanguageGo,
				Scope:     codeast.ScopeCode,
				FilePath:  "/repo/pkg/myfunc.go",
				LineStart: 10,
				LineEnd:   12,
				Signature: "func MyFunc()",
				Package:   "pkg",
				Comment:   "MyFunc does something.",
			},
			nodeID:  "node:abc123",
			relPath: "pkg/myfunc.go",
			baseMetadata: map[string]any{
				"source": "repo",
			},
		},
		{
			name: "nil base metadata still creates metadata map",
			astNode: &codeast.Node{
				ID:       "pkg.Simple",
				Name:     "Simple",
				FullName: "pkg.Simple",
				Type:     codeast.EntityFunction,
				Code:     "func Simple() {}",
				Language: codeast.LanguageGo,
				Scope:    codeast.ScopeCode,
			},
			nodeID:       "node:def456",
			relPath:      "pkg/simple.go",
			baseMetadata: nil,
		},
		{
			name: "empty package and comment are excluded",
			astNode: &codeast.Node{
				ID:       "pkg.NoExtra",
				Name:     "NoExtra",
				FullName: "pkg.NoExtra",
				Type:     codeast.EntityFunction,
				Code:     "func NoExtra() {}",
				Language: codeast.LanguageGo,
				Scope:    codeast.ScopeCode,
				Package:  "",
				Comment:  "",
			},
			nodeID:       "node:ghi789",
			relPath:      "pkg/noextra.go",
			baseMetadata: map[string]any{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := graphNodeFromCodeAST(tc.astNode, tc.nodeID, tc.relPath, tc.baseMetadata)

			require.Equal(t, tc.nodeID, node.ID)
			require.Equal(t, tc.astNode.Name, node.Name)
			require.Equal(t, tc.astNode.Code, node.Content)
			require.NotNil(t, node.Metadata)

			require.Equal(t, string(tc.astNode.Type), node.Metadata[codeast.TrpcAstMetaPrefix+"type"])
			require.Equal(t, tc.astNode.Name, node.Metadata[codeast.TrpcAstMetaPrefix+"name"])
			require.Equal(t, tc.astNode.FullName, node.Metadata[codeast.TrpcAstMetaPrefix+"full_name"])
			require.Equal(t, string(tc.astNode.Language), node.Metadata[codeast.TrpcAstMetaPrefix+"language"])
			require.Equal(t, string(tc.astNode.Scope), node.Metadata[codeast.TrpcAstMetaPrefix+"scope"])
			require.Equal(t, tc.relPath, node.Metadata[codeast.TrpcAstMetaPrefix+"file_path"])
			require.Equal(t, tc.astNode.LineStart, node.Metadata[codeast.TrpcAstMetaPrefix+"line_start"])
			require.Equal(t, tc.astNode.LineEnd, node.Metadata[codeast.TrpcAstMetaPrefix+"line_end"])
			require.Equal(t, tc.astNode.Signature, node.Metadata[codeast.TrpcAstMetaPrefix+"signature"])
			require.Equal(t, tc.relPath, node.Metadata[source.MetaFilePath])
			require.Equal(t, tc.astNode.ChunkIndex, node.Metadata[source.MetaChunkIndex])
			require.Equal(t, len([]rune(tc.astNode.Code)), node.Metadata[source.MetaContentLength])

			if tc.astNode.Package != "" {
				require.Equal(t, tc.astNode.Package, node.Metadata[codeast.TrpcAstMetaPrefix+"package"])
			} else {
				require.NotContains(t, node.Metadata, codeast.TrpcAstMetaPrefix+"package")
			}
			if tc.astNode.Comment != "" {
				require.Equal(t, tc.astNode.Comment, node.Metadata[codeast.TrpcAstMetaPrefix+"comment"])
			} else {
				require.NotContains(t, node.Metadata, codeast.TrpcAstMetaPrefix+"comment")
			}

			require.NotContains(t, node.Metadata, source.MetaRepoURL)
			require.NotContains(t, node.Metadata, source.MetaRepoPath)
			require.NotContains(t, node.Metadata, codeast.TrpcAstMetaPrefix+"go_type_kind")
		})
	}

	t.Run("ast node metadata is merged with prefix", func(t *testing.T) {
		astNode := &codeast.Node{
			ID:       "pkg.WithMeta",
			Name:     "WithMeta",
			FullName: "pkg.WithMeta",
			Type:     codeast.EntityFunction,
			Code:     "func WithMeta() {}",
			Language: codeast.LanguageGo,
			Scope:    codeast.ScopeCode,
			Metadata: map[string]any{"receiver_type": "Service"},
		}
		node := graphNodeFromCodeAST(astNode, "node:meta", "pkg/withmeta.go", nil)
		require.Equal(t, "Service", node.Metadata[codeast.TrpcAstMetaPrefix+"receiver_type"])
	})

	t.Run("base metadata repo keys are deleted", func(t *testing.T) {
		astNode := &codeast.Node{
			ID: "pkg.Del", Name: "Del", FullName: "pkg.Del",
			Type: codeast.EntityFunction, Code: "func Del() {}",
			Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
		}
		base := map[string]any{
			source.MetaRepoURL:  "https://example.com/repo",
			source.MetaRepoPath: "/tmp/repo",
		}
		node := graphNodeFromCodeAST(astNode, "node:del", "pkg/del.go", base)
		require.NotContains(t, node.Metadata, source.MetaRepoURL)
		require.NotContains(t, node.Metadata, source.MetaRepoPath)
	})
}

func TestGraphNodeFromDocumentChunk(t *testing.T) {
	tests := []struct {
		name         string
		doc          *document.Document
		nodeID       string
		relPath      string
		chunkIndex   int
		baseMetadata map[string]any
		wantName     string
	}{
		{
			name: "basic mapping",
			doc: &document.Document{
				Name:    "README.md",
				Content: "# Hello World",
			},
			nodeID:       "node:doc1",
			relPath:      "README.md",
			chunkIndex:   0,
			baseMetadata: map[string]any{"source": "repo"},
			wantName:     "README.md",
		},
		{
			name: "empty doc name uses relPath",
			doc: &document.Document{
				Name:    "",
				Content: "some content",
			},
			nodeID:       "node:doc2",
			relPath:      "docs/guide.md",
			chunkIndex:   1,
			baseMetadata: nil,
			wantName:     "docs/guide.md",
		},
		{
			name: "doc metadata is merged excluding repo keys",
			doc: &document.Document{
				Name:    "notes.md",
				Content: "notes",
				Metadata: map[string]any{
					"custom_key":       "custom_value",
					source.MetaRepoURL: "should-be-excluded",
				},
			},
			nodeID:       "node:doc3",
			relPath:      "notes.md",
			chunkIndex:   0,
			baseMetadata: map[string]any{},
			wantName:     "notes.md",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := graphNodeFromDocumentChunk(tc.doc, tc.nodeID, tc.relPath, tc.chunkIndex, tc.baseMetadata)

			require.Equal(t, tc.nodeID, node.ID)
			require.Equal(t, tc.wantName, node.Name)
			require.Equal(t, tc.doc.Content, node.Content)
			require.NotNil(t, node.Metadata)
			require.Equal(t, tc.relPath, node.Metadata[source.MetaFilePath])
			require.Equal(t, tc.chunkIndex, node.Metadata[source.MetaChunkIndex])
			require.Equal(t, len([]rune(tc.doc.Content)), node.Metadata[source.MetaContentLength])
		})
	}

	t.Run("repo url and repo path keys from doc metadata are excluded", func(t *testing.T) {
		doc := &document.Document{
			Name:    "test.md",
			Content: "test",
			Metadata: map[string]any{
				source.MetaRepoURL:  "https://example.com",
				source.MetaRepoPath: "/tmp/repo",
				"keep":              "this",
			},
		}
		node := graphNodeFromDocumentChunk(doc, "node:x", "test.md", 0, nil)
		require.NotContains(t, node.Metadata, source.MetaRepoURL)
		require.NotContains(t, node.Metadata, source.MetaRepoPath)
		require.Equal(t, "this", node.Metadata["keep"])
	})

	t.Run("nil doc metadata does not panic", func(t *testing.T) {
		doc := &document.Document{
			Name:    "safe.md",
			Content: "safe",
		}
		node := graphNodeFromDocumentChunk(doc, "node:safe", "safe.md", 0, map[string]any{"base": true})
		require.Equal(t, true, node.Metadata["base"])
	})

	t.Run("base metadata is cloned not shared", func(t *testing.T) {
		base := map[string]any{"shared": "original"}
		doc := &document.Document{Name: "a.md", Content: "a"}
		node := graphNodeFromDocumentChunk(doc, "node:clone", "a.md", 0, base)
		node.Metadata["shared"] = "modified"
		require.Equal(t, "original", base["shared"])
	})
}

// Verify graph helpers are used in hasGraphNode/hasGraphEdge from repo_source_test.go.
func TestGraphHelpersSmokeCheck(t *testing.T) {
	data := &graph.Data{
		Nodes: []*graph.Node{
			{ID: "n1", Metadata: map[string]any{"k": "v"}},
		},
		Edges: []*graph.Edge{
			{FromID: "n1", ToID: "n2", Type: "calls"},
		},
	}
	require.True(t, hasGraphNode(data, "n1"))
	require.False(t, hasGraphNode(data, "missing"))
	require.True(t, hasGraphEdge(data, "n1", "n2", "calls"))
	require.False(t, hasGraphEdge(data, "n1", "n2", "other"))
	require.True(t, hasGraphNodeMetadataValue(data, "n1", "k", "v"))
	require.False(t, hasGraphNodeMetadataKey(data, "n1", "missing"))
}
