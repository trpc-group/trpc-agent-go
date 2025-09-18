//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import "reflect"

// MessagesEqual reports whether two Message values are semantically equal.
// It compares primitive fields directly and performs deep equality checks for
// composite structures such as ContentParts and ToolCalls.
func MessagesEqual(a, b Message) bool {
	if a.Role != b.Role {
		return false
	}
	if a.Content != b.Content {
		return false
	}
	if a.ToolID != b.ToolID {
		return false
	}
	if a.ToolName != b.ToolName {
		return false
	}
	if a.ReasoningContent != b.ReasoningContent {
		return false
	}
	if !reflect.DeepEqual(a.ContentParts, b.ContentParts) {
		return false
	}
	if !reflect.DeepEqual(a.ToolCalls, b.ToolCalls) {
		return false
	}
	return true
}
