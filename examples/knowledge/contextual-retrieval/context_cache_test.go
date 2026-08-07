//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "contexts.jsonl")
	descriptor := testContextDescriptor()
	cache, err := openContextCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	if _, ok := cache.get(descriptor); ok {
		t.Fatal("new cache unexpectedly contained a record")
	}
	if err := cache.put(descriptor, "generated context"); err != nil {
		t.Fatalf("put cache record: %v", err)
	}

	reopened, err := openContextCache(path)
	if err != nil {
		t.Fatalf("reopen cache: %v", err)
	}
	got, ok := reopened.get(descriptor)
	if !ok || got != "generated context" {
		t.Fatalf("cache get = (%q, %t), want generated context", got, ok)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("cache mode = %o, want 600", got)
	}
}

func TestContextCacheRejectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*contextCacheRecord)
		want   string
	}{
		{
			name: "input hash mismatch",
			mutate: func(record *contextCacheRecord) {
				record.ParentSHA256 = hashText("different parent")
			},
			want: "key does not match",
		},
		{
			name: "context hash mismatch",
			mutate: func(record *contextCacheRecord) {
				record.Context = "tampered context"
			},
			want: "context hash mismatch",
		},
		{
			name: "empty context",
			mutate: func(record *contextCacheRecord) {
				record.Context = ""
				record.ContextSHA256 = hashText("")
			},
			want: "empty context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "contexts.jsonl")
			record := newContextCacheRecord(testContextDescriptor(), "generated context")
			tt.mutate(&record)
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatalf("marshal record: %v", err)
			}
			encoded = append(encoded, '\n')
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatalf("write cache: %v", err)
			}
			if _, err := openContextCache(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("open cache error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestContextCacheRejectsConflictingDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contexts.jsonl")
	descriptor := testContextDescriptor()
	cache, err := openContextCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	if err := cache.put(descriptor, "first context"); err != nil {
		t.Fatalf("put first record: %v", err)
	}
	if err := cache.put(descriptor, "different context"); err == nil ||
		!strings.Contains(err.Error(), "different context") {
		t.Fatalf("conflicting put error = %v", err)
	}
}

func TestContextCacheRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := openContextCache(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("open symlink error = %v", err)
	}
}

func testContextDescriptor() contextDescriptor {
	return contextDescriptor{
		ProviderIdentity:           "fake:model-a",
		PromptVersion:              contextPromptVersionV1,
		EmbeddingTextFormatVersion: embeddingTextFormatVersionV1,
		ParentSHA256:               hashText("parent"),
		ChunkSHA256:                hashText("chunk"),
	}
}
