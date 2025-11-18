//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package builtin

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	// Auto-register Code component at package init time
	registry.MustRegister(&CodeComponent{})
}

// CodeComponent is a builtin component for executing code in various languages.
// It supports Python, JavaScript, and Bash code execution with configurable
// execution modes (local or container).
//
// Configuration:
//   - code: string (required) - The code to execute
//   - language: string (required) - Programming language (python, javascript, bash)
//   - executor_type: string (optional) - Execution mode: "local" or "container" (default: "local")
//   - timeout: int (optional) - Execution timeout in seconds (default: 30)
//   - work_dir: string (optional) - Working directory for code execution
//   - clean_temp_files: bool (optional) - Whether to clean temporary files after execution (default: true)
//
// Example DSL:
//
//	{
//	  "id": "data_processing",
//	  "component": {
//	    "type": "component",
//	    "ref": "builtin.code"
//	  },
//	  "config": {
//	    "code": "import statistics\ndata = [5, 12, 8, 15, 7, 9, 11]\nresult = statistics.mean(data)\nprint(f'Mean: {result}')",
//	    "language": "python",
//	    "executor_type": "local",
//	    "timeout": 30
//	  }
//	}
type CodeComponent struct{}

// Metadata returns the component metadata.
func (c *CodeComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.code",
		DisplayName: "Code Executor",
		Description: "Executes code in Python, JavaScript, or Bash with configurable isolation",
		Category:    "Code",
		Version:     "1.0.0",
		// Code executor does not consume named state inputs beyond the built-in
		// graph fields, so Inputs can remain empty. All parameters are provided
		// via config.
		Inputs: []registry.ParameterSchema{},
		Outputs: []registry.ParameterSchema{
			{
				Name:        "output",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Code execution output",
			},
			{
				Name:        "output_files",
				Type:        "[]codeexecutor.File",
				GoType:      reflect.TypeOf([]codeexecutor.File{}),
				Description: "Files generated during code execution",
			},
		},
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "code",
				DisplayName: "Code",
				Description: "The code to execute",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
				Placeholder: "print('hello from code executor')",
			},
			{
				Name:        "language",
				DisplayName: "Language",
				Description: "Programming language: python, javascript, or bash",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
				Placeholder: "python",
			},
			{
				Name:        "executor_type",
				DisplayName: "Executor Type",
				Description: "Execution mode: local or container (default: local)",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
				Default:     "local",
			},
			{
				Name:        "timeout",
				DisplayName: "Timeout (seconds)",
				Description: "Execution timeout in seconds (default: 30)",
				Type:        "int",
				TypeID:      "number",
				Kind:        "number",
				GoType:      reflect.TypeOf(0),
				Required:    false,
				Default:     30,
			},
			{
				Name:        "work_dir",
				DisplayName: "Working Directory",
				Description: "Working directory for code execution",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
			},
			{
				Name:        "clean_temp_files",
				DisplayName: "Clean Temporary Files",
				Description: "Whether to clean temporary files after execution",
				Type:        "bool",
				TypeID:      "boolean",
				Kind:        "boolean",
				GoType:      reflect.TypeOf(false),
				Required:    false,
				Default:     true,
			},
		},
	}
}

// Execute executes the code component.
func (c *CodeComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Extract code from config
	code, ok := config["code"].(string)
	if !ok || code == "" {
		return nil, fmt.Errorf("code is required and must be a non-empty string")
	}

	// Extract language from config
	language, ok := config["language"].(string)
	if !ok || language == "" {
		return nil, fmt.Errorf("language is required and must be a non-empty string")
	}

	// Validate language
	if language != "python" && language != "javascript" && language != "bash" {
		return nil, fmt.Errorf("unsupported language: %s (supported: python, javascript, bash)", language)
	}

	// Extract executor type (default: local)
	executorType := "local"
	if et, ok := config["executor_type"].(string); ok && et != "" {
		executorType = et
	}

	// Extract timeout (default: 30 seconds)
	timeout := 30
	if t, ok := config["timeout"].(int); ok && t > 0 {
		timeout = t
	} else if t, ok := config["timeout"].(float64); ok && t > 0 {
		timeout = int(t)
	}

	// Extract work_dir (optional)
	workDir := ""
	if wd, ok := config["work_dir"].(string); ok {
		workDir = wd
	}

	// Extract clean_temp_files (default: true)
	cleanTempFiles := true
	if ctf, ok := config["clean_temp_files"].(bool); ok {
		cleanTempFiles = ctf
	}

	// Create executor based on type
	var executor codeexecutor.CodeExecutor
	switch executorType {
	case "local":
		executor = local.New(
			local.WithTimeout(time.Duration(timeout)*time.Second),
			local.WithWorkDir(workDir),
			local.WithCleanTempFiles(cleanTempFiles),
		)
	case "container":
		// TODO: Implement container executor when needed
		// For now, fall back to local
		return nil, fmt.Errorf("container executor not yet implemented, use 'local' for now")
	default:
		return nil, fmt.Errorf("unsupported executor_type: %s (supported: local, container)", executorType)
	}

	// Prepare code execution input
	input := codeexecutor.CodeExecutionInput{
		CodeBlocks: []codeexecutor.CodeBlock{
			{
				Code:     code,
				Language: language,
			},
		},
		ExecutionID: fmt.Sprintf("code-%d", time.Now().UnixNano()),
	}

	// Execute code
	result, err := executor.ExecuteCode(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("code execution failed: %w", err)
	}

	// Return result as state update
	return graph.State{
		"output":       result.Output,
		"output_files": result.OutputFiles,
	}, nil
}

// Validate validates the component configuration.
func (c *CodeComponent) Validate(config registry.ComponentConfig) error {
	// Validate code
	code, ok := config["code"].(string)
	if !ok || code == "" {
		return fmt.Errorf("code is required and must be a non-empty string")
	}

	// Validate language
	language, ok := config["language"].(string)
	if !ok || language == "" {
		return fmt.Errorf("language is required and must be a non-empty string")
	}

	if language != "python" && language != "javascript" && language != "bash" {
		return fmt.Errorf("unsupported language: %s (supported: python, javascript, bash)", language)
	}

	// Validate executor_type if provided
	if executorType, ok := config["executor_type"].(string); ok && executorType != "" {
		if executorType != "local" && executorType != "container" {
			return fmt.Errorf("unsupported executor_type: %s (supported: local, container)", executorType)
		}
	}

	// Validate timeout if provided
	if timeout, ok := config["timeout"].(int); ok && timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got: %d", timeout)
	}

	return nil
}
