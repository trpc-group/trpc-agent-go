//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositorySnapshotResourceBudgets(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*snapshotLimits)
		prepareRepo func(*testing.T, string)
		wantError   string
	}{
		{
			name: "tracked entries include missing files",
			configure: func(limits *snapshotLimits) {
				limits.maxFiles = 1
			},
			prepareRepo: func(t *testing.T, repoRoot string) {
				mustWriteFile(t, filepath.Join(repoRoot, "a-missing.go"), "package snapshot\n")
				mustWriteFile(t, filepath.Join(repoRoot, "b-present.go"), "package snapshot\n")
				mustRunGit(t, repoRoot, "add", "a-missing.go", "b-present.go")
				if err := os.Remove(filepath.Join(repoRoot, "a-missing.go")); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "tracked entries",
		},
		{
			name: "unique parent directories",
			configure: func(limits *snapshotLimits) {
				limits.maxDirectories = 1
			},
			prepareRepo: func(t *testing.T, repoRoot string) {
				mustWriteFile(t, filepath.Join(repoRoot, "one", "a.go"), "package one\n")
				mustWriteFile(t, filepath.Join(repoRoot, "two", "b.go"), "package two\n")
				mustRunGit(t, repoRoot, "add", "one/a.go", "two/b.go")
			},
			wantError: "tracked directories",
		},
		{
			name: "single raw path bytes",
			configure: func(limits *snapshotLimits) {
				limits.maxPathBytes = 4
			},
			prepareRepo: func(t *testing.T, repoRoot string) {
				mustWriteFile(t, filepath.Join(repoRoot, "long-name.go"), "package snapshot\n")
				mustRunGit(t, repoRoot, "add", "long-name.go")
			},
			wantError: "path limit",
		},
		{
			name: "total raw path bytes",
			configure: func(limits *snapshotLimits) {
				limits.maxTotalPathBytes = 3
			},
			prepareRepo: func(t *testing.T, repoRoot string) {
				mustWriteFile(t, filepath.Join(repoRoot, "aa"), "a")
				mustWriteFile(t, filepath.Join(repoRoot, "bb"), "b")
				mustRunGit(t, repoRoot, "add", "aa", "bb")
			},
			wantError: "tracked paths exceed",
		},
		{
			name: "actual content bytes",
			configure: func(limits *snapshotLimits) {
				limits.maxBytes = 4
			},
			prepareRepo: func(t *testing.T, repoRoot string) {
				mustWriteFile(t, filepath.Join(repoRoot, "data"), "12345")
				mustRunGit(t, repoRoot, "add", "data")
			},
			wantError: "exceeds 4 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			mustRunGit(t, repoRoot, "init")
			tt.prepareRepo(t, repoRoot)
			limits := defaultSandboxSnapshotLimits()
			tt.configure(&limits)
			before := snapshotTempRoots(t)
			if _, err := prepareSandboxRepoSnapshot(
				context.Background(),
				repoRoot,
				nil,
				limits,
			); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("prepare snapshot error = %v, want %q", err, tt.wantError)
			}
			assertNoNewSnapshotTempRoots(t, before)
		})
	}
}

func TestRepositorySnapshotPathLimitAllowsExactBoundary(t *testing.T) {
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	mustWriteFile(t, filepath.Join(repoRoot, "1234"), "data")
	mustRunGit(t, repoRoot, "add", "1234")
	limits := defaultSandboxSnapshotLimits()
	limits.maxPathBytes = 4
	snapshot, err := prepareSandboxRepoSnapshot(context.Background(), repoRoot, nil, limits)
	if err != nil {
		t.Fatalf("prepare exact-boundary snapshot: %v", err)
	}
	defer os.RemoveAll(snapshot.Root)
	if snapshot.Files != 1 {
		t.Fatalf("snapshot = %+v, want one exact-boundary path", snapshot)
	}
}

