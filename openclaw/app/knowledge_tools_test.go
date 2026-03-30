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

	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

func TestRawKnowledgeComponentUnmarshalYAML_StoresNode(t *testing.T) {
	t.Parallel()

	var cfg struct {
		Embedder rawKnowledgeComponent `yaml:"embedder"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(`
embedder:
  type: openai
`), &cfg))
	require.NotNil(t, cfg.Embedder.Node)
	require.Equal(t, yaml.MappingNode, cfg.Embedder.Node.Kind)
}

func TestBuildKnowledgeTools_SingleKnowledgeUsesGenericTool(t *testing.T) {
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
	require.Len(t, bundle.tools, 1)
	require.Equal(t, "knowledge_search", bundle.tools[0].Declaration().Name)
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
	require.Len(t, bundle.tools, 2)
	require.Equal(t, "docs_kb_knowledge_search", bundle.tools[0].Declaration().Name)
	require.Equal(t, "faq_knowledge_search", bundle.tools[1].Declaration().Name)
}

func TestBuildKnowledgeTools_RejectsCollidingToolNames(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeTools(map[string]*yaml.Node{
		"Docs KB": yamlNode(t, `
vector_store:
  type: inmemory
`),
		"Docs-KB": yamlNode(t, `
vector_store:
  type: inmemory
`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "knowledge tool name collision")
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
	require.Len(t, bundle.tools, 1)
	require.Equal(t, "knowledge_search", bundle.tools[0].Declaration().Name)
}

func TestBuildKnowledgeTools_SingleKnowledgeUsesGenericToolNameWithMaxResultsConfig(t *testing.T) {
	t.Parallel()

	bundle, err := buildKnowledgeTools(map[string]*yaml.Node{
		"docs": yamlNode(t, `
vector_store:
  type: inmemory
  max_results: 3
`),
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	require.Len(t, bundle.tools, 1)
	require.Equal(t, "knowledge_search", bundle.tools[0].Declaration().Name)
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

func TestBuildKnowledgeBase_DecodeStrictFailure(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeBase(yamlNode(t, `
unexpected: true
vector_store:
  type: inmemory
`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode failed")
	require.Contains(t, err.Error(), "field unexpected not found")
}

func TestBuildKnowledgeBase_RequiresVectorStore(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeBase(yamlNode(t, `
embedder:
  type: openai
`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "vector_store is required")
}

func TestBuildKnowledgeEmbedder_UnsupportedTypeFails(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeEmbedder(&rawKnowledgeComponent{Node: yamlNode(t, `
type: unsupported
`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported knowledge embedder type")
}

func TestBuildKnowledgeEmbedder_DefaultsToOpenAIWhenConfigMissing(t *testing.T) {
	t.Parallel()

	emb, err := buildKnowledgeEmbedder(nil)
	require.NoError(t, err)
	require.NotNil(t, emb)
}

func TestBuildKnowledgeEmbedder_DefaultsToOpenAIWhenNodeMissing(t *testing.T) {
	t.Parallel()

	emb, err := buildKnowledgeEmbedder(&rawKnowledgeComponent{})
	require.NoError(t, err)
	require.NotNil(t, emb)
	require.Equal(t, openaiembedder.DefaultDimensions, emb.GetDimensions())
}

func TestBuildKnowledgeEmbedder_NormalizesTypeName(t *testing.T) {
	t.Parallel()

	emb, err := buildKnowledgeEmbedder(&rawKnowledgeComponent{Node: yamlNode(t, `
type: " OPENAI "
dimensions: 256
`)})
	require.NoError(t, err)
	require.NotNil(t, emb)
	require.Equal(t, 256, emb.GetDimensions())
}

func TestBuildKnowledgeEmbedder_StrictDecodeFailure(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeEmbedder(&rawKnowledgeComponent{Node: yamlNode(t, `
type: openai
unknown: true
`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "field unknown not found")
}

func TestBuildKnowledgeEmbedder_TypeDecodeFailure(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeEmbedder(&rawKnowledgeComponent{Node: yamlNode(t, `
- type: openai
`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "embedder type invalid")
}

func TestBuildOpenAIKnowledgeEmbedder_IgnoresNonPositiveDimensions(t *testing.T) {
	t.Parallel()

	emb, err := buildOpenAIKnowledgeEmbedder(yamlNode(t, `
model: text-embedding-3-small
base_url: https://example.invalid/v1
api_key: test-key
dimensions: 0
`))
	require.NoError(t, err)
	require.NotNil(t, emb)
	require.Equal(t, openaiembedder.DefaultDimensions, emb.GetDimensions())
}

func TestBuildKnowledgeVectorStore_RequiresConfig(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeVectorStore(nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vector_store is required")
}

func TestBuildKnowledgeVectorStore_RequiresNode(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeVectorStore(&rawKnowledgeComponent{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vector_store is required")
}

func TestBuildKnowledgeVectorStore_TypeDecodeFailure(t *testing.T) {
	t.Parallel()

	_, err := buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, `
- type: inmemory
`)}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "vector_store type invalid")
}

func TestBuildKnowledgeVectorStore_InMemoryBuildsSuccessfully(t *testing.T) {
	t.Parallel()

	store, err := buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, `
type: inmemory
max_results: 0
`)}, nil)
	require.NoError(t, err)
	require.NotNil(t, store)
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

func TestKnowledgeComponentType_DefaultAndDecodeError(t *testing.T) {
	t.Parallel()

	typeName, err := knowledgeComponentType(nil)
	require.NoError(t, err)
	require.Empty(t, typeName)

	typeName, err = knowledgeComponentType(yamlNode(t, `
type: " OPENAI "
`))
	require.NoError(t, err)
	require.Equal(t, "openai", typeName)

	_, err = knowledgeComponentType(yamlNode(t, `
- type: openai
`))
	require.Error(t, err)
}

func TestKnowledgeEmbedderDimensions_CoversNilAndValue(t *testing.T) {
	t.Parallel()

	require.Zero(t, knowledgeEmbedderDimensions(nil))

	emb, err := buildOpenAIKnowledgeEmbedder(yamlNode(t, `
dimensions: 384
`))
	require.NoError(t, err)
	require.Equal(t, 384, knowledgeEmbedderDimensions(emb))
}

func TestKnowledgeToolMaxResults_ReadsVectorStoreConfig(t *testing.T) {
	t.Parallel()

	maxResults, err := knowledgeToolMaxResults(yamlNode(t, `
vector_store:
  type: pgvector
  url: postgres://user:pass@127.0.0.1:5432/dbname?sslmode=disable
  max_results: 6
`))
	require.NoError(t, err)
	require.Equal(t, 6, maxResults)
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

func TestBuildKnowledgeVectorStore_ElasticsearchUsesExplicitDimension(t *testing.T) {
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
vector_dimension: 128
max_results: 3
username: user
password: pass
api_key: token
`, server.URL))}, emb)
	require.NoError(t, err)
	require.NotNil(t, store)
	require.Contains(t, createIndexBody, `128`)
	require.NotContains(t, createIndexBody, `256`)
}

