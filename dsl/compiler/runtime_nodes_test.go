package compiler

import (
	"encoding/json"
	"testing"
)

func TestCoerceConfigInt(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		want      int
		wantError bool
	}{
		{name: "int32", value: int32(512), want: 512},
		{name: "int64", value: int64(512), want: 512},
		{name: "uint32", value: uint32(512), want: 512},
		{name: "float64_int", value: float64(512), want: 512},
		{name: "float64_fraction", value: float64(512.5), wantError: true},
		{name: "json_number_int", value: json.Number("512"), want: 512},
		{name: "json_number_fraction", value: json.Number("512.5"), wantError: true},
		{name: "string", value: "512", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coerceConfigInt(tt.value, "max_tokens")
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}
