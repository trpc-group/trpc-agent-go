//
//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codeexecutor provides an interface and utilities for executing
// code blocks and running programs in workspaces.
package codeexecutor

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// CodeExecutor executes code blocks via a friendly front-door API.
type CodeExecutor interface {
	// ExecuteCode executes the code blocks provided in the input and
	// returns the result.
	ExecuteCode(context.Context, CodeExecutionInput) (CodeExecutionResult, error)
	// CodeBlockDelimiter returns the delimiters used for code blocks.
	CodeBlockDelimiter() CodeBlockDelimiter
}

// CodeExecutionInput is the input for code execution.
type CodeExecutionInput struct {
	CodeBlocks  []CodeBlock
	ExecutionID string
}

// CodeExecutionResult is the result of code execution including files.
type CodeExecutionResult struct {
	Output      string
	OutputFiles []File
}

// String formats a human-readable result.
func (r CodeExecutionResult) String() string {
	if r.Output != "" && len(r.OutputFiles) == 0 {
		return fmt.Sprintf("Code execution result:\n%s\n", r.Output)
	}
	if len(r.OutputFiles) != 0 {
		var filesNames []string
		for _, file := range r.OutputFiles {
			filesNames = append(filesNames, file.Name)
		}
		return "Code execution result:\n Saved output files:\n" +
			strings.Join(filesNames, "\n")
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

// ExtractCodeBlock extracts fenced code blocks using the given delimiter.
func ExtractCodeBlock(input string, delimiter CodeBlockDelimiter) []CodeBlock {
	var blocks []CodeBlock
	startDelim := regexp.QuoteMeta(delimiter.Start)
	endDelim := regexp.QuoteMeta(delimiter.End)
	pattern := regexp.MustCompile(`(?s)` + startDelim + `([^\n]*)\n(.*?)` + endDelim)
	matches := pattern.FindAllStringSubmatch(input, -1)
	for _, match := range matches {
		if len(match) >= 3 {
			language := strings.TrimSpace(match[1])
			code := match[2]
			blocks = append(blocks, CodeBlock{Code: code, Language: language})
		}
	}
	return blocks
}
