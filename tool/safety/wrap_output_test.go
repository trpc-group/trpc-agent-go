//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestWrappedCallableRedactsStructuredToolOutput(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	wrapped, err := WrapCallableTool(secretOutputTool{}, guard)
	if err != nil {
		t.Fatalf("WrapCallableTool() error = %v", err)
	}
	result, err := wrapped.Call(context.Background(), []byte(
		`{"command":"go test ./tool/safety","timeout_sec":10}`,
	))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, secret := range []string{"database-output-secret", "nested-output-token"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("wrapped tool output leaked %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), RedactedValue) {
		t.Fatalf("wrapped tool output has no redaction marker: %s", encoded)
	}
}

type secretOutputTool struct{}

func (secretOutputTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "workspace_exec"}
}

func (secretOutputTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{OpenWorld: true, MaxResultSize: 4096}
}

func (secretOutputTool) Call(context.Context, []byte) (any, error) {
	return map[string]any{
		"DB_PASSWORD": "database-output-secret",
		"nested": map[string]any{
			"access_token": "nested-output-token",
		},
		"safe": "visible",
	}, nil
}
