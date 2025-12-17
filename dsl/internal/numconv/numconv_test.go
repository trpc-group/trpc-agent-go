//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package numconv

import (
	"encoding/json"
	"math"
	"testing"
)

func TestInt(t *testing.T) {
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
			got, err := Int(tt.value, "max_tokens")
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

func TestInt_TooLarge(t *testing.T) {
	maxInt, _ := intBounds()

	_, err := Int(uint64(maxInt)+1, "max_tokens")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestIntTrunc(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		want      int
		wantError bool
	}{
		{name: "float64_fraction", value: float64(12.9), want: 12},
		{name: "float64_negative", value: float64(-12.9), want: -12},
		{name: "json_number_fraction", value: json.Number("12.9"), want: 12},
		{name: "string", value: "12", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IntTrunc(tt.value, "timeout")
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

func TestFloat64(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		want      float64
		wantError bool
	}{
		{name: "float64", value: float64(0.7), want: 0.7},
		{name: "float32", value: float32(0.7), want: 0.7},
		{name: "int32", value: int32(1), want: 1},
		{name: "uint32", value: uint32(1), want: 1},
		{name: "json_number_float", value: json.Number("0.7"), want: 0.7},
		{name: "json_number_int", value: json.Number("1"), want: 1},
		{name: "nan", value: math.NaN(), wantError: true},
		{name: "inf", value: math.Inf(1), wantError: true},
		{name: "string", value: "0.7", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Float64(tt.value, "temperature")
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-6 {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
