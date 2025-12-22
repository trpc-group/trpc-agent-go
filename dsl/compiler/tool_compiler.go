//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package compiler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/toolspec"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	geminiemb "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
	huggingfaceemb "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/huggingface"
	openaiemb "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"

	// External vectorstore/embedder implementations (require replace directives in go.mod)
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/milvus"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

// ToolCompileResult contains the compiled tools from ToolSpec.
type ToolCompileResult struct {
	Tools           []tool.Tool
	ToolSets        []tool.ToolSet
	Knowledge       []knowledge.Knowledge
	KnowledgeTools  []tool.Tool
	CodeExecutor    codeexecutor.CodeExecutor
	HasCodeExecutor bool
}

// CompileTools compiles tool specifications from DSL config into runtime tools.
func CompileTools(cfg map[string]any, toolsProvider dsl.ToolProvider) (*ToolCompileResult, error) {
	result := &ToolCompileResult{}

	toolsConfig, ok := cfg["tools"]
	if !ok {
		return result, nil
	}

	parsed, err := toolspec.ParseTools(toolsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tools: %w", err)
	}

	// Validate tool configurations to prevent silent overrides
	if err := validateToolSpecs(parsed); err != nil {
		return nil, err
	}

	// Compile builtin tools
	if len(parsed.BuiltinTools) > 0 {
		if toolsProvider == nil {
			return nil, fmt.Errorf("builtin tools configured but no ToolProvider available: %v", parsed.BuiltinTools)
		}
		for _, name := range parsed.BuiltinTools {
			t, err := toolsProvider.Get(name)
			if err != nil {
				return nil, fmt.Errorf("builtin tool %q not found in ToolProvider: %w", name, err)
			}
			result.Tools = append(result.Tools, t)
		}
	}

	// Compile MCP tools
	for _, spec := range parsed.MCPTools {
		toolSet, err := compileMCPTool(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to compile mcp tool: %w", err)
		}
		result.ToolSets = append(result.ToolSets, toolSet)
	}

	// Compile Web Search tools
	for _, spec := range parsed.WebSearchTools {
		t := compileWebSearchTool(spec)
		result.Tools = append(result.Tools, t)
	}

	// Compile Knowledge Search tools
	for _, spec := range parsed.KnowledgeSearchTools {
		kb, kbTool, err := compileKnowledgeSearchTool(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to compile knowledge_search tool: %w", err)
		}
		result.Knowledge = append(result.Knowledge, kb)
		result.KnowledgeTools = append(result.KnowledgeTools, kbTool)
	}

	// Compile Code Interpreter tools
	for _, spec := range parsed.CodeInterpreterTools {
		executor, err := compileCodeInterpreterTool(spec)
		if err != nil {
			return nil, fmt.Errorf("failed to compile code_interpreter tool: %w", err)
		}
		result.CodeExecutor = executor
		result.HasCodeExecutor = true
	}

	return result, nil
}

// validateToolSpecs checks for configuration errors that would cause silent tool overrides.
func validateToolSpecs(parsed *toolspec.ParseResult) error {
	// Check for multiple web_search tools (all produce fixed name "duckduckgo_search")
	if len(parsed.WebSearchTools) > 1 {
		return fmt.Errorf("multiple web_search tools configured: all web_search tools produce the same tool name 'duckduckgo_search', only the last one would be effective; remove duplicates or use different tool types")
	}

	// Check for multiple knowledge_search tools (all produce fixed name "knowledge_search")
	if len(parsed.KnowledgeSearchTools) > 1 {
		return fmt.Errorf("multiple knowledge_search tools configured: all knowledge_search tools produce the same tool name 'knowledge_search', only the last one would be effective; remove duplicates")
	}

	// Check for multiple code_interpreter tools (only one executor can be active)
	if len(parsed.CodeInterpreterTools) > 1 {
		return fmt.Errorf("multiple code_interpreter tools configured: only one code executor can be active, remove duplicates")
	}

	// Check for duplicate builtin tool names
	builtinSeen := make(map[string]bool)
	for _, name := range parsed.BuiltinTools {
		if builtinSeen[name] {
			return fmt.Errorf("duplicate builtin tool %q configured: remove the duplicate entry", name)
		}
		builtinSeen[name] = true
	}

	// Check for MCP tools: require server_label when multiple MCP servers are configured
	if len(parsed.MCPTools) > 1 {
		mcpLabels := make(map[string]int) // label -> count
		for i, spec := range parsed.MCPTools {
			label := spec.ServerLabel
			if label == "" {
				label = "mcp" // default toolset name
			}
			mcpLabels[label]++
			if mcpLabels[label] > 1 {
				if spec.ServerLabel == "" {
					return fmt.Errorf("multiple mcp tools configured without server_label: mcp tools[%d] and others use default toolset name 'mcp', which causes tool name conflicts; add unique server_label to each mcp tool", i)
				}
				return fmt.Errorf("duplicate mcp server_label %q: mcp tools with the same server_label will have conflicting tool name prefixes; use unique server_label for each mcp server", label)
			}
		}
	}

	return nil
}

