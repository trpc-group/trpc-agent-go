package codeexecutor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type CodeExecutor interface {
	ExecuteCode(context.Context, CodeExecutionInput) (CodeExecutionResult, error)
	CodeBlockDelimiter() CodeBlockDelimiter
}

type CodeExecutionInput struct {
	CodeBlocks  []CodeBlock
	InputFiles  []File
	ExecutionID string
}

type CodeExecutionResult struct {
	Stdout      string
	Stderr      string
	OutputFiles []File
}

type File struct {
	Name     string
	Content  string
	MIMEType string
}

type CodeBlock struct {
	Code     string
	Language string
}

type CodeBlockDelimiter struct {
	Start string
	End   string
}

// ExtractCodeBlock extracts code blocks from the input string using regex.
// It returns a slice of CodeBlock containing the extracted code and language
// example:
// input: "```python\nprint('Hello, World!')```"
// output: []CodeBlock{{Code: "print('Hello, World!')", Language: "python"}}
// TODO: Consdier moving to internal/codeexecutor
func ExtractCodeBlock(input string, delimiter CodeBlockDelimiter) []CodeBlock {
	var blocks []CodeBlock

	// Escape special regex characters in delimiters
	startDelim := regexp.QuoteMeta(delimiter.Start)
	endDelim := regexp.QuoteMeta(delimiter.End)

	// Pattern to match code blocks with optional language specification
	// More explicit pattern to handle the newline after language correctly
	pattern := regexp.MustCompile(`(?s)` + startDelim + `([^\n]*)\n(.*?)` + endDelim)

	matches := pattern.FindAllStringSubmatch(input, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			// Extract language from the first line (everything after the start delimiter)
			language := strings.TrimSpace(match[1])
			code := match[2]
			blocks = append(blocks, CodeBlock{
				Code:     code,
				Language: language,
			})
		}
	}

	return blocks
}

// BuildCodeExecutionResult formats the code execution result into a human-readable string.
// TODO: Consdier moving to internal/codeexecutor
func BuildCodeExecutionResult(codeExecutionResult CodeExecutionResult) string {
	if codeExecutionResult.Stderr != "" {
		return fmt.Sprintf("Code execution result:\n%s\n", codeExecutionResult.Stderr)
	}
	if codeExecutionResult.Stdout != "" && len(codeExecutionResult.OutputFiles) == 0 {
		return fmt.Sprintf("Code execution result:\n%s\n", codeExecutionResult.Stdout)
	}
	if len(codeExecutionResult.OutputFiles) != 0 {
		var filesNames []string
		for _, file := range codeExecutionResult.OutputFiles {
			filesNames = append(filesNames, file.Name)
		}

		return "Code execution result:\n Saved artifacts:\n" + strings.Join(filesNames, "\n")
	}

	return "Code execution result: No output or errors."
}
