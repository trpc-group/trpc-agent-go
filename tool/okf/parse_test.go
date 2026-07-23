//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package okf

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestConceptID(t *testing.T) {
	cases := map[string]string{
		"tables/orders.md": "tables/orders",
		"/a/b.md":          "a/b",
		"note.md":          "note",
	}
	for in, want := range cases {
		if got := conceptID(in); got != want {
			t.Errorf("conceptID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitFrontmatter(t *testing.T) {
	fm, body := splitFrontmatter([]byte("---\ntype: Protocol\ntitle: x402\n---\n\n# Body\ntext"))
	if fm.Type != "Protocol" || fm.Title != "x402" {
		t.Errorf("frontmatter not parsed: %+v", fm)
	}
	if string(body) != "\n# Body\ntext" { // closing fence consumes one newline; blank line kept.
		t.Errorf("body = %q", body)
	}

	// CRLF frontmatter.
	fm, _ = splitFrontmatter([]byte("---\r\ntype: T\r\n---\r\nbody"))
	if fm.Type != "T" {
		t.Errorf("CRLF frontmatter not parsed: %+v", fm)
	}

	// Optional fields are decoded independently. In particular, scalar tags in
	// existing OKF bundles must not discard required or recommended metadata.
	fm, body = splitFrontmatter([]byte("---\ntype: BigQuery Dataset\ntitle: Stack Overflow\ndescription: [wrong, shape]\ntags: bigquery, dataset, stackoverflow\nowner:\n  team: data\n---\nbody"))
	if fm.Type != "BigQuery Dataset" || fm.Title != "Stack Overflow" {
		t.Errorf("optional field drift discarded metadata: %+v", fm)
	}
	if !reflect.DeepEqual(fm.Tags, []string{"bigquery", "dataset", "stackoverflow"}) {
		t.Errorf("scalar tags = %#v", fm.Tags)
	}
	if fm.Description != "" || fm.Extra["owner"] == nil || string(body) != "body" {
		t.Errorf("tolerant frontmatter = %+v body=%q", fm, body)
	}

	// No frontmatter: tolerant, whole input is body.
	fm, body = splitFrontmatter([]byte("# just markdown"))
	if fm.Type != "" || string(body) != "# just markdown" {
		t.Errorf("no-frontmatter case: fm=%+v body=%q", fm, body)
	}

	// Malformed YAML: tolerated, not rejected; body kept verbatim.
	raw := []byte("---\ntype: [unclosed\n---\nbody")
	fm, body = splitFrontmatter(raw)
	if fm.Type != "" || string(body) != string(raw) {
		t.Errorf("bad YAML should be tolerated: fm=%+v body=%q", fm, body)
	}
}

func TestSplitFrontmatter_FieldShapes(t *testing.T) {
	fm, body := splitFrontmatter([]byte("---\ntype: Protocol\ntitle: Full\ndescription: Summary\nresource: https://example.com/p\ntags: [one, 2, three]\ntimestamp: 2026-07-21T00:00:00Z\nokf_version: \"0.1\"\ncustom: true\n---\nbody"))
	if fm.Type != "Protocol" || fm.Title != "Full" || fm.Description != "Summary" ||
		fm.Resource != "https://example.com/p" || fm.Timestamp != "2026-07-21T00:00:00Z" ||
		fm.Extra["okf_version"] != "0.1" || fm.Extra["custom"] != true || string(body) != "body" {
		t.Fatalf("full frontmatter = %+v, body = %q", fm, body)
	}
	if !reflect.DeepEqual(fm.Tags, []string{"one", "2", "three"}) {
		t.Errorf("mixed-shape tags = %#v, want scalar values preserved as strings", fm.Tags)
	}

	fm, _ = splitFrontmatter([]byte("---\ntype: Protocol\ntags:\n  nested: true\n---\nbody"))
	if fm.Type != "Protocol" || fm.Tags != nil {
		t.Errorf("malformed optional tags should not hide required fields: %+v", fm)
	}

	raw := []byte("---\ntype: First\ntype: Second\n---\nbody")
	fm, body = splitFrontmatter(raw)
	if fm.Type != "" || string(body) != string(raw) {
		t.Errorf("duplicate YAML keys should be tolerated as malformed input: fm=%+v body=%q", fm, body)
	}

	fm, body = splitFrontmatter([]byte("---\n- not\n- a mapping\n---\nbody"))
	if fm.Type != "" || fm.Title != "" || fm.Tags != nil || fm.Extra != nil || string(body) != "body" {
		t.Errorf("non-mapping frontmatter should be ignored field-wise: fm=%+v body=%q", fm, body)
	}

	fm, _ = splitFrontmatter([]byte("---\ntype: Protocol\ncustom:\n  1: value\n---\nbody"))
	wantExtra := map[string]any{"1": "value"}
	if !reflect.DeepEqual(fm.Extra["custom"], wantExtra) {
		t.Errorf("numeric map key was not normalized: %#v", fm.Extra["custom"])
	}
	if _, err := json.Marshal(fm); err != nil {
		t.Errorf("normalized frontmatter is not JSON-compatible: %v", err)
	}
}

func TestNormalizeJSONValue(t *testing.T) {
	value, ok := normalizeJSONValue([]any{
		map[any]any{"enabled": true},
	})
	if !ok {
		t.Fatal("JSON-compatible nested value was rejected")
	}
	want := []any{map[string]any{"enabled": true}}
	if !reflect.DeepEqual(value, want) {
		t.Errorf("normalized value = %#v, want %#v", value, want)
	}

	unsupported := []struct {
		name  string
		value any
	}{
		{name: "string map child", value: map[string]any{"bad": make(chan int)}},
		{name: "unsupported map key", value: map[any]any{nil: "value"}},
		{name: "normalized key collision", value: map[any]any{"1": "string", 1: "number"}},
		{name: "generic map child", value: map[any]any{"bad": make(chan int)}},
		{name: "slice child", value: []any{make(chan int)}},
		{name: "scalar", value: make(chan int)},
	}
	for _, test := range unsupported {
		t.Run(test.name, func(t *testing.T) {
			if normalized, ok := normalizeJSONValue(test.value); ok || normalized != nil {
				t.Errorf("normalizeJSONValue(%T) = %#v, %v; want nil, false",
					test.value, normalized, ok)
			}
		})
	}
}

func TestExtractLinks(t *testing.T) {
	body := []byte(`See [a](/x/y.md), [b](./z.md#sec), [c](../w.md?q=1),
[title](./with-title.md "display"), [angle](<./space name.md>), and [reference][ref].

![image](./image.md)

` + "`[inline-code](./inline-code.md)`" + `

~~~markdown
[fenced-code](./fenced-code.md)
~~~

[external](https://h/p.md), [network](//h/p.md), [anchor](#top).

[ref]: ../reference.md "reference title"
`)
	links := extractLinks("dir/sub", body)
	got := map[string]string{}
	for _, l := range links {
		got[l.Target] = l.Text
	}
	// Absolute, relative, deep, titled, angle and reference links resolve. Link
	// extraction is intentionally independent of whether a target exists.
	want := map[string]string{
		"x/y":                "a",
		"dir/sub/z":          "b",
		"dir/w":              "c",
		"dir/sub/with-title": "title",
		"dir/sub/space name": "angle",
		"dir/reference":      "reference",
	}
	for target, linkText := range want {
		if got[target] != linkText {
			t.Errorf("link %q = %q, want %q; got %v", target, got[target], linkText, links)
		}
	}
	if len(links) != len(want) {
		t.Errorf("images, code and external links should be excluded, got %v", links)
	}
}

func TestExtractLinks_DropsBundleEscapes(t *testing.T) {
	rootLinks := extractLinks(".", []byte(
		"[escape](../outside.md) [root](/inside.md) [local](inside.md)",
	))
	if !reflect.DeepEqual(rootLinks, []Link{
		{Target: "inside", Text: "root"},
		{Target: "inside", Text: "local"},
	}) {
		t.Errorf("root links = %#v", rootLinks)
	}

	nestedLinks := extractLinks("nested", []byte(
		"[escape](../../outside.md) [parent](../inside.md) [child](./child.md)",
	))
	if !reflect.DeepEqual(nestedLinks, []Link{
		{Target: "inside", Text: "parent"},
		{Target: "nested/child", Text: "child"},
	}) {
		t.Errorf("nested links = %#v", nestedLinks)
	}
}

func TestParseConcept(t *testing.T) {
	c := ParseConcept("research/x402", []byte("---\ntype: Protocol\n---\n\nSee [m](/tables/orders.md)."))
	if c.ID != "research/x402" || c.Frontmatter.Type != "Protocol" {
		t.Errorf("parsed concept: %+v", c)
	}
	if len(c.Links) != 1 || c.Links[0].Target != "tables/orders" {
		t.Errorf("links: %v", c.Links)
	}
}
