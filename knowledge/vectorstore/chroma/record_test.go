//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chroma

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/chroma"
)

func TestDocToRecordAndBack(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	now := int64(1_700_000_000)
	doc := &document.Document{
		ID:        "doc-1",
		Name:      "guide",
		Content:   "hello chroma",
		CreatedAt: time.Unix(now-10, 0),
		Metadata: map[string]any{
			"category": "guide",
			"ok":       true,
			"n":        3,
			"nested":   map[string]any{"k": "v"},
		},
	}

	rec, err := vs.docToRecord(doc, []float64{0.1, 0.2, 0.3}, now)
	if err != nil {
		t.Fatalf("docToRecord() error = %v", err)
	}
	if len(rec.IDs) != 1 || rec.IDs[0] != "doc-1" || rec.Documents[0] != "hello chroma" {
		t.Fatalf("record id/doc = %#v", rec)
	}
	md := rec.Metadatas[0]
	if md[metaName] != "guide" {
		t.Fatalf("reserved name = %#v, want document.Name", md[metaName])
	}
	if md["category"] != "guide" || md["ok"] != true {
		t.Fatalf("scalar metadata = %#v", md)
	}
	raw, _ := md[metaJSON].(string)
	var nested map[string]any
	if err := json.Unmarshal([]byte(raw), &nested); err != nil || nested["nested"] == nil {
		t.Fatalf("_json = %q err=%v", raw, err)
	}

	got, emb, err := vs.recordToDoc(&storage.GetResult{
		IDs:        rec.IDs,
		Documents:  rec.Documents,
		Metadatas:  rec.Metadatas,
		Embeddings: rec.Embeddings,
	}, 0)
	if err != nil {
		t.Fatalf("recordToDoc() error = %v", err)
	}
	if got.ID != "doc-1" || got.Name != "guide" || got.Content != "hello chroma" {
		t.Fatalf("doc = %#v", got)
	}
	if got.Metadata["category"] != "guide" {
		t.Fatalf("restored category = %#v", got.Metadata)
	}
	if _, ok := got.Metadata["nested"]; !ok {
		t.Fatalf("nested missing: %#v", got.Metadata)
	}
	if len(emb) != 3 {
		t.Fatalf("emb len = %d", len(emb))
	}
}

func TestDocToRecordOmitsEmptyEmbedding(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	rec, err := vs.docToRecord(&document.Document{ID: "d", Content: "c"}, nil, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Embeddings) != 0 {
		t.Fatalf("embeddings = %#v, want omitted", rec.Embeddings)
	}
}

func TestDocToRecordRejectsReservedMetadataKeys(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	for _, key := range []string{metaName, metaCreatedAt, metaUpdatedAt, metaJSON} {
		t.Run(key, func(t *testing.T) {
			_, err := vs.docToRecord(&document.Document{
				ID:       "d",
				Metadata: map[string]any{key: "caller-value"},
			}, []float64{1, 0, 0}, time.Now().Unix())
			if err == nil || !strings.Contains(err.Error(), "conflicts with a reserved document field") {
				t.Fatalf("docToRecord() error = %v", err)
			}
		})
	}
}

func TestDocToRecordRejectsByteMetadata(t *testing.T) {
	vs := testVectorStore(nil)
	_, err := vs.docToRecord(&document.Document{
		ID: "i", Content: "c", Metadata: map[string]any{"k": []byte("v")},
	}, []float64{1, 0, 0}, 1)
	if err == nil || !strings.Contains(err.Error(), "must not be []byte") {
		t.Fatalf("docToRecord() error = %v", err)
	}
}

func TestDocToRecordRejectsConfiguredSparseKey(t *testing.T) {
	vs := testVectorStore(nil, WithSparseSearch(stubSparseEmbedder{}))
	_, err := vs.docToRecord(&document.Document{
		ID:       "i",
		Content:  "c",
		Metadata: map[string]any{defaultSparseEmbeddingKey: "user value"},
	}, []float64{1, 0, 0}, 1)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("docToRecord() error = %v", err)
	}
}

