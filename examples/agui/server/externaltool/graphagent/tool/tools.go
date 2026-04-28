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

// NewInternalTools returns the tools executed by the graph.
func NewInternalTools() map[string]agenttool.Tool {
	return map[string]agenttool.Tool{
		InternalLookupName:  newInternalLookupTool(),
		InternalProfileName: newInternalProfileTool(),
	}
}

// NewExternalTools returns the tool declarations executed by the caller.
func NewExternalTools() map[string]agenttool.Tool {
	return map[string]agenttool.Tool{
		ExternalSearchName:   newExternalSearchTool(),
		ExternalApprovalName: newExternalApprovalTool(),
	}
}
