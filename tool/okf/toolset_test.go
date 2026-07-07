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

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeStore is an in-memory Store that records how it was called.
type fakeStore struct {
	lastListDir string
	lastReadID  string
	lastQuery   Query
	body        string
}

func (f *fakeStore) List(_ context.Context, dir string) (Listing, error) {
	f.lastListDir = dir
	return Listing{Dir: dir, Concepts: []ConceptMeta{{ID: "x"}}}, nil
}

func (f *fakeStore) Read(_ context.Context, id string) (Concept, error) {
	f.lastReadID = id
	return Concept{ID: id, Body: f.body}, nil
}

func (f *fakeStore) Find(_ context.Context, q Query) ([]Hit, error) {
	f.lastQuery = q
	return []Hit{{ConceptMeta: ConceptMeta{ID: "hit"}}}, nil
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
	names := toolNames(ts.Tools(context.Background()))
	if len(names) != 3 {
		t.Fatalf("want 3 tools, got %v", names)
	}
	for _, want := range []string{"okf_list", "okf_read", "okf_find"} {
		if toolByName(ts.Tools(context.Background()), want) == nil {
			t.Errorf("missing tool %q (have %v)", want, names)
		}
	}
}

func TestNewToolSet_FindDisabled(t *testing.T) {
	ts, _ := NewToolSet(&fakeStore{}, WithFindEnabled(false))
	names := toolNames(ts.Tools(context.Background()))
	if len(names) != 2 {
		t.Fatalf("want 2 tools, got %v", names)
	}
	if toolByName(ts.Tools(context.Background()), "okf_find") != nil {
		t.Error("okf_find should be disabled")
	}
}

func TestNewToolSet_NamePrefix(t *testing.T) {
	ts, _ := NewToolSet(&fakeStore{}, WithNamePrefix("paydocs"))
	if toolByName(ts.Tools(context.Background()), "paydocs_okf_read") == nil {
		t.Errorf("prefix not applied: %v", toolNames(ts.Tools(context.Background())))
	}
}

// TestRequiredSchemaInference guards the pointer/omitempty convention: only
// genuinely required args must land in InputSchema.Required.
func TestRequiredSchemaInference(t *testing.T) {
	ts, _ := NewToolSet(&fakeStore{})
	tools := ts.Tools(context.Background())

	if req := toolByName(tools, "okf_list").Declaration().InputSchema.Required; len(req) != 0 {
		t.Errorf("okf_list should have no required args (dir optional), got %v", req)
	}
	if req := toolByName(tools, "okf_read").Declaration().InputSchema.Required; len(req) != 1 || req[0] != "concept_id" {
		t.Errorf("okf_read required = %v, want [concept_id]", req)
	}
	if req := toolByName(tools, "okf_find").Declaration().InputSchema.Required; len(req) != 1 || req[0] != "query" {
		t.Errorf("okf_find required = %v, want [query] only (type/tags/limit optional)", req)
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
	out := callTool(t, toolByName(ts.Tools(context.Background()), "okf_read"), `{"concept_id":"a/b"}`)

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

func TestCall_ReadTruncation(t *testing.T) {
	store := &fakeStore{body: "0123456789ABCDE"} // 15 bytes.
	ts, _ := NewToolSet(store, WithMaxBodyBytes(10))
	out := callTool(t, toolByName(ts.Tools(context.Background()), "okf_read"), `{"concept_id":"c"}`)

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
	out := callTool(t, toolByName(ts.Tools(context.Background()), "okf_read"), `{"concept_id":"c"}`)

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

// TestFindQueryDescriptionIntact guards against the jsonschema-tag comma split
// silently dropping half of the description.
func TestFindQueryDescriptionIntact(t *testing.T) {
	ts, _ := NewToolSet(&fakeStore{})
	d := toolByName(ts.Tools(context.Background()), "okf_find").Declaration()
	desc := d.InputSchema.Properties["query"].Description
	if !strings.Contains(desc, "description and body") {
		t.Errorf("query description truncated by comma-in-tag: %q", desc)
	}
}

func TestCall_FindDefaultLimit(t *testing.T) {
	store := &fakeStore{}
	ts, _ := NewToolSet(store, WithFindLimit(7))
	find := toolByName(ts.Tools(context.Background()), "okf_find")

	callTool(t, find, `{"query":"pay"}`)
	if store.lastQuery.Limit != 7 {
		t.Errorf("default find limit not applied: %d", store.lastQuery.Limit)
	}
	callTool(t, find, `{"query":"pay","limit":3,"type":"Rule","tags":["a"]}`)
	if store.lastQuery.Limit != 3 || store.lastQuery.Type != "Rule" || len(store.lastQuery.Tags) != 1 {
		t.Errorf("explicit find args not plumbed: %+v", store.lastQuery)
	}
}
