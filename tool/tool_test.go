//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestStreamableTool_Interface(t *testing.T) {
	// Compile-time check
	var _ StreamableTool = (*testStreamableTool)(nil)
}

type testStreamableTool struct{}

func (d *testStreamableTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error) {
	s := NewStream(1)
	go func() {
		defer s.Writer.Close()
		s.Writer.Send(StreamChunk{Content: "test", Metadata: Metadata{CreatedAt: time.Now()}}, nil)
		s.Writer.Send(StreamChunk{Content: "more data"}, nil)
		s.Writer.Send(StreamChunk{Content: "final chunk"}, nil)

	}()
	return s.Reader, nil
}
func (d *testStreamableTool) Declaration() *Declaration {
	return &Declaration{
		Name:        "TestStreamableTool",
		Description: "A test tool for streaming data.",
		InputSchema: &Schema{
			Type:        "object",
			Properties:  map[string]*Schema{"input": {Type: "string"}},
			Required:    []string{"input"},
			Description: "Input for the test streamable tool.",
		},
	}
}

func TestSchemaPatternJSON(t *testing.T) {
	schema := &Schema{Type: "string", Pattern: "^[a-z0-9_-]+$"}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if string(data) != `{"type":"string","pattern":"^[a-z0-9_-]+$"}` {
		t.Fatalf("unexpected schema JSON: %s", string(data))
	}

	var roundTrip Schema
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if roundTrip.Pattern != schema.Pattern {
		t.Fatalf("pattern = %q, want %q", roundTrip.Pattern, schema.Pattern)
	}
}
