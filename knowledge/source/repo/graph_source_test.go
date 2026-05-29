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
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	docreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/graph"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/codeast"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
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
		{
			name: "tag target kind with tag value",
			info: &repoInfo{
				url:        "https://github.com/example/repo",
				branch:     "v1.0.0",
				targetKind: checkoutTargetTag,
			},
			want: "repo:https://github.com/example/repo#tag:v1.0.0",
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

type stringChunkIndexReader struct{}

func (r *stringChunkIndexReader) ReadFromFile(_ string) ([]*document.Document, error) {
	return []*document.Document{
		{Name: "doc", Content: "content", Metadata: map[string]any{source.MetaChunkIndex: "not-an-int"}},
	}, nil
}

func (r *stringChunkIndexReader) ReadFromReader(_ string, _ io.Reader) ([]*document.Document, error) {
	return nil, nil
}

func (r *stringChunkIndexReader) ReadFromURL(_ string) ([]*document.Document, error) {
	return nil, nil
}

func (r *stringChunkIndexReader) Name() string                  { return "string-chunk-idx" }
func (r *stringChunkIndexReader) SupportedExtensions() []string { return []string{".md"} }

type noChunkIndexReader struct{}

func (r *noChunkIndexReader) ReadFromFile(_ string) ([]*document.Document, error) {
	return []*document.Document{
		{Name: "doc", Content: "content", Metadata: map[string]any{"custom_key": "value"}},
	}, nil
}

func (r *noChunkIndexReader) ReadFromReader(_ string, _ io.Reader) ([]*document.Document, error) {
	return nil, nil
}

func (r *noChunkIndexReader) ReadFromURL(_ string) ([]*document.Document, error) {
	return nil, nil
}

func (r *noChunkIndexReader) Name() string                  { return "no-chunk-idx" }
func (r *noChunkIndexReader) SupportedExtensions() []string { return []string{".md"} }

func TestReadGraphZeroParseConcurrencySkipsOption(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, filepath.Join(dir, "main.go"), "package demo\n\nfunc Main() {}\n")

	parser := &configurableDirectoryParser{result: &codeast.Result{}}
	codeast.RegisterDirectoryParser(codeast.FileTypeGo, parser)
	defer codeast.RegisterDirectoryParser(codeast.FileTypeGo, stubDirectoryParser{})

	t.Run("default zero skips option", func(t *testing.T) {
		src := New(WithRepository(Repository{Dir: dir}))
		_, err := src.ReadGraph(context.Background())
		require.NoError(t, err)
		require.Zero(t, codeast.ParseConcurrency(parser.opts))
	})

	t.Run("negative option with negative source skips option", func(t *testing.T) {
		parser.opts = nil
		src := New(
			WithRepository(Repository{Dir: dir}),
			WithParseConcurrency(-1),
		)
		_, err := src.ReadGraph(context.Background(), source.WithReadGraphParseConcurrency(-5))
		require.NoError(t, err)
		require.Zero(t, codeast.ParseConcurrency(parser.opts))
	})
}

func TestReadGraphPassesAllowedGoFilesToParser(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.go")
	skipPath := filepath.Join(dir, "skip.pb.go")
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module example.com/demo\n\ngo 1.21\n")
	writeRepoFile(t, mainPath, "package demo\n\nfunc Main() {}\n")
	writeRepoFile(t, skipPath, "package demo\n\nfunc Skip() {}\n")

	parser := &configurableDirectoryParser{result: &codeast.Result{}}
	codeast.RegisterDirectoryParser(codeast.FileTypeGo, parser)
	defer codeast.RegisterDirectoryParser(codeast.FileTypeGo, stubDirectoryParser{})

	src := New(
		WithRepository(Repository{Dir: dir}),
		WithSkipSuffixes([]string{".pb.go"}),
	)
	_, err := src.ReadGraph(context.Background())
	require.NoError(t, err)

	includeFiles := codeast.ParseIncludeFiles(parser.opts)
	require.Equal(t, []string{mainPath}, includeFiles)
}

func TestReadGraphResolveRepositoryError(t *testing.T) {
	src := New()
	_, err := src.ReadGraph(context.Background())
	require.Error(t, err)
}

func TestReadGraphResolveScanRootError(t *testing.T) {
	src := New(WithRepository(Repository{Dir: t.TempDir(), Subdir: "../escape"}))
	_, err := src.ReadGraph(context.Background())
	require.Error(t, err)
}

func TestReadGraphFromRemoteRepo(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\nfunc Run() {}\n",
		},
	}}, nil)

	src := New(WithRepository(Repository{URL: remoteURL, Branch: "main"}))
	data, err := src.ReadGraph(context.Background())
	require.NoError(t, err)
	require.NotNil(t, data)
}

