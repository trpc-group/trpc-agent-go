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
		assert.Equal(t, []string{"code_blocks"}, decl.InputSchema.Required)

		cbSchema := decl.InputSchema.Properties["code_blocks"]
		require.NotNil(t, cbSchema)
		assert.Equal(t, "array", cbSchema.Type)
		require.NotNil(t, cbSchema.Items)
		assert.Equal(t, "object", cbSchema.Items.Type)

		langSchema := cbSchema.Items.Properties["language"]
		assert.Equal(t, "string", langSchema.Type)
		assert.Equal(t, []any{"python", "bash"}, langSchema.Enum)

		require.NotNil(t, decl.OutputSchema)
		assert.Equal(t, "object", decl.OutputSchema.Type)
		assert.Equal(t, []string{"output"}, decl.OutputSchema.Required)
		assert.Equal(t, "string", decl.OutputSchema.Properties["output"].Type)
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

		cbSchema := decl.InputSchema.Properties["code_blocks"]
		require.NotNil(t, cbSchema)
		require.NotNil(t, cbSchema.Items)
		langSchema := cbSchema.Items.Properties["language"]
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
		cbSchema := decl.InputSchema.Properties["code_blocks"]
		require.NotNil(t, cbSchema)
		require.NotNil(t, cbSchema.Items)
		langSchema := cbSchema.Items.Properties["language"]
		require.NotNil(t, langSchema)
		assert.Equal(t, []any{"python"}, langSchema.Enum)
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

		input := codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{
				Language: "python",
				Code:     "print('Hello, World!')",
			}},
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)

		require.NoError(t, err)
		output, ok := result.(codeexecutor.CodeExecutionResult)
		require.True(t, ok)
		assert.Equal(t, "Hello, World!\n", output.Output)
	})

	t.Run("execution with error", func(t *testing.T) {
		exec := &mockCodeExecutor{
			result: codeexecutor.CodeExecutionResult{
				Output: "partial output",
			},
			err: errors.New("execution failed: syntax error"),
		}
		ct := NewTool(exec)

		input := codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{
				Language: "python",
				Code:     "print('unclosed",
			}},
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)

		require.Error(t, err)
		assert.Equal(t, "execution failed: syntax error", err.Error())
		output, ok := result.(codeexecutor.CodeExecutionResult)
		require.True(t, ok)
		assert.Equal(t, "partial output", output.Output)
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

		input := codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{
				Language: "javascript",
				Code:     "console.log('hi')",
			}},
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)

		output, ok := result.(codeexecutor.CodeExecutionResult)
		require.True(t, ok)
		assert.Equal(t, "Error: unsupported language: 0: javascript", output.Output)
	})

	t.Run("bash execution", func(t *testing.T) {
		exec := &mockCodeExecutor{
			result: codeexecutor.CodeExecutionResult{
				Output: "file1.txt\nfile2.txt\n",
			},
		}
		ct := NewTool(exec)

		input := codeexecutor.CodeExecutionInput{
			CodeBlocks: []codeexecutor.CodeBlock{{
				Language: "bash",
				Code:     "ls -la",
			}},
		}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)

		require.NoError(t, err)
		output, ok := result.(codeexecutor.CodeExecutionResult)
		require.True(t, ok)
		assert.Contains(t, output.Output, "file1.txt")
	})

	t.Run("missing code_blocks", func(t *testing.T) {
		exec := &mockCodeExecutor{}
		ct := NewTool(exec)

		input := codeexecutor.CodeExecutionInput{}
		args, _ := json.Marshal(input)

		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)

		output, ok := result.(codeexecutor.CodeExecutionResult)
		require.True(t, ok)
		assert.Equal(t, "Error: missing code_blocks", output.Output)
	})

	t.Run("double-encoded code_blocks string", func(t *testing.T) {
		exec := &mockCodeExecutor{
			result: codeexecutor.CodeExecutionResult{Output: "ok"},
		}
		ct := NewTool(exec)

		// Simulate LLM double-encoding: code_blocks is a JSON string containing the array.
		inner, _ := json.Marshal([]codeexecutor.CodeBlock{{Language: "python", Code: "1+1"}})
		args, _ := json.Marshal(map[string]any{
			"code_blocks": string(inner),
		})

		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)
		output, ok := result.(codeexecutor.CodeExecutionResult)
		require.True(t, ok)
		assert.Equal(t, "ok", output.Output)
	})

	t.Run("double-encoded code_blocks invalid inner JSON", func(t *testing.T) {
		exec := &mockCodeExecutor{}
		ct := NewTool(exec)

		// code_blocks is a JSON string but its content is not valid JSON.
		args, _ := json.Marshal(map[string]any{
			"code_blocks": "not valid json",
		})

		_, err := ct.Call(context.Background(), args)
		require.Error(t, err)
	})

	t.Run("single object code_block wrapped into array", func(t *testing.T) {
		exec := &mockCodeExecutor{
			result: codeexecutor.CodeExecutionResult{Output: "wrapped"},
		}
		ct := NewTool(exec)

		// Simulate LLM sending a single object instead of an array.
		args := []byte(`{"code_blocks":{"language":"python","code":"print(1)"}}`)

		result, err := ct.Call(context.Background(), args)
		require.NoError(t, err)
		output, ok := result.(codeexecutor.CodeExecutionResult)
		require.True(t, ok)
		assert.Equal(t, "wrapped", output.Output)
	})

	t.Run("malformed code_blocks array", func(t *testing.T) {
		exec := &mockCodeExecutor{}
		ct := NewTool(exec)

		// code_blocks starts with '[' but contains invalid JSON.
		args := []byte(`{"code_blocks":[invalid]}`)

		_, err := ct.Call(context.Background(), args)
		require.Error(t, err)
	})
}

