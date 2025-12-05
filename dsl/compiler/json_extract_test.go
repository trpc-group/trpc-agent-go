package compiler

import "testing"

func TestExtractFirstJSONObjectFromText_Success(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantJSON string
	}{
		{
			name:     "simple_object",
			input:    `{"key":"value"}`,
			wantJSON: `{"key":"value"}`,
		},
		{
			name:     "leading_trailing_noise",
			input:    `prefix {"key": 1} suffix`,
			wantJSON: `{"key": 1}`,
		},
		{
			name:     "nested_object_and_array",
			input:    `xx {"a":{"b":[1,2,3]}} yy`,
			wantJSON: `{"a":{"b":[1,2,3]}}`,
		},
		{
			name:     "array_root",
			input:    `some text [1, {"a": "b"} , 3] other`,
			wantJSON: `[1, {"a": "b"} , 3]`,
		},
		{
			name:     "string_with_closing_brace",
			input:    `noise {"text": "value with } brace"} end`,
			wantJSON: `{"text": "value with } brace"}`,
		},
		{
			name:     "string_with_escaped_quote_and_brace",
			input:    `xx {"text": "he said \"{ ok }\""} yy`,
			wantJSON: `{"text": "he said \"{ ok }\""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractFirstJSONObjectFromText(tt.input)
			if !ok {
				t.Fatalf("extractFirstJSONObjectFromText(%q) returned ok=false", tt.input)
			}
			if got != tt.wantJSON {
				t.Fatalf("extractFirstJSONObjectFromText(%q) = %q, want %q", tt.input, got, tt.wantJSON)
			}
		})
	}
}

func TestExtractFirstJSONObjectFromText_Failure(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "no_json",
			input: "just plain text",
		},
		{
			name:  "unbalanced_object",
			input: `prefix {"key": 1`,
		},
		{
			name:  "unbalanced_array",
			input: `xx [1, 2, 3 `,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := extractFirstJSONObjectFromText(tt.input); ok {
				t.Fatalf("extractFirstJSONObjectFromText(%q) = %q, want ok=false", tt.input, got)
			}
		})
	}
}