func compileMCPTool(spec *toolspec.MCPToolSpec) (tool.ToolSet, error) {
	transport := spec.Transport
	if transport == "" {
		transport = "streamable_http"
	}

	// Resolve secrets in headers
	headers := make(map[string]string, len(spec.Headers))
	for k, v := range spec.Headers {
		headers[k] = toolspec.ResolveSecret(v)
	}

	cfgMap := map[string]any{
		"transport":  transport,
		"server_url": spec.ServerURL,
	}
	if len(headers) > 0 {
		cfgMap["headers"] = headers
	}
	if len(spec.AllowedTools) > 0 {
		cfgMap["tool_filter"] = spec.AllowedTools
	}
	// Use server_label as toolset name to avoid conflicts when multiple MCP servers are configured
	if spec.ServerLabel != "" {
		cfgMap["name"] = spec.ServerLabel
	}

	return createMCPToolSet(cfgMap)
}

func compileWebSearchTool(spec *toolspec.WebSearchToolSpec) tool.Tool {
	// Resolve API key secret
	apiKey := toolspec.ResolveSecret(spec.APIKey)

	switch spec.Provider {
	case "duckduckgo", "":
		// Note: max_results config is ignored as the underlying duckduckgo package
		// uses a fixed maxResults=5. To support configurable max_results, the
		// duckduckgo package would need to be extended with WithMaxResults option.
		var opts []duckduckgo.Option
		opts = append(opts, duckduckgo.WithHTTPClient(&http.Client{
			Timeout: 30 * time.Second,
		}))
		return duckduckgo.NewTool(opts...)
	case "google", "bing":
		// For providers requiring API key, use DuckDuckGo as fallback if no key
		if apiKey == "" {
			return duckduckgo.NewTool()
		}
		// TODO: Implement Google/Bing search when available
		return duckduckgo.NewTool()
	default:
		return duckduckgo.NewTool()
	}
}

func compileKnowledgeSearchTool(spec *toolspec.KnowledgeSearchToolSpec) (knowledge.Knowledge, tool.Tool, error) {
	vs, err := createVectorStore(spec.VectorStore)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create vector store: %w", err)
	}

	emb, err := createEmbedder(spec.Embedder)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create embedder: %w", err)
	}

	opts := []knowledge.Option{
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
	}

	kb := knowledge.New(opts...)

	// Create knowledge search tool with options
	var toolOpts []knowledgetool.Option
	if spec.MaxResults > 0 {
		toolOpts = append(toolOpts, knowledgetool.WithMaxResults(spec.MaxResults))
	}
	if spec.MinScore > 0 {
		toolOpts = append(toolOpts, knowledgetool.WithMinScore(spec.MinScore))
	}

	// Add conditioned filter if specified
	if spec.ConditionedFilter != nil {
		filterCondition := convertFilterCondition(spec.ConditionedFilter)
		if filterCondition != nil {
			toolOpts = append(toolOpts, knowledgetool.WithConditionedFilter(filterCondition))
		}
	}

	// Decide which tool to create based on agentic_filter config
	var kbTool tool.Tool
	if spec.AgenticFilter != nil && spec.AgenticFilter.Enabled {
		// Use NewAgenticFilterSearchTool for LLM-driven dynamic filtering
		kbTool = knowledgetool.NewAgenticFilterSearchTool(kb, spec.AgenticFilter.Info, toolOpts...)
	} else {
		// Use standard NewKnowledgeSearchTool
		kbTool = knowledgetool.NewKnowledgeSearchTool(kb, toolOpts...)
	}

	return kb, kbTool, nil
}

// convertFilterCondition converts toolspec.FilterCondition to searchfilter.UniversalFilterCondition.
func convertFilterCondition(fc *toolspec.FilterCondition) *searchfilter.UniversalFilterCondition {
	if fc == nil {
		return nil
	}

	result := &searchfilter.UniversalFilterCondition{
		Field:    fc.Field,
		Operator: fc.Operator,
	}

	// Handle logical operators (and/or) - Value should be array of FilterCondition
	if fc.Operator == "and" || fc.Operator == "or" {
		if conditions, ok := fc.Value.([]any); ok {
			subConditions := make([]*searchfilter.UniversalFilterCondition, 0, len(conditions))
			for _, c := range conditions {
				if condMap, ok := c.(map[string]any); ok {
					subFC := mapToFilterCondition(condMap)
					if subFC != nil {
						subConditions = append(subConditions, convertFilterCondition(subFC))
					}
				}
			}
			result.Value = subConditions
		}
	} else {
		// For comparison operators, keep the value as-is
		result.Value = fc.Value
	}

	return result
}