func TestReadGraphFromRemoteRepoWithDocExtensions(t *testing.T) {
	remoteURL, _ := createRemoteRepo(t, []repoCommit{{
		branch: "main",
		files: map[string]string{
			"go.mod":     "module example.com/demo\n\ngo 1.21\n",
			"service.go": "package demo\n\nfunc Run() {}\n",
			"README.md":  "# Demo\n\nHello world\n",
		},
	}}, nil)

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	src := New(
		WithRepository(Repository{URL: remoteURL, Branch: "main"}),
		WithDocExtensions([]string{".md"}),
	)
	data, err := src.ReadGraph(context.Background())
	require.NoError(t, err)
	require.NotNil(t, data)
	require.NotEmpty(t, data.Nodes)

	docCount := countGraphDocumentNodes(data)
	require.Equal(t, 1, docCount)
}

func TestReadGraphDocumentNodesUseConfiguredReaders(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "notes.txt"), "DROPkeep")

	src := New(
		WithRepository(Repository{Dir: dir}),
		WithDocExtensions([]string{".txt"}),
		WithTransformers(transform.NewCharFilter("DROP")),
	)
	data, err := src.ReadGraph(context.Background())
	require.NoError(t, err)

	var docNode *graph.Node
	for _, node := range data.Nodes {
		if node.Metadata[source.MetaFilePath] == "notes.txt" {
			docNode = node
			break
		}
	}
	require.NotNil(t, docNode)
	require.Equal(t, "keep", docNode.Content)
	require.Equal(t, len([]rune("keep")), docNode.Metadata[source.MetaContentLength])
}

func TestAllowedGoPathsFiltersTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeRepoFile(t, filepath.Join(dir, "main_test.go"), "package main\n")
	writeRepoFile(t, filepath.Join(dir, "README.md"), "# readme\n")
	writeRepoFile(t, filepath.Join(dir, "lib", "util.go"), "package lib\n")
	writeRepoFile(t, filepath.Join(dir, "lib", "util_test.go"), "package lib\n")

	src := New(WithFileExtensions([]string{".go", ".md"}))
	allowed, err := src.allowedGoPaths(dir, dir)
	require.NoError(t, err)

	require.Contains(t, allowed, "main.go")
	require.Contains(t, allowed, filepath.ToSlash("lib/util.go"))
	require.NotContains(t, allowed, "main_test.go")
	require.NotContains(t, allowed, filepath.ToSlash("lib/util_test.go"))
	require.NotContains(t, allowed, "README.md")
}

func TestAllowedGoPathsErrorOnInvalidRoot(t *testing.T) {
	src := New()
	_, err := src.allowedGoPaths(t.TempDir(), filepath.Join(t.TempDir(), "nonexistent"))
	require.Error(t, err)
}

func TestReadDocumentNodesErrorOnInvalidRoot(t *testing.T) {
	src := New(WithDocExtensions([]string{".md"}))
	_, err := src.readDocumentNodes(
		context.Background(),
		filepath.Join(t.TempDir(), "nonexistent"),
		t.TempDir(),
		&repoInfo{name: "demo"},
	)
	require.Error(t, err)
}

func TestReadDocumentNodesChunkIndexEdgeCases(t *testing.T) {
	t.Run("non-int chunk index falls back to loop index for ID", func(t *testing.T) {
		dir := t.TempDir()
		writeRepoFile(t, filepath.Join(dir, "doc.md"), "content\n")

		docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
			return &stringChunkIndexReader{}
		})
		defer docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
			return &testMarkdownReader{}
		})

		src := New(
			WithRepository(Repository{Dir: dir}),
			WithDocExtensions([]string{".md"}),
		)
		src.readers["markdown"] = &stringChunkIndexReader{}
		nodes, err := src.readDocumentNodes(context.Background(), dir, dir, &repoInfo{name: "demo"})
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.NotEmpty(t, nodes[0].ID)
		require.Equal(t, "content", nodes[0].Content)
	})

	t.Run("metadata without chunk index key uses loop index", func(t *testing.T) {
		dir := t.TempDir()
		writeRepoFile(t, filepath.Join(dir, "doc.md"), "content\n")

		docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
			return &noChunkIndexReader{}
		})
		defer docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
			return &testMarkdownReader{}
		})

		src := New(
			WithRepository(Repository{Dir: dir}),
			WithDocExtensions([]string{".md"}),
		)
		src.readers["markdown"] = &noChunkIndexReader{}
		nodes, err := src.readDocumentNodes(context.Background(), dir, dir, &repoInfo{name: "demo"})
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.Equal(t, 0, nodes[0].Metadata[source.MetaChunkIndex])
		require.Equal(t, "value", nodes[0].Metadata["custom_key"])
	})
}

func TestGraphNodeFromCodeASTUnicodeContent(t *testing.T) {
	astNode := &codeast.Node{
		ID: "pkg.Unicode", Name: "Unicode", FullName: "pkg.Unicode",
		Type: codeast.EntityFunction, Code: "func Unicode() string { return \"你好世界\" }",
		Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
	}
	node := graphNodeFromCodeAST(astNode, "node:unicode", "pkg/unicode.go", nil)
	require.Equal(t, len([]rune(astNode.Code)), node.Metadata[source.MetaContentLength])
	require.NotEqual(t, len(astNode.Code), node.Metadata[source.MetaContentLength])
}

