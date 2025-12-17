//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// FormatResultsComponent formats the results from code executions.
type FormatResultsComponent struct{}

func (c *FormatResultsComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "custom.format_results",
		DisplayName: "Format Results",
		Description: "Formats the final results from all code executions",
		Category:    "custom",
		Inputs: []registry.ParameterSchema{
			{
				Name:        "python_output",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Output from Python code execution",
			},
			{
				Name:        "bash_output",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Output from Bash code execution",
			},
		},
		Outputs: []registry.ParameterSchema{
			{
				Name:        "final_result",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Description: "Formatted final result",
			},
		},
	}
}

func (c *FormatResultsComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	pythonOutput, _ := state["python_output"].(string)
	bashOutput, _ := state["bash_output"].(string)

	result := fmt.Sprintf(`
╔════════════════════════════════════════════════════════════════╗
║           Code Execution Workflow Results                      ║
╚════════════════════════════════════════════════════════════════╝

📊 Python Analysis Results:
%s

🖥️  System Information:
%s

✅ Workflow completed successfully!
`, pythonOutput, bashOutput)

	return graph.State{
		"final_result": result,
	}, nil
}

func (c *FormatResultsComponent) Validate(config registry.ComponentConfig) error {
	return nil
}

