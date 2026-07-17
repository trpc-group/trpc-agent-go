//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package resultcodec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSON_BackwardCompatibleWithMarshal(t *testing.T) {
	ctx := context.Background()
	c := JSON()
	cases := []struct {
		name   string
		result any
	}{
		{"string", "success"},
		{"map", map[string]any{"message": "done", "count": 42}},
		{"nil", nil},
		{"bool", true},
		{"slice", []int{1, 2, 3}},
		{"nested", map[string]any{"todos": []map[string]string{
			{"content": "step 1", "status": "completed"},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Encode(ctx, tc.result)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			// For inputs without <, >, & the codec must match json.Marshal
			// byte-for-byte, preserving legacy behavior.
			want, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatalf("json.Marshal error: %v", err)
			}
			if got != string(want) {
				t.Errorf("got %q, want %q", got, string(want))
			}
		})
	}
}

func TestJSON_DoesNotHTMLEscape(t *testing.T) {
	got, err := JSON().Encode(
		context.Background(),
		map[string]string{"code": "a <-b && c > d"},
	)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if !strings.Contains(got, "a <-b && c > d") {
		t.Errorf("special characters must be preserved verbatim: %q", got)
	}
	for _, esc := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(got, esc) {
			t.Errorf("must not HTML-escape (%s): %q", esc, got)
		}
	}
}

func TestJSON_Deterministic(t *testing.T) {
	ctx := context.Background()
	in := map[string]any{"b": 2, "a": 1, "c": 3}
	first, err := JSON().Encode(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, err := JSON().Encode(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("non-deterministic output: %q vs %q", got, first)
		}
	}
}

func TestJSON_ErrorOnNonSerializable(t *testing.T) {
	if _, err := JSON().Encode(context.Background(), make(chan int)); err == nil {
		t.Fatal("expected error for non-serializable value")
	}
}