// mapToFilterCondition converts a map[string]any to *toolspec.FilterCondition.
func mapToFilterCondition(m map[string]any) *toolspec.FilterCondition {
	fc := &toolspec.FilterCondition{}
	if field, ok := m["field"].(string); ok {
		fc.Field = field
	}
	if op, ok := m["operator"].(string); ok {
		fc.Operator = op
	}
	fc.Value = m["value"]
	return fc
}

// createVectorStore creates a vector store instance based on the config type.
// Directly instantiates the appropriate implementation without factory registration.
func createVectorStore(cfg *toolspec.VectorStoreConfig) (vectorstore.VectorStore, error) {
	if cfg == nil {
		return nil, fmt.Errorf("vector_store config is required")
	}

	// Resolve secrets
	cfg.Password = toolspec.ResolveSecret(cfg.Password)

	switch cfg.Type {
	case toolspec.VectorStorePgVector:
		return createPgVectorStore(cfg)
	case toolspec.VectorStoreMilvus:
		return createMilvusStore(cfg)
	case toolspec.VectorStoreElasticsearch:
		return createElasticsearchStore(cfg)
	case toolspec.VectorStoreTCVector:
		return createTCVectorStore(cfg)
	default:
		return nil, fmt.Errorf("unsupported vector store type: %q", cfg.Type)
	}
}

func createPgVectorStore(cfg *toolspec.VectorStoreConfig) (vectorstore.VectorStore, error) {
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	table := cfg.Table
	if table == "" {
		table = "documents"
	}
	dimension := cfg.Dimension
	if dimension == 0 {
		dimension = 1536
	}
	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}

	opts := []pgvector.Option{
		pgvector.WithHost(cfg.Host),
		pgvector.WithPort(port),
		pgvector.WithUser(cfg.User),
		pgvector.WithPassword(cfg.Password),
		pgvector.WithDatabase(cfg.Database),
		pgvector.WithTable(table),
		pgvector.WithIndexDimension(dimension),
		pgvector.WithSSLMode(sslMode),
	}

	return pgvector.New(opts...)
}

func createMilvusStore(cfg *toolspec.VectorStoreConfig) (vectorstore.VectorStore, error) {
	dimension := cfg.Dimension
	if dimension == 0 {
		dimension = 1536
	}

	opts := []milvus.Option{
		milvus.WithAddress(cfg.Address),
		milvus.WithCollectionName(cfg.Collection),
		milvus.WithDimension(dimension),
	}

	// Milvus requires context for initialization
	return milvus.New(context.Background(), opts...)
}

func createElasticsearchStore(cfg *toolspec.VectorStoreConfig) (vectorstore.VectorStore, error) {
	dimension := cfg.Dimension
	if dimension == 0 {
		dimension = 1536
	}

	opts := []elasticsearch.Option{
		elasticsearch.WithAddresses(cfg.Addresses),
		elasticsearch.WithIndexName(cfg.Index),
		elasticsearch.WithVectorDimension(dimension),
	}

	return elasticsearch.New(opts...)
}

func createTCVectorStore(cfg *toolspec.VectorStoreConfig) (vectorstore.VectorStore, error) {
	// Resolve URL secret
	url := toolspec.ResolveSecret(cfg.URL)
	user := toolspec.ResolveSecret(cfg.User)

	opts := []tcvector.Option{
		tcvector.WithURL(url),
		tcvector.WithUsername(user),
		tcvector.WithPassword(cfg.Password), // Already resolved in createVectorStore
		tcvector.WithDatabase(cfg.Database),
		tcvector.WithCollection(cfg.Collection),
	}

	if cfg.Dimension > 0 {
		opts = append(opts, tcvector.WithIndexDimension(uint32(cfg.Dimension)))
	}

	return tcvector.New(opts...)
}

