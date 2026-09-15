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

func TestSchemaNumericBoundsJSON(t *testing.T) {
	schema := &Schema{
		Type:             "number",
		Minimum:          json.Number("1.5"),
		Maximum:          json.Number("10"),
		ExclusiveMinimum: json.Number("1"),
		ExclusiveMaximum: json.Number("11"),
	}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	want := `{"type":"number","minimum":1.5,"maximum":10,"exclusiveMinimum":1,"exclusiveMaximum":11}`
	if string(data) != want {
		t.Fatalf("schema JSON = %s, want %s", string(data), want)
	}

	var roundTrip Schema
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if roundTrip.Minimum != schema.Minimum ||
		roundTrip.Maximum != schema.Maximum ||
		roundTrip.ExclusiveMinimum != schema.ExclusiveMinimum ||
		roundTrip.ExclusiveMaximum != schema.ExclusiveMaximum {
		t.Fatalf("numeric bounds changed after round trip: got %+v, want %+v", roundTrip, schema)
	}
}

func TestSchemaUnmarshal_Draft4BooleanExclusiveIgnored(t *testing.T) {
	const payload = `{
		"type": "integer",
		"minimum": 0,
		"exclusiveMinimum": true,
		"maximum": 10,
		"exclusiveMaximum": false
	}`
	var schema Schema
	if err := json.Unmarshal([]byte(payload), &schema); err != nil {
		t.Fatalf("unmarshal draft-4 exclusive bounds: %v", err)
	}
	if schema.Type != "integer" {
		t.Fatalf("type = %q, want integer", schema.Type)
	}
	if schema.Minimum != json.Number("0") {
		t.Fatalf("minimum = %q, want 0", schema.Minimum)
	}
	if schema.Maximum != json.Number("10") {
		t.Fatalf("maximum = %q, want 10", schema.Maximum)
	}
	if schema.ExclusiveMinimum != "" || schema.ExclusiveMaximum != "" {
		t.Fatalf("boolean exclusive bounds should be ignored, got exclusiveMinimum=%q exclusiveMaximum=%q",
			schema.ExclusiveMinimum, schema.ExclusiveMaximum)
	}
}

func TestSchemaUnmarshal_NestedDraft4BooleanExclusiveIgnored(t *testing.T) {
	const payload = `{
		"type": "object",
		"properties": {
			"page_size": {
				"type": "integer",
				"minimum": 1,
				"exclusiveMinimum": true
			}
		}
	}`
	var schema Schema
	if err := json.Unmarshal([]byte(payload), &schema); err != nil {
		t.Fatalf("unmarshal nested draft-4 exclusive bounds: %v", err)
	}
	pageSize := schema.Properties["page_size"]
	if pageSize == nil {
		t.Fatal("expected page_size property")
	}
	if pageSize.Minimum != json.Number("1") {
		t.Fatalf("minimum = %q, want 1", pageSize.Minimum)
	}
	if pageSize.ExclusiveMinimum != "" {
		t.Fatalf("boolean exclusiveMinimum should be ignored, got %q", pageSize.ExclusiveMinimum)
	}
}
