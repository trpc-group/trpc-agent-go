//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"context"
	"testing"
)

func TestToolNameModeOf_DefaultsToQualified(t *testing.T) {
	if got := ToolNameModeOf(&defaultNameModeMockToolSet{}); got != ToolNameModeQualified {
		t.Fatalf("ToolNameModeOf() = %v, want %v", got, ToolNameModeQualified)
	}
	if got := ToolNameModeOf(nil); got != ToolNameModeQualified {
		t.Fatalf("ToolNameModeOf(nil) = %v, want %v", got, ToolNameModeQualified)
	}
}

type originalNameMockToolSet struct {
	defaultNameModeMockToolSet
}

func (s *originalNameMockToolSet) ToolNameMode() ToolNameMode {
	return ToolNameModeOriginal
}

func TestToolNameModeOf_UsesOptionalProvider(t *testing.T) {
	set := &originalNameMockToolSet{}
	if got := ToolNameModeOf(set); got != ToolNameModeOriginal {
		t.Fatalf("ToolNameModeOf() = %v, want %v", got, ToolNameModeOriginal)
	}
}

type defaultNameModeMockToolSet struct{}

func (defaultNameModeMockToolSet) Tools(context.Context) []Tool { return nil }

func (defaultNameModeMockToolSet) Close() error { return nil }

func (defaultNameModeMockToolSet) Name() string { return "mock" }
