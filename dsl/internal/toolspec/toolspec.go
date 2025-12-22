//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package toolspec provides unified tool specification types and parsing logic
// for the DSL compiler.
package toolspec

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ToolType represents the type of tool specification.
type ToolType string

const (
	ToolTypeMCP             ToolType = "mcp"
	ToolTypeBuiltin         ToolType = "builtin"
	ToolTypeWebSearch       ToolType = "web_search"
	ToolTypeKnowledgeSearch ToolType = "knowledge_search"
	ToolTypeCodeInterpreter ToolType = "code_interpreter"
)

// ToolSpec is the unified tool specification interface.
type ToolSpec interface {
	Type() ToolType
}

// MCPToolSpec represents an MCP tool configuration.
type MCPToolSpec struct {
	ServerURL    string            `json:"server_url"`
	Transport    string            `json:"transport,omitempty"`
	ServerLabel  string            `json:"server_label,omitempty"`
	AllowedTools []string          `json:"allowed_tools,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
}

func (m *MCPToolSpec) Type() ToolType { return ToolTypeMCP }

// BuiltinToolSpec represents a built-in tool reference.
type BuiltinToolSpec struct {
	Name string `json:"name"`
}

func (b *BuiltinToolSpec) Type() ToolType { return ToolTypeBuiltin }

// WebSearchToolSpec represents a web search tool configuration.
type WebSearchToolSpec struct {
	Provider   string `json:"provider,omitempty"`
	APIKey     string `json:"api_key,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

func (w *WebSearchToolSpec) Type() ToolType { return ToolTypeWebSearch }

// KnowledgeSearchToolSpec represents a knowledge search tool configuration.
type KnowledgeSearchToolSpec struct {
	VectorStore *VectorStoreConfig `json:"vector_store"`
	Embedder    *EmbedderConfig    `json:"embedder"`
	MaxResults  int                `json:"max_results,omitempty"`
	MinScore    float64            `json:"min_score,omitempty"`
}

func (k *KnowledgeSearchToolSpec) Type() ToolType { return ToolTypeKnowledgeSearch }

// VectorStoreType represents the type of vector store.
type VectorStoreType string

const (
	VectorStorePgVector      VectorStoreType = "pgvector"
	VectorStoreMilvus        VectorStoreType = "milvus"
	VectorStoreElasticsearch VectorStoreType = "elasticsearch"
	VectorStoreTCVector      VectorStoreType = "tcvector"
)

// VectorStoreConfig is the vector store configuration.
type VectorStoreConfig struct {
	Type       VectorStoreType `json:"type"`
	Host       string          `json:"host,omitempty"`
	Port       int             `json:"port,omitempty"`
	User       string          `json:"user,omitempty"`
	Password   string          `json:"password,omitempty"`
	Database   string          `json:"database,omitempty"`
	Table      string          `json:"table,omitempty"`
	Dimension  int             `json:"dimension,omitempty"`
	SSLMode    string          `json:"ssl_mode,omitempty"`
	Address    string          `json:"address,omitempty"`
	Collection string          `json:"collection,omitempty"`
	Addresses  []string        `json:"addresses,omitempty"`
	Index      string          `json:"index,omitempty"`
	// TCVector specific fields
	URL string `json:"url,omitempty"`
}

// EmbedderType represents the type of embedder.
type EmbedderType string

const (
	EmbedderOpenAI      EmbedderType = "openai"
	EmbedderOllama      EmbedderType = "ollama"
	EmbedderGemini      EmbedderType = "gemini"
	EmbedderHuggingFace EmbedderType = "huggingface"
)

// EmbedderConfig is the embedder configuration.
type EmbedderConfig struct {
	Type       EmbedderType `json:"type"`
	APIKey     string       `json:"api_key,omitempty"`
	BaseURL    string       `json:"base_url,omitempty"`
	Model      string       `json:"model,omitempty"`
	Dimensions int          `json:"dimensions,omitempty"`
	// HuggingFace specific fields
	Normalize           bool   `json:"normalize,omitempty"`
	PromptName          string `json:"prompt_name,omitempty"`
	Truncate            bool   `json:"truncate,omitempty"`
	TruncationDirection string `json:"truncation_direction,omitempty"` // "Left" or "Right"
	EmbedRoute          string `json:"embed_route,omitempty"`          // "/embed" or "/embed_all"
}

// CodeInterpreterToolSpec represents a code interpreter tool configuration.
type CodeInterpreterToolSpec struct {
	Executor *ExecutorConfig `json:"executor,omitempty"`
}

func (c *CodeInterpreterToolSpec) Type() ToolType { return ToolTypeCodeInterpreter }

// ExecutorType represents the type of code executor.
type ExecutorType string

const (
	ExecutorLocal     ExecutorType = "local"
	ExecutorContainer ExecutorType = "container"
)

// ExecutorConfig is the code executor configuration.
type ExecutorConfig struct {
	Type           ExecutorType `json:"type"`
	WorkDir        string       `json:"work_dir,omitempty"`
	TimeoutSeconds int          `json:"timeout_seconds,omitempty"`
	CleanTempFiles *bool        `json:"clean_temp_files,omitempty"`
	Image          string       `json:"image,omitempty"`
}

// ParseResult contains the parsed tool specifications.
type ParseResult struct {
	BuiltinTools         []string
	MCPTools             []*MCPToolSpec
	WebSearchTools       []*WebSearchToolSpec
	KnowledgeSearchTools []*KnowledgeSearchToolSpec
	CodeInterpreterTools []*CodeInterpreterToolSpec
}

// ParseTools parses the tools configuration from DSL config.
// Accepts both []any (from JSON unmarshal) and []map[string]any (from manual construction).
func ParseTools(toolsConfig any) (*ParseResult, error) {
	if toolsConfig == nil {
		return &ParseResult{}, nil
	}

	// Normalize to []map[string]any to handle both JSON unmarshal and manual construction
	var items []map[string]any

	switch v := toolsConfig.(type) {
	case []any:
		// From JSON unmarshal: []interface{}
		items = make([]map[string]any, 0, len(v))
		for i, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("tools[%d]: expected object, got %T", i, item)
			}
			items = append(items, obj)
		}
	case []map[string]any:
		// From manual construction
		items = v
	default:
		return nil, fmt.Errorf("tools must be an array, got %T", toolsConfig)
	}

	result := &ParseResult{}

	for i, obj := range items {
		if err := parseToolSpec(obj, result, i); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func parseToolSpec(obj map[string]any, result *ParseResult, index int) error {
	typeVal, ok := obj["type"].(string)
	if !ok || typeVal == "" {
		return fmt.Errorf("tools[%d]: 'type' field is required", index)
	}

	data, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("tools[%d]: failed to marshal: %w", index, err)
	}

	switch ToolType(typeVal) {
	case ToolTypeMCP:
		var spec MCPToolSpec
		if err := json.Unmarshal(data, &spec); err != nil {
			return fmt.Errorf("tools[%d]: invalid mcp spec: %w", index, err)
		}
		if spec.ServerURL == "" {
			return fmt.Errorf("tools[%d]: mcp requires server_url", index)
		}
		if spec.Transport == "" {
			spec.Transport = "streamable_http"
		}
		result.MCPTools = append(result.MCPTools, &spec)

	case ToolTypeBuiltin:
		var spec BuiltinToolSpec
		if err := json.Unmarshal(data, &spec); err != nil {
			return fmt.Errorf("tools[%d]: invalid builtin spec: %w", index, err)
		}
		if spec.Name == "" {
			return fmt.Errorf("tools[%d]: builtin requires name", index)
		}
		result.BuiltinTools = append(result.BuiltinTools, spec.Name)

	case ToolTypeWebSearch:
		var spec WebSearchToolSpec
		if err := json.Unmarshal(data, &spec); err != nil {
			return fmt.Errorf("tools[%d]: invalid web_search spec: %w", index, err)
		}
		if spec.Provider == "" {
			spec.Provider = "duckduckgo"
		}
		result.WebSearchTools = append(result.WebSearchTools, &spec)

	case ToolTypeKnowledgeSearch:
		var spec KnowledgeSearchToolSpec
		if err := json.Unmarshal(data, &spec); err != nil {
			return fmt.Errorf("tools[%d]: invalid knowledge_search spec: %w", index, err)
		}
		if spec.VectorStore == nil {
			return fmt.Errorf("tools[%d]: knowledge_search requires vector_store", index)
		}
		if spec.Embedder == nil {
			return fmt.Errorf("tools[%d]: knowledge_search requires embedder", index)
		}
		result.KnowledgeSearchTools = append(result.KnowledgeSearchTools, &spec)

	case ToolTypeCodeInterpreter:
		var spec CodeInterpreterToolSpec
		if err := json.Unmarshal(data, &spec); err != nil {
			return fmt.Errorf("tools[%d]: invalid code_interpreter spec: %w", index, err)
		}
		result.CodeInterpreterTools = append(result.CodeInterpreterTools, &spec)

	default:
		return fmt.Errorf("tools[%d]: unknown tool type %q", index, typeVal)
	}

	return nil
}

// ResolveSecret resolves a value that may be an environment variable reference.
// Format: "env:VAR_NAME" resolves to os.Getenv("VAR_NAME").
func ResolveSecret(value string) string {
	if strings.HasPrefix(value, "env:") {
		return os.Getenv(strings.TrimPrefix(value, "env:"))
	}
	return value
}

// HasAnyTools returns true if the ParseResult contains any tools.
func (r *ParseResult) HasAnyTools() bool {
	return len(r.BuiltinTools) > 0 ||
		len(r.MCPTools) > 0 ||
		len(r.WebSearchTools) > 0 ||
		len(r.KnowledgeSearchTools) > 0 ||
		len(r.CodeInterpreterTools) > 0
}
