//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/file"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func releaseDocsToolSet(root string) (tool.ToolSet, error) {
	releaseDocs, err := file.NewToolSet(
		file.WithName(toolSetName),
		file.WithBaseDir(root),
		file.WithSaveFileEnabled(false),
		file.WithReplaceContentEnabled(false),
	)
	if err != nil {
		return nil, fmt.Errorf("create release docs tool set: %w", err)
	}
	return releaseDocs, nil
}

func parseActivationMode(raw string) (llmagent.ToolActivationMode, error) {
	switch llmagent.ToolActivationMode(strings.ToLower(strings.TrimSpace(raw))) {
	case llmagent.ToolActivationModeInclude:
		return llmagent.ToolActivationModeInclude, nil
	case llmagent.ToolActivationModeOnly:
		return llmagent.ToolActivationModeOnly, nil
	default:
		return "", fmt.Errorf("invalid -mode %q: want include|only", raw)
	}
}

func parseActivationLifetime(raw string) (llmagent.ToolActivationLifetime, error) {
	switch llmagent.ToolActivationLifetime(strings.ToLower(strings.TrimSpace(raw))) {
	case llmagent.ToolActivationLifetimeInvocation:
		return llmagent.ToolActivationLifetimeInvocation, nil
	case llmagent.ToolActivationLifetimeSession:
		return llmagent.ToolActivationLifetimeSession, nil
	default:
		return "", fmt.Errorf(
			"invalid -lifetime %q: want invocation|session",
			raw,
		)
	}
}

func calculatorTool() tool.Tool {
	type calculatorInput struct {
		Operation string  `json:"operation" description:"Operation to perform: add, subtract, multiply, or divide."`
		A         float64 `json:"a" description:"First operand."`
		B         float64 `json:"b" description:"Second operand."`
	}
	type calculatorOutput struct {
		Result float64 `json:"result"`
	}
	return function.NewFunctionTool(
		func(_ context.Context, in calculatorInput) (calculatorOutput, error) {
			switch strings.ToLower(strings.TrimSpace(in.Operation)) {
			case "add", "+":
				return calculatorOutput{Result: in.A + in.B}, nil
			case "subtract", "-":
				return calculatorOutput{Result: in.A - in.B}, nil
			case "multiply", "*":
				return calculatorOutput{Result: in.A * in.B}, nil
			case "divide", "/":
				if in.B == 0 {
					return calculatorOutput{}, fmt.Errorf("division by zero")
				}
				return calculatorOutput{Result: in.A / in.B}, nil
			default:
				return calculatorOutput{}, fmt.Errorf(
					"unsupported operation %q",
					in.Operation,
				)
			}
		},
		function.WithName("calculator"),
		function.WithDescription("Perform basic arithmetic."),
	)
}
