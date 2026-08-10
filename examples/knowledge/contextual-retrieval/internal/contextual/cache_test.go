//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package contextual

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "contexts.json")
	cache, err := openContextCache(path)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	if _, ok := cache.get("key"); ok {
		t.Fatal("new cache unexpectedly contained a record")
	}
	if err := cache.put("key", "generated context"); err != nil {
		t.Fatalf("put context: %v", err)
	}

	reopened, err := openContextCache(path)
	if err != nil {
		t.Fatalf("reopen cache: %v", err)
	}
	got, ok := reopened.get("key")
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
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".context-cache-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary cache files = %v, error = %v", matches, err)
	}
}

func TestContextCacheRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name     string
		contents contextCacheFile
		wantErr  string
	}{
		{
			name:     "unsupported version",
			contents: contextCacheFile{Version: 2, Contexts: map[string]string{}},
			wantErr:  "unsupported",
		},
		{
			name:     "empty key",
			contents: contextCacheFile{Version: contextCacheVersion, Contexts: map[string]string{"": "context"}},
			wantErr:  "empty key",
		},
		{
			name:     "empty context",
			contents: contextCacheFile{Version: contextCacheVersion, Contexts: map[string]string{"key": "  "}},
			wantErr:  "is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "contexts.json")
			data, err := json.Marshal(tt.contents)
			if err != nil {
				t.Fatalf("marshal cache: %v", err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatalf("write cache: %v", err)
			}
			if _, err := openContextCache(path); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("open cache error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "contexts.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write malformed cache: %v", err)
	}
	if _, err := openContextCache(path); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("open malformed cache error = %v, want decode error", err)
	}
}

func TestContextCacheRejectsConflictingValue(t *testing.T) {
	cache, err := openContextCache(filepath.Join(t.TempDir(), "contexts.json"))
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	if err := cache.put("key", "first context"); err != nil {
		t.Fatalf("put first context: %v", err)
	}
	if err := cache.put("key", "different context"); err == nil ||
		!strings.Contains(err.Error(), "different context") {
		t.Fatalf("conflicting put error = %v", err)
	}
	if err := cache.put("key", "first context"); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
}

func TestContextCacheRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	data, err := json.Marshal(contextCacheFile{
		Version:  contextCacheVersion,
		Contexts: map[string]string{},
	})
	if err != nil {
		t.Fatalf("marshal cache: %v", err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := openContextCache(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("open symlink error = %v", err)
	}
}

func TestContextCacheKeyTracksGenerationInputs(t *testing.T) {
	base := contextCacheKey("model-a", "parent", "chunk", "embedding")
	tests := []struct {
		name string
		key  string
	}{
		{"model", contextCacheKey("model-b", "parent", "chunk", "embedding")},
		{"parent", contextCacheKey("model-a", "different parent", "chunk", "embedding")},
		{"chunk", contextCacheKey("model-a", "parent", "different chunk", "embedding")},
		{"embedding", contextCacheKey("model-a", "parent", "chunk", "different embedding")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.key == base {
				t.Fatalf("%s change did not change cache key", tt.name)
			}
		})
	}
}
