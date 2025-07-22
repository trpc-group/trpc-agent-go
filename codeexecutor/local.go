package codeexecutor

import "context"

// LocalCodeExecutor that unsafely execute code in the current local context
type LocalCodeExecutor struct {
}

// ExecuteCode executes the code in the local environment and returns the result.
func (l *LocalCodeExecutor) ExecuteCode(ctx context.Context, input CodeExecutionInput) (CodeExecutionResult, error) {
	// Implementation for local code execution
	return CodeExecutionResult{}, nil
}

// CodeBlockDelimiter returns the code block delimiter used by the local executor.
func (l *LocalCodeExecutor) CodeBlockDelimiter() CodeBlockDelimiter {
	return CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}
