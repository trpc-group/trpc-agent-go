package codeexecutor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// CodeExecutor is an interface for executing code blocks in different environments.
type CodeExecutor interface {
	// ExecuteCode executes the code blocks provided in the input and returns the result.
	ExecuteCode(context.Context, CodeExecutionInput) (CodeExecutionResult, error)
	// CodeBlockDelimiter returns the delimiters used for code blocks.
	CodeBlockDelimiter() CodeBlockDelimiter
}

// CodeExecutionInput represents the input for code execution, containing code blocks and an execution ID.
type CodeExecutionInput struct {
	CodeBlocks  []CodeBlock
	ExecutionID string
}

// CodeExecutionResult represents the result of code execution, including output and any generated files.
type CodeExecutionResult struct {
	Output      string
	OutputFiles []File
}

// String implements the Stringer interface, formatting the code execution result into a human-readable string.
func (r CodeExecutionResult) String() string {
	if r.Output != "" && len(r.OutputFiles) == 0 {
		return fmt.Sprintf("Code execution result:\n%s\n", r.Output)
	}
	if len(r.OutputFiles) != 0 {
		var filesNames []string
		for _, file := range r.OutputFiles {
			filesNames = append(filesNames, file.Name)
		}

		return "Code execution result:\n Saved artifacts:\n" + strings.Join(filesNames, "\n")
	}

	return "Code execution result: No output or errors."
}

// File represents a file generated during code execution.
type File struct {
	Name     string
	Content  string
	MIMEType string
}

// CodeBlock represents a single block of code to be executed.
type CodeBlock struct {
	Code     string
	Language string
}

// CodeBlockDelimiter defines the start and end delimiters for code blocks.
type CodeBlockDelimiter struct {
	Start string
	End   string
}

// ExtractCodeBlock extracts code blocks from the input string using regex.
// It returns a slice of CodeBlock containing the extracted code and language
// example:
// input: "```python\nprint('Hello, World!')```", delimiter: CodeBlockDelimiter{Start: "```", End: "```"}
// output: []CodeBlock{{Code: "print('Hello, World!')", Language: "python"}}
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
