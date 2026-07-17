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
	"testing"
	"time"
)

type textStatus string

func TestText_TextTypes(t *testing.T) {
	ctx := context.Background()
	c := Text()
	cases := []struct {
		name   string
		result any
		want   string
	}{
		{"string", "hello <b>", "hello <b>"},
		{"empty string", "", ""},
		{"nil", nil, ""},
		{"bytes", []byte("raw bytes"), "raw bytes"},
		{"raw message", json.RawMessage(`{"a":1}`), `{"a":1}`},
		{"named string", textStatus("running"), "running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Encode(ctx, tc.result)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestText_TextMarshaler(t *testing.T) {
	ts := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	got, err := Text().Encode(context.Background(), ts)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	want, err := ts.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("got %q, want %q", got, string(want))
	}
}

func TestText_InvalidUTF8ReplacedWithU_FFFD(t *testing.T) {
	ctx := context.Background()
	// 0xff is an invalid UTF-8 byte and must be replaced with U+FFFD.
	invalid := "ok\xffend"
	want := "ok\uFFFDend"
	cases := []struct {
		name   string
		result any
	}{
		{"bytes", []byte(invalid)},
		{"raw message", json.RawMessage(invalid)},
		{"string", invalid},
		{"named string", textStatus(invalid)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Text().Encode(ctx, tc.result)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestText_ErrorOnStructuredResult(t *testing.T) {
	type payload struct {
		A int
	}
	cases := []any{
		payload{A: 1},
		map[string]any{"a": 1},
		[]int{1, 2, 3},
		42,
	}
	for _, in := range cases {
		if _, err := Text().Encode(context.Background(), in); err == nil {
			t.Errorf("expected error for structured result %T", in)
		}
	}
}
