//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

// IsExternalTool reports whether name is handled by the graph external interrupt node.
func IsExternalTool(name string) bool {
	switch name {
	case ExternalSearchName, ExternalApprovalName:
		return true
	default:
		return false
	}
}