func Test_unmarshalCodeBlocks(t *testing.T) {
	t.Run("normal array", func(t *testing.T) {
		raw := json.RawMessage(`[{"language":"python","code":"1+1"}]`)
		blocks, err := unmarshalCodeBlocks(raw)
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		assert.Equal(t, "python", blocks[0].Language)
		assert.Equal(t, "1+1", blocks[0].Code)
	})

	t.Run("single object", func(t *testing.T) {
		raw := json.RawMessage(`{"language":"bash","code":"echo hi"}`)
		blocks, err := unmarshalCodeBlocks(raw)
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		assert.Equal(t, "bash", blocks[0].Language)
	})

	t.Run("double-encoded string containing array", func(t *testing.T) {
		inner, _ := json.Marshal([]codeexecutor.CodeBlock{{Language: "python", Code: "x"}})
		raw, _ := json.Marshal(string(inner)) // string → "\"[...]\""
		blocks, err := unmarshalCodeBlocks(raw)
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		assert.Equal(t, "python", blocks[0].Language)
	})

	t.Run("double-encoded string containing single object", func(t *testing.T) {
		inner, _ := json.Marshal(codeexecutor.CodeBlock{Language: "python", Code: "y"})
		raw, _ := json.Marshal(string(inner))
		blocks, err := unmarshalCodeBlocks(raw)
		require.NoError(t, err)
		require.Len(t, blocks, 1)
		assert.Equal(t, "python", blocks[0].Language)
	})

	t.Run("empty input", func(t *testing.T) {
		blocks, err := unmarshalCodeBlocks(nil)
		require.NoError(t, err)
		assert.Nil(t, blocks)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := unmarshalCodeBlocks(json.RawMessage(`not json`))
		require.Error(t, err)
	})

	t.Run("unexpected type (number)", func(t *testing.T) {
		_, err := unmarshalCodeBlocks(json.RawMessage(`42`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected array, object, or string")
	})

	t.Run("string containing invalid JSON", func(t *testing.T) {
		raw, _ := json.Marshal("not valid json")
		_, err := unmarshalCodeBlocks(raw)
		require.Error(t, err)
	})

	t.Run("array with non-object element", func(t *testing.T) {
		_, err := unmarshalCodeBlocks(json.RawMessage(`[42]`))
		require.Error(t, err)
	})

	t.Run("object with invalid field type", func(t *testing.T) {
		_, err := unmarshalCodeBlocks(json.RawMessage(`{"language":[1,2,3],"code":"x"}`))
		require.Error(t, err)
	})

	t.Run("whitespace before value", func(t *testing.T) {
		raw := json.RawMessage(`  [{"language":"python","code":"1"}]`)
		blocks, err := unmarshalCodeBlocks(raw)
		require.NoError(t, err)
		require.Len(t, blocks, 1)
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
		assert.Contains(t, decl.InputSchema.Required, "code_blocks")
	})

	t.Run("language property has enum", func(t *testing.T) {
		cbSchema := decl.InputSchema.Properties["code_blocks"]
		require.NotNil(t, cbSchema)
		require.NotNil(t, cbSchema.Items)
		langProp := cbSchema.Items.Properties["language"]
		assert.NotNil(t, langProp)
		assert.NotEmpty(t, langProp.Enum)
	})

	t.Run("code_blocks items has code property", func(t *testing.T) {
		cbSchema := decl.InputSchema.Properties["code_blocks"]
		require.NotNil(t, cbSchema)
		require.NotNil(t, cbSchema.Items)
		codeProp := cbSchema.Items.Properties["code"]
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
