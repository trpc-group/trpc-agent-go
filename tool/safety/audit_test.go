//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestJSONLSinkConcurrentAndOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	sink, err := NewJSONLSink(path)
	if err != nil {
		t.Fatal(err)
	}
	const count = 100
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sink.WriteAudit(context.Background(), AuditEvent{
				Timestamp: time.Now(), Decision: tool.PermissionActionAllow, RequestID: "safe-hash",
			}); err != nil {
				t.Errorf("WriteAudit: %v", err)
			}
		}()
	}
	wg.Wait()
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
		var event AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid JSONL: %v", err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines != count {
		t.Fatalf("lines = %d, want %d", lines, count)
	}
	if err := sink.WriteAudit(context.Background(), AuditEvent{}); err == nil {
		t.Fatal("write after Close succeeded")
	}
}

func TestJSONLSinkSecuresExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	sink, err := NewJSONLSink(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestJSONLSinkRejectsDirectoryAndSymlink(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewJSONLSink(dir); err == nil {
		t.Fatal("directory audit target was accepted")
	}
	target := filepath.Join(dir, "target.jsonl")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "audit-link.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewJSONLSink(link); err == nil {
		t.Fatal("symbolic-link audit target was accepted")
	}
}