func TestRepositorySnapshotCancellationBeforeCreationLeavesNoTempRoot(t *testing.T) {
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := snapshotTempRoots(t)
	if _, err := prepareSandboxRepoSnapshot(
		ctx,
		repoRoot,
		nil,
		defaultSandboxSnapshotLimits(),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("prepare canceled snapshot error = %v, want context canceled", err)
	}
	assertNoNewSnapshotTempRoots(t, before)
}

func TestSnapshotCopyStopsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterFirstRead{
		cancel: cancel,
		data:   []byte("copied-before-cancel"),
	}
	var destination bytes.Buffer
	copied, err := copyWithContext(ctx, &destination, reader, 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copy error = %v, want context canceled", err)
	}
	if copied == 0 || copied != int64(destination.Len()) {
		t.Fatalf("copied = %d destination bytes = %d", copied, destination.Len())
	}
	if reader.reads != 1 {
		t.Fatalf("underlying reader calls = %d, want one before cancellation", reader.reads)
	}
}

func TestSnapshotCopyEnforcesActualBytesRead(t *testing.T) {
	var destination bytes.Buffer
	copied, err := copyWithContext(
		context.Background(),
		&destination,
		strings.NewReader("12345"),
		4,
	)
	if !errors.Is(err, errSnapshotCopyLimit) {
		t.Fatalf("copy error = %v, want snapshot byte limit", err)
	}
	if copied != 5 {
		t.Fatalf("copied = %d, want the actual fifth byte to trigger the limit", copied)
	}
}

func TestSnapshotLimitProducesSingleGovernanceWarning(t *testing.T) {
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	mustWriteFile(t, filepath.Join(repoRoot, "go.mod"), "module example.com/snapshot\n\ngo 1.21\n")
	mustWriteFile(t, filepath.Join(repoRoot, "hello.go"), "package hello\n")
	mustRunGit(t, repoRoot, "add", "go.mod", "hello.go")
	limits := defaultSandboxSnapshotLimits()
	limits.maxFiles = 1
	runner := &recordingSandboxRunner{}
	before := snapshotTempRoots(t)
	governance, err := runGovernance(
		context.Background(),
		config{},
		reviewInput{kind: inputKindRepoPath, repoRoot: repoRoot},
		parseUnifiedDiff([]byte(minimalDiff())),
		runtimeHooks{
			sandboxRunner:          runner,
			snapshotLimitsOverride: &limits,
		},
	)
	if err != nil {
		t.Fatalf("run governance: %v", err)
	}
	if len(governance.Warnings) != 1 ||
		governance.Warnings[0].RuleID != ruleSandboxSnapshotUnavailable {
		t.Fatalf("governance warnings = %+v, want one snapshot warning", governance.Warnings)
	}
	if len(runner.calls) != 1 || runner.calls[0].Kind != commandCheckGoVersion {
		t.Fatalf("sandbox calls = %+v, want no repository-dependent command", runner.calls)
	}
	finalized := finalizeRuleMatches(governance.Warnings)
	if !finalized.NeedsHumanReview || len(finalized.Warnings) != 1 {
		t.Fatalf("finalized snapshot warning = %+v", finalized)
	}
	assertNoNewSnapshotTempRoots(t, before)
}

type cancelAfterFirstRead struct {
	cancel func()
	data   []byte
	reads  int
}

func (r *cancelAfterFirstRead) Read(buffer []byte) (int, error) {
	if r.reads > 0 {
		return 0, io.EOF
	}
	r.reads++
	n := copy(buffer, r.data)
	r.cancel()
	return n, nil
}

func snapshotTempRoots(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read temp directory: %v", err)
	}
	roots := make(map[string]bool)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "code-review-repo-snapshot-") {
			roots[entry.Name()] = true
		}
	}
	return roots
}

func assertNoNewSnapshotTempRoots(t *testing.T, before map[string]bool) {
	t.Helper()
	for root := range snapshotTempRoots(t) {
		if !before[root] {
			t.Fatalf("snapshot failure left temporary root %q", root)
		}
	}
}