func TestGraphNodeFromDocumentChunkUnicodeContent(t *testing.T) {
	doc := &document.Document{
		Name:    "notes.md",
		Content: "你好世界 Hello World",
	}
	node := graphNodeFromDocumentChunk(doc, "node:udc", "notes.md", 0, nil)
	require.Equal(t, len([]rune(doc.Content)), node.Metadata[source.MetaContentLength])
}

func TestGraphDataFromCodeASTSelfReferentialEdge(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "service.go"), "package demo\n")

	src := New(WithRepository(Repository{Dir: dir}))
	result := &codeast.Result{
		Nodes: []*codeast.Node{
			{
				ID: "pkg.Recursive", Type: codeast.EntityFunction,
				Name: "Recursive", FullName: "pkg.Recursive",
				Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
				Code:      "func Recursive() { Recursive() }",
				FilePath:  filepath.Join(dir, "service.go"),
				LineStart: 1, LineEnd: 1,
			},
		},
		Edges: []*codeast.Edge{
			{FromID: "pkg.Recursive", ToID: "pkg.Recursive", Type: codeast.RelationCalls},
		},
	}
	allowed := map[string]struct{}{"service.go": {}}
	data := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo"}, allowed)
	require.Len(t, data.Nodes, 1)
	require.Len(t, data.Edges, 1)
	require.Equal(t, data.Edges[0].FromID, data.Edges[0].ToID)
}

func TestGraphDataFromCodeASTEdgeNilMetadata(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "service.go"), "package demo\n")

	src := New(WithRepository(Repository{Dir: dir}))
	result := &codeast.Result{
		Nodes: []*codeast.Node{
			{
				ID: "pkg.A", Type: codeast.EntityFunction,
				Name: "A", FullName: "pkg.A",
				Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
				Code: "func A() {}", FilePath: filepath.Join(dir, "service.go"),
				LineStart: 1, LineEnd: 1,
			},
			{
				ID: "pkg.B", Type: codeast.EntityFunction,
				Name: "B", FullName: "pkg.B",
				Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
				Code: "func B() {}", FilePath: filepath.Join(dir, "service.go"),
				LineStart: 2, LineEnd: 2,
			},
		},
		Edges: []*codeast.Edge{
			{FromID: "pkg.A", ToID: "pkg.B", Type: codeast.RelationCalls, Metadata: nil},
		},
	}
	allowed := map[string]struct{}{"service.go": {}}
	data := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo"}, allowed)
	require.Len(t, data.Edges, 1)
	require.NotNil(t, data.Edges[0].Metadata)
	require.Equal(t, "repo_graph_source", data.Edges[0].Metadata["builder"])
}

func TestGraphDataFromCodeASTEmptyAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	src := New(WithRepository(Repository{Dir: dir}))
	result := &codeast.Result{
		Nodes: []*codeast.Node{
			{
				ID: "pkg.Func", Type: codeast.EntityFunction,
				Name: "Func", FullName: "pkg.Func",
				Language: codeast.LanguageGo, Scope: codeast.ScopeCode,
				Code: "func Func() {}", FilePath: filepath.Join(dir, "service.go"),
				LineStart: 1, LineEnd: 1,
			},
		},
		Edges: []*codeast.Edge{
			{FromID: "pkg.Func", ToID: "pkg.Func", Type: codeast.RelationCalls},
		},
	}
	data := src.graphDataFromCodeAST(result, dir, &repoInfo{name: "demo"}, map[string]struct{}{})
	require.Empty(t, data.Nodes)
	require.Empty(t, data.Edges)
}

func TestReadDocumentNodesEmptyDocExtensions(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "doc.md"), "# hello\n")

	src := New(WithDocExtensions([]string{}))
	nodes, err := src.readDocumentNodes(context.Background(), dir, dir, &repoInfo{name: "demo"})
	require.NoError(t, err)
	require.Empty(t, nodes)
}

func TestReadDocumentNodesWithInfoBranchMetadata(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, filepath.Join(dir, "doc.md"), "# content\n")

	docreader.RegisterReader([]string{".md"}, func(opts ...docreader.Option) docreader.Reader {
		return &testMarkdownReader{}
	})

	src := New(
		WithRepository(Repository{Dir: dir, RepoName: "my-repo", RepoURL: "https://example.com/repo.git"}),
		WithDocExtensions([]string{".md"}),
	)
	nodes, err := src.readDocumentNodes(
		context.Background(), dir, dir,
		&repoInfo{name: "my-repo", url: "https://example.com/repo.git", branch: "develop", targetKind: checkoutTargetBranch},
	)
	require.NoError(t, err)
	require.NotEmpty(t, nodes)
	for _, node := range nodes {
		require.Contains(t, node.ID, "node:")
		require.NotEmpty(t, node.Metadata[source.MetaFilePath])
	}
}

func TestRepoGraphNamespaceCommitTargetKind(t *testing.T) {
	info := &repoInfo{
		url:        "https://example.com/repo",
		branch:     "abc123",
		targetKind: checkoutTargetCommit,
	}
	got := repoGraphNamespace(info)
	require.Equal(t, "repo:https://example.com/repo#commit:abc123", got)
}
