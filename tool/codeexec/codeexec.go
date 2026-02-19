//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package codeexec provides a code execution tool that allows LLM to execute code.
package codeexec

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures the code execution tool.
type Option func(*config)

type config struct {
	name        string
	description string
	languages   []string
}

// WithName sets the tool name.
func WithName(name string) Option {
	return func(c *config) { c.name = name }
}

// WithDescription sets the tool description.
func WithDescription(desc string) Option {
	return func(c *config) { c.description = desc }
}

// WithLanguages sets the supported languages (default: python, bash).
func WithLanguages(langs ...string) Option {
	return func(c *config) {
		// Defensive copy to avoid caller mutation.
		c.languages = append([]string(nil), langs...)
	}
}

func defaultConfig() config {
	return config{
		name:        "execute_code",
		description: "Execute code and return the result. Use for computation, data analysis, or logic verification.",
		languages:   []string{"python", "bash"},
	}
}

func applyOptions(opts ...Option) config {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.languages == nil {
		cfg.languages = []string{}
	}
	return cfg
}

// NewTool creates a new code execution tool with the given CodeExecutor and options.
//
// This follows the common pattern in this repo: return a `tool.CallableTool` interface and
// keep the concrete implementation unexported.
func NewTool(exec codeexecutor.CodeExecutor, opts ...Option) tool.CallableTool {
	cfg := applyOptions(opts...)
	return &executeCodeTool{executor: exec, cfg: cfg}
}

type executeCodeTool struct {
	executor codeexecutor.CodeExecutor
	cfg      config
}

// Declaration returns the tool's declaration.
func (t *executeCodeTool) Declaration() *tool.Declaration {
	langEnum := make([]any, len(t.cfg.languages))
	for i, l := range t.cfg.languages {
		langEnum[i] = l
	}

	return &tool.Declaration{
		Name:        t.cfg.name,
		Description: t.cfg.description,
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"code_blocks"},
			Properties: map[string]*tool.Schema{
				"code_blocks": {
					Type:        "array",
					Description: "Code blocks to execute",
					Items: &tool.Schema{
						Type:     "object",
						Required: []string{"language", "code"},
						Properties: map[string]*tool.Schema{
							"language": {
								Type:        "string",
								Enum:        langEnum,
								Description: "Programming language to execute",
							},
							"code": {
								Type:        "string",
								Description: "Code to execute",
							},
						},
					},
				},
				"execution_id": {
					Type:        "string",
					Description: "Optional execution/session identifier",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"output"},
			Properties: map[string]*tool.Schema{
				"output": {
					Type:        "string",
					Description: "Standard output from code execution",
				},
				"output_files": {
					Type:        "array",
					Description: "Files generated during code execution",
					Items: &tool.Schema{
						Type: "object",
						Properties: map[string]*tool.Schema{
							"name": {
								Type:        "string",
								Description: "File name",
							},
							"content": {
								Type:        "string",
								Description: "File content (may be omitted)",
							},
							"mime_type": {
								Type:        "string",
								Description: "MIME type (may be omitted)",
							},
						},
					},
				},
			},
		},
	}
}

// unmarshalCodeBlocks flexibly decodes code_blocks from JSON, handling common LLM
// quirks: the value may be a normal array, a single object (instead of an array),
// or a double-encoded JSON string containing either of the above.
func unmarshalCodeBlocks(raw json.RawMessage) ([]codeexecutor.CodeBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	// If the LLM double-encoded the array as a JSON string, unwrap and re-parse.
	if s, ok := val.(string); ok {
		raw = json.RawMessage(s)
		if err := json.Unmarshal(raw, &val); err != nil {
			return nil, err
		}
	}

	switch val.(type) {
	case []any:
		var blocks []codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, err
		}
		return blocks, nil
	case map[string]any:
		// Single object — wrap into a slice.
		var block codeexecutor.CodeBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, err
		}
		return []codeexecutor.CodeBlock{block}, nil
	default:
		return nil, fmt.Errorf("code_blocks: expected array, object, or string, got %T", val)
	}
}

// Call executes the code and returns the result.
func (t *executeCodeTool) Call(ctx context.Context, args []byte) (any, error) {
	aux := &struct {
		CodeBlocks  json.RawMessage `json:"code_blocks"`
		ExecutionID string          `json:"execution_id,omitempty"`
	}{}
	if err := json.Unmarshal(args, aux); err != nil {
		return nil, err
	}
	blocks, err := unmarshalCodeBlocks(aux.CodeBlocks)
	if err != nil {
		return nil, err
	}
	input := codeexecutor.CodeExecutionInput{
		CodeBlocks:  blocks,
		ExecutionID: aux.ExecutionID,
	}

	// Best-effort validation. We return it as structured tool output (instead of Go error)
	// so the model can correct itself.
	if len(input.CodeBlocks) == 0 {
		return codeexecutor.CodeExecutionResult{Output: "Error: missing code_blocks"}, nil
	}
	for i, b := range input.CodeBlocks {
		if b.Language == "" || !t.isSupportedLanguage(b.Language) {
			return codeexecutor.CodeExecutionResult{Output: fmt.Sprintf("Error: unsupported language: %d: %s", i, b.Language)}, nil
		}
	}

	return t.executor.ExecuteCode(ctx, input)
}

func (t *executeCodeTool) isSupportedLanguage(language string) bool {
	return slices.Contains(t.cfg.languages, language)
}
