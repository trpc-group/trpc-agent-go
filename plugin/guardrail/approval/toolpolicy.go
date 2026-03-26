//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import "fmt"

// ToolPolicy determines how a tool should be handled by approval.
type ToolPolicy string

const (
	// ToolPolicyRequireApproval requires reviewer approval before execution.
	ToolPolicyRequireApproval ToolPolicy = "require_approval"
	// ToolPolicySkipApproval skips reviewer approval and allows execution.
	ToolPolicySkipApproval ToolPolicy = "skip_approval"
	// ToolPolicyDenied blocks tool execution immediately.
	ToolPolicyDenied ToolPolicy = "denied"
)

func validateToolPolicy(policy ToolPolicy) error {
	switch policy {
	case ToolPolicyRequireApproval, ToolPolicySkipApproval, ToolPolicyDenied:
		return nil
	default:
		return fmt.Errorf("invalid tool policy %q", policy)
	}
}
