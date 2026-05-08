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
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	vectors "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	inmemoryvs "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	vectorpg "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

// knowledgeEntry is the parsed intermediate representation of a knowledge
// provider configuration.
type knowledgeEntry struct {
	Type       string
	Name       string
	MaxResults int
	Config     *yaml.Node
}

type knowledgeToolsBundle struct {
	tools []tool.Tool
}

type rawKnowledgeComponent struct {
	Node *yaml.Node
}

func (r *rawKnowledgeComponent) UnmarshalYAML(node *yaml.Node) error {
	r.Node = node
	return nil
}

// builtinKnowledgeConfig is the config schema for the "builtin" knowledge
// provider (embedder + vector_store).
type builtinKnowledgeConfig struct {
	Embedder    *rawKnowledgeComponent `yaml:"embedder,omitempty"`
	VectorStore *rawKnowledgeComponent `yaml:"vector_store,omitempty"`
}

func newBuiltinKnowledge(
	_ registry.KnowledgeProviderDeps,
	spec registry.PluginSpec,
) (knowledge.Knowledge, error) {
	var cfg builtinKnowledgeConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	emb, err := buildKnowledgeEmbedder(cfg.Embedder)
	if err != nil {
		return nil, err
	}
	store, err := buildKnowledgeVectorStore(cfg.VectorStore, emb)
	if err != nil {
		return nil, err
	}
	return knowledge.New(
		knowledge.WithEmbedder(emb),
		knowledge.WithVectorStore(store),
	), nil
}

func buildKnowledgeTools(
	entries []knowledgeEntry,
) (*knowledgeToolsBundle, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	type knowledgeWithMaxResults struct {
		kb         knowledge.Knowledge
		maxResults int
	}
	knowledges := make(map[string]*knowledgeWithMaxResults, len(entries))
	for _, entry := range entries {
		f, ok := registry.LookupKnowledgeProvider(entry.Type)
		if !ok {
			return nil, fmt.Errorf(
				"unsupported knowledge provider type: %s",
				entry.Type,
			)
		}
		kb, err := f(registry.KnowledgeProviderDeps{}, registry.PluginSpec{
			Type:   entry.Type,
			Name:   entry.Name,
			Config: entry.Config,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"knowledge %q config invalid: %w",
				entry.Name,
				err,
			)
		}
		knowledges[entry.Name] = &knowledgeWithMaxResults{
			kb:         kb,
			maxResults: entry.MaxResults,
		}
	}

	names := make([]string, 0, len(knowledges))
	for name := range knowledges {
		names = append(names, name)
	}
	sort.Strings(names)

	if len(names) == 1 {
		entry := knowledges[names[0]]
		toolOpts := []knowledgetool.Option{
			knowledgetool.WithToolName("knowledge_search"),
			knowledgetool.WithToolDescription(
				"Search for relevant information in the knowledge base.",
			),
		}
		if entry.maxResults > 0 {
			toolOpts = append(
				toolOpts,
				knowledgetool.WithMaxResults(entry.maxResults),
			)
		}
		return &knowledgeToolsBundle{
			tools: []tool.Tool{
				knowledgetool.NewKnowledgeSearchTool(
					entry.kb,
					toolOpts...,
				),
			},
		}, nil
	}

	tools := make([]tool.Tool, 0, len(names))
	seenToolNames := make(map[string]string, len(names))
	for _, name := range names {
		toolName := knowledgeToolName(name)
		if existing, ok := seenToolNames[toolName]; ok {
			return nil, fmt.Errorf(
				"knowledge tool name collision: %q and %q both map to %q",
				existing,
				name,
				toolName,
			)
		}
		seenToolNames[toolName] = name
		entry := knowledges[name]
		toolOpts := []knowledgetool.Option{
			knowledgetool.WithToolName(toolName),
			knowledgetool.WithToolDescription(
				fmt.Sprintf(
					"Search for relevant information in the %q knowledge base.",
					name,
				),
			),
		}
		if entry.maxResults > 0 {
			toolOpts = append(
				toolOpts,
				knowledgetool.WithMaxResults(entry.maxResults),
			)
		}
		tools = append(tools, knowledgetool.NewAgenticFilterSearchTool(
			entry.kb,
			nil,
			toolOpts...,
		))
	}

	return &knowledgeToolsBundle{tools: tools}, nil
}

type knowledgeTypeConfig struct {
	Type string `yaml:"type,omitempty"`
}