// createEmbedder creates an embedder instance based on the config type.
// Directly instantiates the appropriate implementation without factory registration.
func createEmbedder(cfg *toolspec.EmbedderConfig) (embedder.Embedder, error) {
	if cfg == nil {
		return nil, fmt.Errorf("embedder config is required")
	}

	// Resolve secrets
	cfg.APIKey = toolspec.ResolveSecret(cfg.APIKey)

	switch cfg.Type {
	case toolspec.EmbedderOpenAI:
		return createOpenAIEmbedder(cfg)
	case toolspec.EmbedderOllama:
		return createOllamaEmbedder(cfg)
	case toolspec.EmbedderGemini:
		return createGeminiEmbedder(cfg)
	case toolspec.EmbedderHuggingFace:
		return createHuggingFaceEmbedder(cfg)
	default:
		return nil, fmt.Errorf("unsupported embedder type: %q", cfg.Type)
	}
}

func createOpenAIEmbedder(cfg *toolspec.EmbedderConfig) (embedder.Embedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai embedder requires api_key")
	}

	model := cfg.Model
	if model == "" {
		model = "text-embedding-3-small"
	}
	dimensions := cfg.Dimensions
	if dimensions == 0 {
		dimensions = 1536
	}

	opts := []openaiemb.Option{
		openaiemb.WithAPIKey(cfg.APIKey),
		openaiemb.WithModel(model),
		openaiemb.WithDimensions(dimensions),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, openaiemb.WithBaseURL(cfg.BaseURL))
	}

	return openaiemb.New(opts...), nil
}

func createOllamaEmbedder(cfg *toolspec.EmbedderConfig) (embedder.Embedder, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("ollama embedder requires base_url")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("ollama embedder requires model")
	}

	opts := []ollama.Option{
		ollama.WithHost(cfg.BaseURL),
		ollama.WithModel(cfg.Model),
	}

	return ollama.New(opts...), nil
}

func createGeminiEmbedder(cfg *toolspec.EmbedderConfig) (embedder.Embedder, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini embedder requires api_key")
	}

	opts := []geminiemb.Option{
		geminiemb.WithAPIKey(cfg.APIKey),
	}
	if cfg.Model != "" {
		opts = append(opts, geminiemb.WithModel(cfg.Model))
	}
	if cfg.Dimensions > 0 {
		opts = append(opts, geminiemb.WithDimensions(cfg.Dimensions))
	}

	return geminiemb.New(context.Background(), opts...)
}

func createHuggingFaceEmbedder(cfg *toolspec.EmbedderConfig) (embedder.Embedder, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("huggingface embedder requires base_url")
	}

	opts := []huggingfaceemb.Option{
		huggingfaceemb.WithBaseURL(cfg.BaseURL),
	}
	if cfg.Dimensions > 0 {
		opts = append(opts, huggingfaceemb.WithDimensions(cfg.Dimensions))
	}
	if cfg.Normalize {
		opts = append(opts, huggingfaceemb.WithNormalize(cfg.Normalize))
	}
	if cfg.PromptName != "" {
		opts = append(opts, huggingfaceemb.WithPromptName(cfg.PromptName))
	}
	if cfg.Truncate {
		opts = append(opts, huggingfaceemb.WithTruncate(cfg.Truncate))
	}
	if cfg.TruncationDirection != "" {
		opts = append(opts, huggingfaceemb.WithTruncationDirection(huggingfaceemb.TruncateDirection(cfg.TruncationDirection)))
	}
	if cfg.EmbedRoute != "" {
		opts = append(opts, huggingfaceemb.WithEmbedRoute(huggingfaceemb.EmbedRoute(cfg.EmbedRoute)))
	}

	return huggingfaceemb.New(opts...), nil
}

func compileCodeInterpreterTool(spec *toolspec.CodeInterpreterToolSpec) (codeexecutor.CodeExecutor, error) {
	if spec.Executor == nil {
		return local.New(), nil
	}

	switch spec.Executor.Type {
	case toolspec.ExecutorLocal, "":
		return createLocalExecutor(spec.Executor)
	case toolspec.ExecutorContainer:
		return nil, fmt.Errorf("container executor not yet implemented")
	default:
		return nil, fmt.Errorf("unknown executor type: %s", spec.Executor.Type)
	}
}

func createLocalExecutor(cfg *toolspec.ExecutorConfig) (codeexecutor.CodeExecutor, error) {
	var opts []local.CodeExecutorOption

	if cfg.WorkDir != "" {
		opts = append(opts, local.WithWorkDir(cfg.WorkDir))
	}
	if cfg.TimeoutSeconds > 0 {
		opts = append(opts, local.WithTimeout(time.Duration(cfg.TimeoutSeconds)*time.Second))
	}
	if cfg.CleanTempFiles != nil {
		opts = append(opts, local.WithCleanTempFiles(*cfg.CleanTempFiles))
	}

	return local.New(opts...), nil
}
