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
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"output"},
			Properties: map[string]*tool.Schema{
				"output": {
					Type:        "string",
					Description: "Standard output from code execution",
				},
				"error": {
					Type:        "string",
					Description: "Error message if execution failed",
				},
			},
		},
	}
}

// ExecuteCodeInput is the input for code execution.
type ExecuteCodeInput struct {
	Language string `json:"language"`
	Code     string `json:"code"`
}

// ExecuteCodeOutput is the output of code execution.
type ExecuteCodeOutput struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// Call executes the code and returns the result.
func (t *executeCodeTool) Call(ctx context.Context, args []byte) (any, error) {
	var input ExecuteCodeInput
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}

	// Best-effort validation. We return it as structured tool output (instead of Go error)
	// so the model can correct itself.
	if !t.isSupportedLanguage(input.Language) {
		return ExecuteCodeOutput{Error: "unsupported language"}, nil
	}

	result, err := t.executor.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: input.Language,
			Code:     input.Code,
		}},
	})

	output := ExecuteCodeOutput{Output: result.Output}
	if err != nil {
		output.Error = err.Error()
	}
	return output, nil
}

func (t *executeCodeTool) isSupportedLanguage(language string) bool {
	return slices.Contains(t.cfg.languages, language)
}
