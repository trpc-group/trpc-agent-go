//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBuildKnowledgeTools_SingleKnowledgeUsesDefaultTool(t *testing.T) {
	t.Parallel()

	bundle, err := buildKnowledgeTools(map[string]*yaml.Node{
		"docs": yamlNode(t, `
embedder:
  type: openai
vector_store:
  type: inmemory
`),
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.defaultKnowledge)
	require.Empty(t, bundle.tools)
}

func TestBuildKnowledgeTools_MultipleKnowledgesCreateNamedTools(t *testing.T) {
	t.Parallel()

	bundle, err := buildKnowledgeTools(map[string]*yaml.Node{
		"Docs KB": yamlNode(t, `
vector_store:
  type: inmemory
`),
		"FAQ": yamlNode(t, `
vector_store:
  type: inmemory
`),
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.Nil(t, bundle.defaultKnowledge)
	require.Len(t, bundle.tools, 2)
	require.Equal(t, "docs_kb_knowledge_search", bundle.tools[0].Declaration().Name)
	require.Equal(t, "faq_knowledge_search", bundle.tools[1].Declaration().Name)
}

func TestBuildKnowledgeTools_InvalidConfigFails(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeTools(map[string]*yaml.Node{
		"docs": yamlNode(t, `
vector_store:
  type: nope
`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported vector_store.type")
}

func TestBuildKnowledgeTools_EmptyConfigsReturnNil(t *testing.T) {
	t.Parallel()

	bundle, err := buildKnowledgeTools(nil)
	require.NoError(t, err)
	require.Nil(t, bundle)

	bundle, err = buildKnowledgeTools(map[string]*yaml.Node{
		" ":    yamlNode(t, `vector_store: {type: inmemory}`),
		"docs": nil,
	})
	require.NoError(t, err)
	require.Nil(t, bundle)
}

func TestBuildKnowledgeTools_EmbedderDefaultsToOpenAI(t *testing.T) {
	t.Parallel()

	bundle, err := buildKnowledgeTools(map[string]*yaml.Node{
		"docs": yamlNode(t, `
embedder:
  model: text-embedding-3-small
vector_store:
  type: inmemory
`),
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.NotNil(t, bundle.defaultKnowledge)
}

func TestBuildKnowledgeTools_VectorStoreUsesTypeSpecificValidation(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeTools(map[string]*yaml.Node{
		"docs": yamlNode(t, `
vector_store:
  type: inmemory
  addresses:
    - http://127.0.0.1:9200
`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "field addresses not found")
}

func TestBuildKnowledgeTools_VectorStoreTypeIsRequired(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeTools(map[string]*yaml.Node{
		"docs": yamlNode(t, `
vector_store:
  max_results: 5
`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "vector_store.type is required")
}

func TestBuildKnowledgeBases_TrimsNamesAndSkipsEmptyEntries(t *testing.T) {
	t.Parallel()

	knowledges, err := buildKnowledgeBases(map[string]*yaml.Node{
		" docs ": yamlNode(t, `
vector_store:
  type: inmemory
`),
		"": yamlNode(t, `
vector_store:
  type: inmemory
`),
		"nil": nil,
	})
	require.NoError(t, err)
	require.Len(t, knowledges, 1)
	require.Contains(t, knowledges, "docs")
}

func TestBuildKnowledgeBase_NilNodeReturnsNil(t *testing.T) {
	t.Parallel()

	kb, err := buildKnowledgeBase(nil)
	require.NoError(t, err)
	require.Nil(t, kb)
}

func TestBuildKnowledgeEmbedder_UnsupportedTypeFails(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeEmbedder(&rawKnowledgeComponent{Node: yamlNode(t, `
type: unsupported
`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported knowledge embedder type")
}

func TestBuildKnowledgeVectorStore_RequiresConfig(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeVectorStore(nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vector_store is required")
}

func TestBuildKnowledgeVectorStore_PGVectorRequiresURL(t *testing.T) {
	t.Parallel()

	emb, err := buildOpenAIKnowledgeEmbedder(yamlNode(t, `
dimensions: 128
`))
	require.NoError(t, err)

	_, err = buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, `
type: pgvector
`)}, emb)
	require.Error(t, err)
	require.Contains(t, err.Error(), "pgvector requires vector_store.url")
}

func TestBuildKnowledgeVectorStore_PGVectorBuildsSuccessfully(t *testing.T) {
	t.Parallel()

	_, restore := stubPostgresBuilder(t)
	defer restore()

	emb, err := buildOpenAIKnowledgeEmbedder(yamlNode(t, `
dimensions: 128
`))
	require.NoError(t, err)

	store, err := buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, `
type: pgvector
url: postgres://user:pass@127.0.0.1:5432/dbname?sslmode=disable
max_results: 5
`)}, emb)
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestBuildKnowledgeVectorStore_ElasticsearchRequiresAddresses(t *testing.T) {
	t.Parallel()

	emb, err := buildOpenAIKnowledgeEmbedder(yamlNode(t, `
dimensions: 256
`))
	require.NoError(t, err)

	_, err = buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, `
type: elasticsearch
`)}, emb)
	require.Error(t, err)
	require.Contains(t, err.Error(), "elasticsearch requires vector_store.addresses")
}

func TestBuildKnowledgeVectorStore_ElasticsearchBuildsSuccessfully(t *testing.T) {
	t.Parallel()

	var createIndexBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/docs":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/docs":
			bodyReader := io.Reader(r.Body)
			if r.Header.Get("Content-Encoding") == "gzip" {
				gz, err := gzip.NewReader(r.Body)
				require.NoError(t, err)
				defer gz.Close()
				bodyReader = gz
			}
			body, err := io.ReadAll(bodyReader)
			require.NoError(t, err)
			createIndexBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"acknowledged":true}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	emb, err := buildOpenAIKnowledgeEmbedder(yamlNode(t, `
dimensions: 256
`))
	require.NoError(t, err)

	store, err := buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, fmt.Sprintf(`
type: elasticsearch
addresses:
  - %s
index_name: docs
`, server.URL))}, emb)
	require.NoError(t, err)
	require.NotNil(t, store)
	require.NotEmpty(t, createIndexBody)
	require.Contains(t, createIndexBody, `"dims"`)
	require.Contains(t, createIndexBody, `256`)
}

func TestKnowledgeToolName_UsesFallbackAndSanitizesLeadingDigits(t *testing.T) {
	t.Parallel()

	require.Equal(t, "knowledge_knowledge_search", knowledgeToolName("!!!"))
	require.Equal(t, "kb_123_knowledge_search", knowledgeToolName("123"))
	require.Equal(t, "", sanitizeKnowledgeToolSegment("!!!"))
	require.Len(t, sanitizeKnowledgeToolSegment("abcdefghijklmnopqrstuvwxyz1234567890-extra"), 40)
}

func TestNewAgent_KnowledgeConfigRegistersSearchTool(t *testing.T) {
	t.Parallel()

	mdl := &captureRequestModel{}
	agt, err := newAgent(mdl, agentConfig{
		AppName:      "demo",
		SkillsRoot:   t.TempDir(),
		StateDir:     t.TempDir(),
		SystemPrompt: "sys",
		KnowledgesConfig: map[string]*yaml.Node{
			"docs": yamlNode(t, `
vector_store:
  type: inmemory
`),
		},
	}, nil, nil)
	require.NoError(t, err)

	req := runAgentAndCapture(t, agt, mdl, nil)
	require.Contains(t, req.Tools, "knowledge_search")
}