type openAIKnowledgeEmbedderConfig struct {
	Type       string `yaml:"type,omitempty"`
	Model      string `yaml:"model,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty"`
	APIKey     string `yaml:"api_key,omitempty"`
	Dimensions *int   `yaml:"dimensions,omitempty"`
}

type inmemoryKnowledgeVectorStoreConfig struct {
	Type       string `yaml:"type,omitempty"`
	MaxResults *int   `yaml:"max_results,omitempty"`
}

type pgvectorKnowledgeVectorStoreConfig struct {
	Type           string `yaml:"type,omitempty"`
	URL            string `yaml:"url,omitempty"`
	Table          string `yaml:"table,omitempty"`
	EnableTSVector *bool  `yaml:"enable_tsvector,omitempty"`
	IndexDimension *int   `yaml:"index_dimension,omitempty"`
	MaxResults     *int   `yaml:"max_results,omitempty"`
}

type elasticsearchKnowledgeVectorStoreConfig struct {
	Type            string   `yaml:"type,omitempty"`
	Addresses       []string `yaml:"addresses,omitempty"`
	Username        string   `yaml:"username,omitempty"`
	Password        string   `yaml:"password,omitempty"`
	APIKey          string   `yaml:"api_key,omitempty"`
	IndexName       string   `yaml:"index_name,omitempty"`
	VectorDimension *int     `yaml:"vector_dimension,omitempty"`
	MaxResults      *int     `yaml:"max_results,omitempty"`
}

type knowledgeVectorStoreBuildContext struct {
	embedder embedder.Embedder
}

type knowledgeEmbedderBuilder func(
	node *yaml.Node,
) (embedder.Embedder, error)

type knowledgeVectorStoreBuilder func(
	node *yaml.Node,
	ctx knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error)

var knowledgeEmbedderBuilders = map[string]knowledgeEmbedderBuilder{
	"":       buildOpenAIKnowledgeEmbedder,
	"openai": buildOpenAIKnowledgeEmbedder,
}

var knowledgeVectorStoreBuilders = map[string]knowledgeVectorStoreBuilder{
	"inmemory":      buildInMemoryKnowledgeVectorStore,
	"pgvector":      buildPGVectorKnowledgeVectorStore,
	"elasticsearch": buildElasticsearchKnowledgeVectorStore,
}

func buildKnowledgeEmbedder(
	cfg *rawKnowledgeComponent,
) (embedder.Embedder, error) {
	if cfg == nil || cfg.Node == nil {
		return buildOpenAIKnowledgeEmbedder(nil)
	}

	typeName, err := knowledgeComponentType(cfg.Node)
	if err != nil {
		return nil, fmt.Errorf("embedder type invalid: %w", err)
	}
	builder, ok := knowledgeEmbedderBuilders[typeName]
	if !ok {
		return nil, fmt.Errorf(
			"unsupported knowledge embedder type: %s",
			typeName,
		)
	}
	return builder(cfg.Node)
}

func buildKnowledgeVectorStore(
	cfg *rawKnowledgeComponent,
	emb embedder.Embedder,
) (vectorstore.VectorStore, error) {
	if cfg == nil || cfg.Node == nil {
		return nil, fmt.Errorf("vector_store is required")
	}

	typeName, err := knowledgeComponentType(cfg.Node)
	if err != nil {
		return nil, fmt.Errorf("vector_store type invalid: %w", err)
	}
	if typeName == "" {
		return nil, fmt.Errorf("vector_store.type is required")
	}
	builder, ok := knowledgeVectorStoreBuilders[typeName]
	if !ok {
		return nil, fmt.Errorf(
			"unsupported vector_store.type: %s",
			typeName,
		)
	}
	return builder(cfg.Node, knowledgeVectorStoreBuildContext{
		embedder: emb,
	})
}

func knowledgeComponentType(node *yaml.Node) (string, error) {
	if node == nil {
		return "", nil
	}

	var cfg knowledgeTypeConfig
	if err := node.Decode(&cfg); err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(cfg.Type)), nil
}

