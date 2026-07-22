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
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeStore is an in-memory Store that records how it was called.
type fakeStore struct {
	lastListDir string
	lastReadID  string
	body        string
	frontmatter Frontmatter
}

func (f *fakeStore) List(_ context.Context, dir string) (Listing, error) {
	f.lastListDir = dir
	return Listing{Dir: dir, Concepts: []ConceptMeta{{ID: "x"}}}, nil
}

func (f *fakeStore) Read(_ context.Context, id string) (Concept, error) {
	f.lastReadID = id
	if id == "__missing__" {
		return Concept{}, ErrNotFound
	}
	return Concept{ID: id, Frontmatter: f.frontmatter, Body: f.body}, nil
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, tl.Declaration().Name)
	}
	return names
}

func toolByName(tools []tool.Tool, name string) tool.Tool {
	for _, tl := range tools {
		if tl.Declaration().Name == name {
			return tl
		}
	}
	return nil
}

func modelTools(ts tool.ToolSet) []tool.Tool {
	return itool.NewNamedToolSet(ts).Tools(context.Background())
}

func TestNewToolSet_NilStore(t *testing.T) {
	if _, err := NewToolSet(nil); err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestNewToolSet_Wiring(t *testing.T) {
	ts, err := NewToolSet(&fakeStore{})
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	if ts.Name() != defaultName {
		t.Errorf("Name = %q", ts.Name())
	}
	rawNames := toolNames(ts.Tools(context.Background()))
	if len(rawNames) != 2 {
		t.Fatalf("want 2 raw tools, got %v", rawNames)
	}
	for _, want := range []string{"list", "read"} {
		if toolByName(ts.Tools(context.Background()), want) == nil {
			t.Errorf("missing raw tool %q (have %v)", want, rawNames)
		}
	}
	tools := modelTools(ts)
	modelNames := toolNames(tools)
	for _, want := range []string{"okf_list", "okf_read"} {
		if toolByName(tools, want) == nil {
			t.Errorf("missing model-facing tool %q (have %v)", want, modelNames)
		}
	}
}

func TestNewToolSet_NamePrefix(t *testing.T) {
	ts, _ := NewToolSet(&fakeStore{}, WithNamePrefix("paydocs"))
	if toolByName(modelTools(ts), "paydocs_okf_read") == nil {
		t.Errorf("prefix not applied: %v", toolNames(modelTools(ts)))
	}
	if ts.Name() != "paydocs_okf" {
		t.Errorf("prefixed tool set name = %q, want paydocs_okf", ts.Name())
	}
}

func TestToolSet_CloseAndListDispatch(t *testing.T) {
	store := &fakeStore{}
	ts, err := NewToolSet(store)
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	if err := ts.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	list := toolByName(modelTools(ts), "okf_list")
	callTool(t, list, `{}`)
	if store.lastListDir != "" {
		t.Errorf("root list dir = %q, want empty", store.lastListDir)
	}
	callTool(t, list, `{"dir":"research"}`)
	if store.lastListDir != "research" {
		t.Errorf("explicit list dir = %q, want research", store.lastListDir)
	}
}

func TestNewToolSet_RejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{name: "prefix with space", opts: []Option{WithNamePrefix("pay docs")}},
		{name: "prefix with unicode", opts: []Option{WithNamePrefix("支付")}},
		{name: "prefix too long", opts: []Option{WithNamePrefix(strings.Repeat("a", 56))}},
		{name: "negative body limit", opts: []Option{WithMaxBodyBytes(-1)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewToolSet(&fakeStore{}, tt.opts...); err == nil {
				t.Fatal("NewToolSet should reject invalid options")
			}
		})
	}
}

// TestRequiredSchemaInference guards the pointer/omitempty convention: only
// genuinely required args must land in InputSchema.Required.
func TestRequiredSchemaInference(t *testing.T) {
	ts, _ := NewToolSet(&fakeStore{})
	tools := modelTools(ts)

	if req := toolByName(tools, "okf_list").Declaration().InputSchema.Required; len(req) != 0 {
		t.Errorf("okf_list should have no required args (dir optional), got %v", req)
	}
	if req := toolByName(tools, "okf_read").Declaration().InputSchema.Required; len(req) != 1 || req[0] != "concept_id" {
		t.Errorf("okf_read required = %v, want [concept_id]", req)
	}
}

