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
