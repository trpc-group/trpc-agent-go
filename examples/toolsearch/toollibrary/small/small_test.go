//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package small

import (
	"testing"
)

func TestGetTools(t *testing.T) {
	tools := GetTools()
	if len(tools) != 10 {
		t.Errorf("Expected 10 tools, got %d", len(tools))
	}

	for i, tool := range tools {
		if tool == nil {
			t.Errorf("Tool at index %d is nil", i)
		}
		if tool.Declaration() == nil {
			t.Errorf("Tool at index %d has nil Declaration", i)
		}
		if tool.Declaration().Name == "" {
			t.Errorf("Tool at index %d has empty name", i)
		}
	}
}