func buildOpenAIKnowledgeEmbedder(
	node *yaml.Node,
) (embedder.Embedder, error) {
	var cfg openAIKnowledgeEmbedderConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	opts := make([]openaiembedder.Option, 0, 4)
	if v := strings.TrimSpace(cfg.Model); v != "" {
		opts = append(opts, openaiembedder.WithModel(v))
	}
	if v := strings.TrimSpace(cfg.BaseURL); v != "" {
		opts = append(opts, openaiembedder.WithBaseURL(v))
	}
	if v := strings.TrimSpace(cfg.APIKey); v != "" {
		opts = append(opts, openaiembedder.WithAPIKey(v))
	}
	if cfg.Dimensions != nil && *cfg.Dimensions > 0 {
		opts = append(opts, openaiembedder.WithDimensions(*cfg.Dimensions))
	}
	return openaiembedder.New(opts...), nil
}

func buildInMemoryKnowledgeVectorStore(
	node *yaml.Node,
	_ knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error) {
	var cfg inmemoryKnowledgeVectorStoreConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	opts := make([]inmemoryvs.Option, 0, 1)
	if cfg.MaxResults != nil && *cfg.MaxResults > 0 {
		opts = append(opts, inmemoryvs.WithMaxResults(*cfg.MaxResults))
	}
	return inmemoryvs.New(opts...), nil
}

func buildPGVectorKnowledgeVectorStore(
	node *yaml.Node,
	ctx knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error) {
	var cfg pgvectorKnowledgeVectorStoreConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("pgvector requires vector_store.url")
	}

	opts := []vectorpg.Option{
		vectorpg.WithPGVectorClientDSN(strings.TrimSpace(cfg.URL)),
	}
	if v := strings.TrimSpace(cfg.Table); v != "" {
		opts = append(opts, vectorpg.WithTable(v))
	}
	if cfg.EnableTSVector != nil {
		opts = append(opts, vectorpg.WithEnableTSVector(*cfg.EnableTSVector))
	}
	if cfg.IndexDimension != nil && *cfg.IndexDimension > 0 {
		opts = append(opts, vectorpg.WithIndexDimension(*cfg.IndexDimension))
	} else if dims := knowledgeEmbedderDimensions(ctx.embedder); dims > 0 {
		opts = append(opts, vectorpg.WithIndexDimension(dims))
	}
	if cfg.MaxResults != nil && *cfg.MaxResults > 0 {
		opts = append(opts, vectorpg.WithMaxResults(*cfg.MaxResults))
	}
	return vectorpg.New(opts...)
}

func buildElasticsearchKnowledgeVectorStore(
	node *yaml.Node,
	ctx knowledgeVectorStoreBuildContext,
) (vectorstore.VectorStore, error) {
	var cfg elasticsearchKnowledgeVectorStoreConfig
	if err := registry.DecodeStrict(node, &cfg); err != nil {
		return nil, err
	}

	if len(cfg.Addresses) == 0 {
		return nil, fmt.Errorf("elasticsearch requires vector_store.addresses")
	}

	opts := []vectors.Option{vectors.WithAddresses(cfg.Addresses)}
	if v := strings.TrimSpace(cfg.Username); v != "" {
		opts = append(opts, vectors.WithUsername(v))
	}
	if v := strings.TrimSpace(cfg.Password); v != "" {
		opts = append(opts, vectors.WithPassword(v))
	}
	if v := strings.TrimSpace(cfg.APIKey); v != "" {
		opts = append(opts, vectors.WithAPIKey(v))
	}
	if v := strings.TrimSpace(cfg.IndexName); v != "" {
		opts = append(opts, vectors.WithIndexName(v))
	}
	if cfg.VectorDimension != nil && *cfg.VectorDimension > 0 {
		opts = append(opts, vectors.WithVectorDimension(*cfg.VectorDimension))
	} else if dims := knowledgeEmbedderDimensions(ctx.embedder); dims > 0 {
		opts = append(opts, vectors.WithVectorDimension(dims))
	}
	if cfg.MaxResults != nil && *cfg.MaxResults > 0 {
		opts = append(opts, vectors.WithMaxResults(*cfg.MaxResults))
	}
	return vectors.New(opts...)
}

func knowledgeEmbedderDimensions(e embedder.Embedder) int {
	if e == nil {
		return 0
	}
	return e.GetDimensions()
}

func knowledgeToolName(name string) string {
	base := sanitizeKnowledgeToolSegment(name)
	if base == "" {
		base = "knowledge"
	}
	return base + "_knowledge_search"
}

func sanitizeKnowledgeToolSegment(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "kb_" + out
	}
	const maxBaseLen = 40
	if len(out) > maxBaseLen {
		out = strings.Trim(out[:maxBaseLen], "_")
	}
	return out
}
