//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import agenttool "trpc.group/trpc-go/trpc-agent-go/tool"

// NewTools returns the full tool set used by the LLMAgent external-tool example.
func NewTools() []agenttool.Tool {
	return []agenttool.Tool{
		newCalculatorTool(),
		newInternalLookupTool(),
		newExternalNoteTool(),
		newExternalApprovalTool(),
	}
}