func TestBuildKnowledgeVectorStore_ElasticsearchSkipsEmbedderDimensionWhenUnavailable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/docs":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPut && r.URL.Path == "/docs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"acknowledged":true}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()

	store, err := buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, fmt.Sprintf(`
type: elasticsearch
addresses:
  - %s
index_name: docs
`, server.URL))}, nil)
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestBuildKnowledgeVectorStore_PGVectorUsesExplicitIndexDimension(t *testing.T) {
	t.Parallel()

	_, restore := stubPostgresBuilder(t)
	defer restore()

	emb, err := buildOpenAIKnowledgeEmbedder(yamlNode(t, `
dimensions: 256
`))
	require.NoError(t, err)

	store, err := buildKnowledgeVectorStore(&rawKnowledgeComponent{Node: yamlNode(t, `
type: pgvector
url: postgres://user:pass@127.0.0.1:5432/dbname?sslmode=disable
table: docs
enable_tsvector: true
index_dimension: 128
max_results: 5
`)}, emb)
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestKnowledgeToolName_UsesFallbackAndSanitizesLeadingDigits(t *testing.T) {
	t.Parallel()

	require.Equal(t, "knowledge_knowledge_search", knowledgeToolName("!!!"))
	require.Equal(t, "kb_123_knowledge_search", knowledgeToolName("123"))
	require.Equal(t, "", sanitizeKnowledgeToolSegment("!!!"))
	require.Equal(t, "docs_faq", sanitizeKnowledgeToolSegment(" Docs / FAQ "))
	require.Len(t, sanitizeKnowledgeToolSegment("abcdefghijklmnopqrstuvwxyz1234567890-extra"), 40)
	require.Equal(t, "", sanitizeKnowledgeToolSegment("___"))
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

func TestNewAgent_MultipleKnowledgeConfigsRegisterNamedTools(t *testing.T) {
	t.Parallel()

	mdl := &captureRequestModel{}
	agt, err := newAgent(mdl, agentConfig{
		AppName:    "demo",
		SkillsRoot: t.TempDir(),
		StateDir:   t.TempDir(),
		KnowledgesConfig: map[string]*yaml.Node{
			"docs": yamlNode(t, `
vector_store:
  type: inmemory
`),
			"faq": yamlNode(t, `
vector_store:
  type: inmemory
`),
		},
	}, nil, nil)
	require.NoError(t, err)

	req := runAgentAndCapture(t, agt, mdl, nil)
	require.Contains(t, req.Tools, "docs_knowledge_search")
	require.Contains(t, req.Tools, "faq_knowledge_search")
}

func TestNewAgent_InvalidKnowledgeConfigReturnsError(t *testing.T) {
	t.Parallel()

	_, err := newAgent(&captureRequestModel{}, agentConfig{
		AppName:    "demo",
		SkillsRoot: t.TempDir(),
		StateDir:   t.TempDir(),
		KnowledgesConfig: map[string]*yaml.Node{
			"docs": yamlNode(t, `
unexpected: true
vector_store:
  type: inmemory
`),
		},
	}, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), `knowledge "docs" config invalid`)
}
