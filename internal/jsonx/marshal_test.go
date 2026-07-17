//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonx

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalNoHTMLEscape_PreservesSpecialChars(t *testing.T) {
	got, err := MarshalNoHTMLEscape(map[string]string{"code": "a <-b && c > d"})
	if err != nil {
		t.Fatalf("MarshalNoHTMLEscape error: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "a <-b && c > d") {
		t.Errorf("special characters must be preserved verbatim: %q", s)
	}
	for _, esc := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(s, esc) {
			t.Errorf("must not HTML-escape (%s): %q", esc, s)
		}
	}
}

func TestMarshalNoHTMLEscape_NoTrailingNewline(t *testing.T) {
	got, err := MarshalNoHTMLEscape(map[string]int{"a": 1})
	if err != nil {
		t.Fatalf("MarshalNoHTMLEscape error: %v", err)
	}
	if strings.HasSuffix(string(got), "\n") {
		t.Errorf("output must not end with a newline: %q", string(got))
	}
}

func TestMarshalNoHTMLEscape_MatchesMarshalForSafeInputs(t *testing.T) {
	inputs := []any{
		"success",
		map[string]any{"message": "done", "count": 42},
		nil,
		true,
		[]int{1, 2, 3},
	}
	for _, in := range inputs {
		got, err := MarshalNoHTMLEscape(in)
		if err != nil {
			t.Fatalf("MarshalNoHTMLEscape error: %v", err)
		}
		want, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("json.Marshal error: %v", err)
		}
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", string(got), string(want))
		}
	}
}

func TestMarshalNoHTMLEscape_ErrorOnNonSerializable(t *testing.T) {
	if _, err := MarshalNoHTMLEscape(make(chan int)); err == nil {
		t.Fatal("expected error for non-serializable value")
	}
}