func callTool(t *testing.T, tl tool.Tool, args string) []byte {
	t.Helper()
	ct, ok := tl.(tool.CallableTool)
	if !ok {
		t.Fatalf("tool %q is not callable", tl.Declaration().Name)
	}
	res, err := ct.Call(context.Background(), []byte(args))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return out
}

func TestCall_ReadRoundtrip(t *testing.T) {
	store := &fakeStore{body: "hello"}
	ts, _ := NewToolSet(store)
	out := callTool(t, toolByName(modelTools(ts), "okf_read"), `{"concept_id":"a/b"}`)

	var got Concept
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if store.lastReadID != "a/b" {
		t.Errorf("store got id %q", store.lastReadID)
	}
	if got.ID != "a/b" || got.Body != "hello" {
		t.Errorf("result = %+v", got)
	}
}

func TestReadOutputSchemaAllowsArbitraryExtraValues(t *testing.T) {
	store := &fakeStore{frontmatter: Frontmatter{Extra: map[string]any{
		"active": true,
		"owner":  "pm-team",
	}}}
	ts, err := NewToolSet(store)
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	read := toolByName(modelTools(ts), "okf_read")
	extra := read.Declaration().OutputSchema.Properties["frontmatter"].Properties["extra"]
	if allowed, ok := extra.AdditionalProperties.(bool); !ok || !allowed {
		t.Fatalf("extra additionalProperties = %#v, want true", extra.AdditionalProperties)
	}

	out := callTool(t, read, `{"concept_id":"a/b"}`)
	var got Concept
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Frontmatter.Extra["owner"] != "pm-team" || got.Frontmatter.Extra["active"] != true {
		t.Errorf("extra values = %#v", got.Frontmatter.Extra)
	}
}

func TestCall_ReadTruncation(t *testing.T) {
	store := &fakeStore{body: "0123456789ABCDE"} // 15 bytes.
	ts, _ := NewToolSet(store, WithMaxBodyBytes(10))
	out := callTool(t, toolByName(modelTools(ts), "okf_read"), `{"concept_id":"c"}`)

	var got Concept
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Body) != 10 || !got.Truncated {
		t.Errorf("want truncated 10-byte body, got len=%d truncated=%v", len(got.Body), got.Truncated)
	}
}

func TestCall_ReadTruncationIsRuneSafe(t *testing.T) {
	store := &fakeStore{body: "知识库内容说明"} // 7 runes x 3 bytes = 21 bytes.
	ts, _ := NewToolSet(store, WithMaxBodyBytes(7))
	out := callTool(t, toolByName(modelTools(ts), "okf_read"), `{"concept_id":"c"}`)

	var got Concept
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !utf8.ValidString(got.Body) {
		t.Errorf("truncated body is not valid UTF-8: %q", got.Body)
	}
	if len(got.Body) > 7 || !got.Truncated {
		t.Errorf("want rune-safe cap <=7 and Truncated, got len=%d truncated=%v", len(got.Body), got.Truncated)
	}
}

func TestCall_ReadNotFound(t *testing.T) {
	ts, _ := NewToolSet(&fakeStore{})
	read := toolByName(modelTools(ts), "okf_read").(tool.CallableTool)
	_, err := read.Call(context.Background(), []byte(`{"concept_id":"__missing__"}`))
	if err == nil {
		t.Fatal("expected error for a missing concept")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not found") || !strings.Contains(msg, "okf_list") {
		t.Errorf("not-found error should be actionable, got %q", msg)
	}
	if strings.Contains(msg, "no such file") {
		t.Errorf("raw os error leaked to the model: %q", msg)
	}
}

func TestCall_ReadNotFoundUsesPrefixedListTool(t *testing.T) {
	ts, err := NewToolSet(&fakeStore{}, WithNamePrefix("paydocs"))
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}
	read := toolByName(modelTools(ts), "paydocs_okf_read").(tool.CallableTool)
	_, err = read.Call(context.Background(), []byte(`{"concept_id":"__missing__"}`))
	if err == nil {
		t.Fatal("expected missing concept error")
	}
	if !strings.Contains(err.Error(), "paydocs_okf_list") {
		t.Errorf("error %q does not mention the available list tool", err)
	}
}
