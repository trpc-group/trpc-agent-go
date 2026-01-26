//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package small provides a small-scale tool library (10 tools) for testing tool search functionality.
package small

import (
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// GetTools returns a slice of 10 commonly used function tools.
func GetTools() []tool.Tool {
	return []tool.Tool{
		NewCalculatorTool(),
		NewTimeTool(),
		NewTextTool(),
		NewCurrencyConverterTool(),
		NewUnitConverterTool(),
		NewPasswordGeneratorTool(),
		NewHashGeneratorTool(),
		NewBase64ConverterTool(),
		NewEmailValidatorTool(),
		NewRandomGeneratorTool(),
	}
}
