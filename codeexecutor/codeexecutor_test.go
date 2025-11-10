//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestExtractCodeBlock(t *testing.T) {
	delimiter := codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}

	tests := []struct {
		name      string
		input     string
		delimiter codeexecutor.CodeBlockDelimiter
		expected  []codeexecutor.CodeBlock
	}{
		{
			name:      "single python block",
			input:     "```python\nprint('Hello, World!')\n```",
			delimiter: delimiter,
			expected: []codeexecutor.CodeBlock{
				{Code: "print('Hello, World!')\n", Language: "python"},
			},
		},
		{
			name:      "multiple blocks with different languages",
			input:     "```go\nfmt.Println(\"hi\")\n```\nSome text\n```js\nconsole.log('hi')\n```",
			delimiter: delimiter,
			expected: []codeexecutor.CodeBlock{
				{Code: "fmt.Println(\"hi\")\n", Language: "go"},
				{Code: "console.log('hi')\n", Language: "js"},
			},
		},
		{
			name:      "block with no language",
			input:     "```\nno language here\n```",
			delimiter: delimiter,
			expected: []codeexecutor.CodeBlock{
				{Code: "no language here\n", Language: ""},
			},
		},
		{
			name:      "block with spaces before language",
			input:     "```   python\nprint('test')\n```",
			delimiter: delimiter,
			expected: []codeexecutor.CodeBlock{
				{Code: "print('test')\n", Language: "python"},
			},
		},
		{
			name:      "no code block",
			input:     "This is just text.",
			delimiter: delimiter,
			expected:  nil,
		},
		{
			name:      "custom delimiter",
			input:     "<code>ruby\nputs 'hi'\n</code>",
			delimiter: codeexecutor.CodeBlockDelimiter{Start: "<code>", End: "</code>"},
			expected: []codeexecutor.CodeBlock{
				{Code: "puts 'hi'\n", Language: "ruby"},
			},
		},
		{
			name:      "empty input",
			input:     "",
			delimiter: delimiter,
			expected:  nil,
		},
		{
			name:      "block with empty code",
			input:     "```go\n\n```",
			delimiter: delimiter,
			expected: []codeexecutor.CodeBlock{
				{Code: "\n", Language: "go"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := codeexecutor.ExtractCodeBlock(tt.input, tt.delimiter)
			assert.Equal(t, tt.expected, blocks)
		})
	}
}

func TestCodeExecutionResultString(t *testing.T) {
	tests := []struct {
		name     string
		result   codeexecutor.CodeExecutionResult
		expected string
	}{
		{
			name: "only output",
			result: codeexecutor.CodeExecutionResult{
				Output: "hello world",
			},
			expected: "Code execution result:\nhello world\n",
		},
		{
			name: "with files",
			result: codeexecutor.CodeExecutionResult{
				OutputFiles: []codeexecutor.File{
					{Name: "foo.txt"},
					{Name: "bar.log"},
				},
			},
			expected: "Code execution result:\n Saved output files:\nfoo.txt\nbar.log",
		},
		{
			name:     "empty result",
			result:   codeexecutor.CodeExecutionResult{},
			expected: "Code execution result: No output or errors.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.String())
		})
	}
}
