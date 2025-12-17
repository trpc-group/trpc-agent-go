package outputformat

import "testing"

func TestParse_Strict(t *testing.T) {
	_, err := Parse(nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	// Default type when omitted.
	spec, err := Parse(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Type != TypeText {
		t.Fatalf("got type %q, want %q", spec.Type, TypeText)
	}

	// JSON type + schema.
	schema := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}
	spec, err = Parse(map[string]any{
		"type":   "json",
		"schema": schema,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Type != TypeJSON {
		t.Fatalf("got type %q, want %q", spec.Type, TypeJSON)
	}
	if spec.Schema == nil {
		t.Fatalf("expected schema, got nil")
	}

	// Invalid type.
	_, err = Parse(map[string]any{"type": "xml"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	// Invalid schema type.
	_, err = Parse(map[string]any{"type": "json", "schema": "bad"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestParseOptional_AndStructuredSchema(t *testing.T) {
	spec, err := ParseOptional(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Type != TypeText {
		t.Fatalf("got type %q, want %q", spec.Type, TypeText)
	}

	if StructuredSchema(nil) != nil {
		t.Fatalf("expected nil schema")
	}

	schema := map[string]any{"type": "object"}
	if StructuredSchema(map[string]any{"type": "text", "schema": schema}) != nil {
		t.Fatalf("expected nil schema when type=text")
	}

	got := StructuredSchema(map[string]any{"type": "json", "schema": schema})
	if got == nil {
		t.Fatalf("expected schema, got nil")
	}
}
