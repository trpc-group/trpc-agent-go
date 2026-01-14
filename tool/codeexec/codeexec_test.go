//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexec

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// mockCodeExecutor is a mock implementation of codeexecutor.CodeExecutor for testing.
type mockCodeExecutor struct {
	result codeexecutor.CodeExecutionResult
	err    error
}

func (m *mockCodeExecutor) ExecuteCode(_ context.Context, _ codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return m.result, m.err
}

func (m *mockCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func TestNewTool(t *testing.T) {
	exec := &mockCodeExecutor{}

	t.Run("default config", func(t *testing.T) {
		ct := NewTool(exec)
		decl := ct.Declaration()

		assert.Equal(t, "execute_code", decl.Name)
		assert.Contains(t, decl.Description, "Execute code")
		assert.Equal(t, []string{"language", "code"}, decl.InputSchema.Required)

		langSchema := decl.InputSchema.Properties["language"]
		assert.Equal(t, "string", langSchema.Type)
		assert.Equal(t, []any{"python", "bash"}, langSchema.Enum)

		require.NotNil(t, decl.OutputSchema)
		assert.Equal(t, "object", decl.OutputSchema.Type)
		assert.Equal(t, []string{"output"}, decl.OutputSchema.Required)
		assert.Equal(t, "string", decl.OutputSchema.Properties["output"].Type)
		assert.Equal(t, "string", decl.OutputSchema.Properties["error"].Type)
	})

	t.Run("with custom name", func(t *testing.T) {
		ct := NewTool(exec, WithName("run_code"))
		decl := ct.Declaration()

		assert.Equal(t, "run_code", decl.Name)
	})

	t.Run("with custom description", func(t *testing.T) {
		ct := NewTool(exec, WithDescription("Custom description"))
		decl := ct.Declaration()

		assert.Equal(t, "Custom description", decl.Description)
	})

	t.Run("with custom languages", func(t *testing.T) {
		ct := NewTool(exec, WithLanguages("python", "javascript", "go"))
		decl := ct.Declaration()

		langSchema := decl.InputSchema.Properties["language"]
		assert.Equal(t, []any{"python", "javascript", "go"}, langSchema.Enum)
	})

	t.Run("with multiple options", func(t *testing.T) {
		ct := NewTool(exec,
			WithName("custom_exec"),
			WithDescription("My code executor"),
			WithLanguages("python"),
		)
		decl := ct.Declaration()

		assert.Equal(t, "custom_exec", decl.Name)
		assert.Equal(t, "My code executor", decl.Description)
		assert.Equal(t, []any{"python"}, decl.InputSchema.Properties["language"].Enum)
	})

}

func TestExecuteCodeTool_Call(t *testing.T) {
	t.Run("successful execution", func(t *testing.T) {
		exec := &mockCodeExecutor{
			result: codeexecutor.CodeExecutionResult{
				Output: "Hello, World!\n",
			},
		}
		ct := NewTool(exec)

		input := ExecuteCodeInput{
			Language: "python",
			Code:     "print('Hello, World!')",
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)

		require.NoError(t, err)
		output, ok := result.(ExecuteCodeOutput)
		require.True(t, ok)
		assert.Equal(t, "Hello, World!\n", output.Output)
		assert.Empty(t, output.Error)
	})

	t.Run("execution with error", func(t *testing.T) {
		exec := &mockCodeExecutor{
			result: codeexecutor.CodeExecutionResult{
				Output: "partial output",
			},
			err: errors.New("execution failed: syntax error"),
		}
		ct := NewTool(exec)

		input := ExecuteCodeInput{
			Language: "python",
			Code:     "print('unclosed",
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)

		require.NoError(t, err) // Call itself doesn't return error
		output, ok := result.(ExecuteCodeOutput)
		require.True(t, ok)
		assert.Equal(t, "partial output", output.Output)
		assert.Equal(t, "execution failed: syntax error", output.Error)
	})

	t.Run("invalid JSON input", func(t *testing.T) {
		exec := &mockCodeExecutor{}
		ct := NewTool(exec)

		_, err := ct.Call(context.Background(), []byte("invalid json"))

		require.Error(t, err)
	})

	t.Run("unsupported language", func(t *testing.T) {
		exec := &mockCodeExecutor{}
		ct := NewTool(exec) // default languages: python, bash

		input := ExecuteCodeInput{
			Language: "javascript",
			Code:     "console.log('hi')",
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)

		output, ok := result.(ExecuteCodeOutput)
		require.True(t, ok)
		assert.Empty(t, output.Output)
		assert.Equal(t, "unsupported language", output.Error)
	})

	t.Run("bash execution", func(t *testing.T) {
		exec := &mockCodeExecutor{
			result: codeexecutor.CodeExecutionResult{
				Output: "file1.txt\nfile2.txt\n",
			},
		}
		ct := NewTool(exec)

		input := ExecuteCodeInput{
			Language: "bash",
			Code:     "ls -la",
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)

		require.NoError(t, err)
		output, ok := result.(ExecuteCodeOutput)
		require.True(t, ok)
		assert.Contains(t, output.Output, "file1.txt")
	})
}

func TestExecuteCodeTool_Declaration(t *testing.T) {
	exec := &mockCodeExecutor{}
	ct := NewTool(exec)
	decl := ct.Declaration()

	t.Run("has correct structure", func(t *testing.T) {
		assert.NotEmpty(t, decl.Name)
		assert.NotEmpty(t, decl.Description)
		assert.NotNil(t, decl.InputSchema)
	})

	t.Run("input schema is object type", func(t *testing.T) {
		assert.Equal(t, "object", decl.InputSchema.Type)
	})

	t.Run("has required fields", func(t *testing.T) {
		assert.Contains(t, decl.InputSchema.Required, "language")
		assert.Contains(t, decl.InputSchema.Required, "code")
	})

	t.Run("language property has enum", func(t *testing.T) {
		langProp := decl.InputSchema.Properties["language"]
		assert.NotNil(t, langProp)
		assert.NotEmpty(t, langProp.Enum)
	})

	t.Run("code property is string", func(t *testing.T) {
		codeProp := decl.InputSchema.Properties["code"]
		assert.NotNil(t, codeProp)
		assert.Equal(t, "string", codeProp.Type)
	})

	t.Run("output schema exists", func(t *testing.T) {
		require.NotNil(t, decl.OutputSchema)
		assert.Equal(t, "object", decl.OutputSchema.Type)
		assert.Equal(t, []string{"output"}, decl.OutputSchema.Required)
		assert.Equal(t, "string", decl.OutputSchema.Properties["output"].Type)
	})
}