func TestDocToRecordDoesNotEnforceServerQuotas(t *testing.T) {
	vs := testVectorStore(nil)
	md := map[string]any{strings.Repeat("k", 37): strings.Repeat("v", 8183)}
	for i := 0; i < 40; i++ {
		md[fmt.Sprintf("extra-%d", i)] = "v"
	}
	rec, err := vs.docToRecord(&document.Document{
		ID:       strings.Repeat("i", 129),
		Name:     strings.Repeat("n", 100),
		Content:  strings.Repeat("c", 16385),
		Metadata: md,
	}, []float64{1, 0, 0}, 1)
	if err != nil {
		t.Fatalf("docToRecord() error = %v", err)
	}
	if rec.IDs[0] != strings.Repeat("i", 129) {
		t.Fatalf("id = %q", rec.IDs[0])
	}
}

func TestValidateEmbeddingRejectsWireZeroVector(t *testing.T) {
	err := validateEmbedding([]float64{1e-50, -1e-50, 1e-50}, 3, true)
	if err == nil || !strings.Contains(err.Error(), "zero vector") {
		t.Fatalf("validateEmbedding() error = %v, want wire zero vector", err)
	}
}

func TestRecordToDocBounds(t *testing.T) {
	vs := testVectorStore(newFakeClient())
	if _, _, err := vs.recordToDoc(nil, 0); !errors.Is(err, errRecordIndexOutOfRange) {
		t.Fatalf("nil result = %v", err)
	}
	if _, _, err := vs.recordToDoc(&storage.GetResult{IDs: []string{"a"}}, 3); !errors.Is(err, errRecordIndexOutOfRange) {
		t.Fatalf("oob = %v", err)
	}
	got, _, err := vs.recordToDoc(&storage.GetResult{
		IDs:       []string{"a"},
		Documents: []string{"c"},
		Metadatas: []map[string]any{{
			metaName:      123,
			metaCreatedAt: "100",
			metaUpdatedAt: true,
			"k":           "v",
		}},
	}, 0)
	if err != nil || got.Name != "" || got.CreatedAt.Unix() != 100 || got.Metadata["k"] != "v" {
		t.Fatalf("partial record = %#v %v", got, err)
	}
	if _, _, err := vs.recordToDoc(&storage.GetResult{
		IDs:       []string{"a"},
		Metadatas: []map[string]any{{metaJSON: "{"}},
	}, 0); err == nil {
		t.Fatal("corrupt _json should fail")
	}
	if _, _, err := vs.recordToDoc(&storage.GetResult{
		IDs:       []string{"a"},
		Metadatas: []map[string]any{{metaJSON: 1}},
	}, 0); err == nil {
		t.Fatal("non-string _json should fail")
	}
	if _, err := vs.docToRecord(nil, nil, 0); !errors.Is(err, errDocumentRequired) {
		t.Fatalf("nil doc = %v", err)
	}
	if _, err := vs.docToRecord(&document.Document{}, nil, 0); !errors.Is(err, errDocumentIDRequired) {
		t.Fatalf("empty id = %v", err)
	}
}

func TestScalarAndUnixHelpers(t *testing.T) {
	if !isScalar(true) || !isScalar([]int{1}) || !isScalar([]string{"a"}) ||
		isScalar(nil) || isScalar([]any{"a", 1}) || isScalar([]int{}) {
		t.Fatal("isScalar")
	}
	if _, ok := asUnix(int64(1)); !ok {
		t.Fatal("asUnix int64")
	}
	if _, ok := asUnix("123"); !ok {
		t.Fatal("asUnix string")
	}
	if _, ok := asUnix(3.0); !ok {
		t.Fatal("asUnix float")
	}
	if _, ok := asUnix(json.Number("7")); !ok {
		t.Fatal("asUnix json")
	}
	if _, ok := asUnix(1); !ok {
		t.Fatal("asUnix int")
	}
	if _, ok := asUnix(true); ok {
		t.Fatal("asUnix bool")
	}
	if !isScalar(int8(1)) || !isScalar(uint32(1)) || !isScalar(float32(1)) {
		t.Fatal("isScalar numeric")
	}
}
