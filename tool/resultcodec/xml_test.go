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
	"testing"
)

type xmlBashResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

func TestXML_GoldenOutputs(t *testing.T) {
	ctx := context.Background()
	c := XML()
	cases := []struct {
		name   string
		result any
		want   string
	}{
		{"string scalar", "hi", "<result>hi</result>"},
		{"int scalar", 42, "<result>42</result>"},
		{"float scalar", 3.5, "<result>3.5</result>"},
		{"bool scalar", true, "<result>true</result>"},
		{"nil", nil, "<result></result>"},
		{
			"map sorts keys",
			map[string]any{"b": 1, "a": "x"},
			"<result><a>x</a><b>1</b></result>",
		},
		{
			"array wraps items",
			[]any{1, "two", true},
			"<result><item>1</item><item>two</item><item>true</item></result>",
		},
		{
			"struct",
			xmlBashResult{ExitCode: 0, Output: "ok"},
			"<result><exit_code>0</exit_code><output>ok</output></result>",
		},
		{
			"nested",
			map[string]any{"list": []any{map[string]any{"x": 1}}},
			"<result><list><item><x>1</x></item></list></result>",
		},
		{
			"empty map",
			map[string]any{},
			"<result></result>",
		},
		{
			"empty array",
			[]any{},
			"<result></result>",
		},
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

func TestXML_EscapesSpecialCharacters(t *testing.T) {
	got, err := XML().Encode(
		context.Background(),
		map[string]any{"k": `a<b>&"c"`},
	)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	want := `<result><k>a&lt;b&gt;&amp;&#34;c&#34;</k></result>`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestXML_InvalidKeyNamesUseItemElement(t *testing.T) {
	got, err := XML().Encode(
		context.Background(),
		map[string]any{"has space": "v", "1num": "n"},
	)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	want := `<result><item key="1num">n</item><item key="has space">v</item></result>`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestXML_UnicodePreserved(t *testing.T) {
	got, err := XML().Encode(
		context.Background(),
		map[string]any{"msg": "你好 😀"},
	)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	want := "<result><msg>你好 😀</msg></result>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestXML_Deterministic(t *testing.T) {
	ctx := context.Background()
	in := map[string]any{"z": 1, "a": 2, "m": 3}
	first, err := XML().Encode(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, err := XML().Encode(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if got != first {
			t.Fatalf("non-deterministic output: %q vs %q", got, first)
		}
	}
}

func TestXML_ErrorOnNonSerializable(t *testing.T) {
	if _, err := XML().Encode(context.Background(), make(chan int)); err == nil {
		t.Fatal("expected error for non-serializable value")
	}
}
