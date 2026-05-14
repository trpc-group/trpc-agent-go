//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnsureLayout_LoadSaveMetadata(t *testing.T) {
	root := t.TempDir()

	// Ensure layout creates dirs and metadata.json.
	paths, err := EnsureLayout(root)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, DirSkills), paths[DirSkills])
	require.Equal(t, filepath.Join(root, DirWork), paths[DirWork])
	require.Equal(t, filepath.Join(root, DirRuns), paths[DirRuns])
	require.Equal(t, filepath.Join(root, DirOut), paths[DirOut])

	// Loading existing metadata should succeed.
	md, err := LoadMetadata(root)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotZero(t, md.CreatedAt.Unix())

	// Modify and save metadata, then reload to verify roundtrip.
	md.Inputs = append(md.Inputs, InputRecord{
		From:      "host://x",
		To:        "work/y",
		Mode:      "copy",
		Timestamp: time.Now(),
	})
	require.NoError(t, SaveMetadata(root, md))
	md2, err := LoadMetadata(root)
	require.NoError(t, err)
	require.Equal(t, md.Version, md2.Version)
	require.Equal(t, len(md.Inputs), len(md2.Inputs))
}

func TestLoadMetadata_MissingFileReturnsDefault(t *testing.T) {
	root := t.TempDir()
	// No metadata.json yet.
	md, err := LoadMetadata(root)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.Empty(t, md.Inputs)
}

func TestSaveMetadata_ConcurrentDirectWritesKeepValidJSON(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, SaveMetadata(root, WorkspaceMetadata{
		Version: 1,
		Skills:  map[string]SkillMeta{},
	}))

	const writerCount = 32
	var wg sync.WaitGroup
	errs := make(chan error, writerCount)
	for i := 0; i < writerCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("skill_%02d", i)
			errs <- SaveMetadata(root, WorkspaceMetadata{
				Version: 1,
				Skills: map[string]SkillMeta{
					name: {Name: name, RelPath: filepath.Join(DirSkills, name)},
				},
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	raw, err := os.ReadFile(filepath.Join(root, MetaFileName))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	_, err = LoadMetadata(root)
	require.NoError(t, err)

	tmpFiles, err := filepath.Glob(filepath.Join(
		root,
		metadataTmpPrefix+"*"+metadataTmpSuffix,
	))
	require.NoError(t, err)
	require.Empty(t, tmpFiles)
}

func TestWithWorkspaceMetadataLock_ConcurrentReadModifyWrite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, SaveMetadata(root, WorkspaceMetadata{
		Version: 1,
		Skills:  map[string]SkillMeta{},
	}))

	const workerCount = 24
	var wg sync.WaitGroup
	errs := make(chan error, workerCount)
	for i := 0; i < workerCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(
				context.Background(),
				3*time.Second,
			)
			defer cancel()
			errs <- WithWorkspaceMetadataLock(
				ctx,
				root,
				func(context.Context) error {
					md, err := LoadMetadata(root)
					if err != nil {
						return err
					}
					md.Inputs = append(md.Inputs, InputRecord{
						From: fmt.Sprintf("host://input_%02d", i),
						To:   filepath.Join(DirWork, fmt.Sprintf("%02d", i)),
					})
					return SaveMetadata(root, md)
				},
			)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	md, err := LoadMetadata(root)
	require.NoError(t, err)
	require.Len(t, md.Inputs, workerCount)
	seen := make(map[string]struct{}, workerCount)
	for _, rec := range md.Inputs {
		seen[rec.From] = struct{}{}
	}
	require.Len(t, seen, workerCount)
}

func TestWithWorkspaceMetadataLock_ContextDeadline(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithWorkspaceMetadataLock(
			context.Background(),
			root,
			func(context.Context) error {
				close(entered)
				<-release
				return nil
			},
		)
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := WithWorkspaceMetadataLock(
		ctx,
		root,
		func(context.Context) error {
			return nil
		},
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	close(release)
	require.NoError(t, <-done)
}

func TestMetadataTempFileNamePattern(t *testing.T) {
	name := MetadataTempFileName()
	const exactGeneratedName = ".metadata.1.2.3.0123456789abcdef.tmp"

	require.True(t, IsMetadataTempFileName(name))
	require.True(t, IsMetadataTempFileName(exactGeneratedName))
	require.True(t, IsMetadataTempFileName(".metadata.1.2.3.norand.tmp"))
	require.True(t, IsMetadataTempFileName(".metadata.tmp"))
	require.True(t, IsRootMetadataTempPath(name))
	require.True(t, IsRootMetadataTempPath(".metadata.tmp"))
	require.False(t, IsMetadataTempFileName(MetaFileName))
	require.False(t, IsMetadataTempFileName(".metadata.tmp.extra"))
	require.False(t, IsMetadataTempFileName(".metadata.user.tmp"))
	require.False(t, IsMetadataTempFileName(
		".metadata.bad.2.3.0123456789abcdef.tmp",
	))
	require.False(t, IsMetadataTempFileName(
		".metadata.0.2.3.0123456789abcdef.tmp",
	))
	require.False(t, IsMetadataTempFileName(".metadata.1.2.3.tmp"))
	require.False(t, IsMetadataTempFileName(".metadata.1.2.3.bad.tmp"))
	require.False(t, IsMetadataTempFileName(
		".metadata.1.2.3.0123456789ABCDEF.tmp",
	))
	require.False(t, IsRootMetadataTempPath("work/"+name))
	require.False(t, IsRootMetadataTempPath(filepath.Join("work", name)))
	require.False(t, IsRootMetadataTempPath(MetaFileName))
}

func TestDirDigest_DeterministicAndSensitive(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(root, "a", "b"), 0o755,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "a", "b", "x.txt"), []byte("one"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "a", "c.txt"), []byte("two"), 0o644,
	))

	d1, err := DirDigest(root)
	require.NoError(t, err)
	// Recompute should match.
	d2, err := DirDigest(root)
	require.NoError(t, err)
	require.Equal(t, d1, d2)

	// Changing a file should change digest.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "a", "c.txt"), []byte("changed"), 0o644,
	))
	d3, err := DirDigest(root)
	require.NoError(t, err)
	require.NotEqual(t, d1, d3)
}

func TestEnsureLayout_PathConflict_Error(t *testing.T) {
	root := t.TempDir()
	// Create a file that conflicts with a required directory name.
	// MkdirAll should fail when hitting a file path.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, DirSkills), []byte("x"), 0o644,
	))
	_, err := EnsureLayout(root)
	require.Error(t, err)
}

func TestLoadMetadata_InvalidJSON_Error(t *testing.T) {
	root := t.TempDir()
	// Write a bogus metadata.json.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, MetaFileName), []byte("not-json"), 0o644,
	))
	_, err := LoadMetadata(root)
	require.Error(t, err)
}

func TestIsMetadataCorruptError(t *testing.T) {
	var md WorkspaceMetadata
	err := json.Unmarshal([]byte(`{"version":`), &md)
	require.Error(t, err)
	require.True(t, IsMetadataCorruptError(err))

	err = json.Unmarshal([]byte(`{"version":"bad"}`), &md)
	require.Error(t, err)
	require.True(t, IsMetadataCorruptError(err))

	require.False(t, IsMetadataCorruptError(fmt.Errorf("plain error")))
}

func TestSaveMetadata_PathIsFile_Error(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "asfile")
	require.NoError(t, os.WriteFile(root, []byte("x"), 0o644))
	err := SaveMetadata(root, WorkspaceMetadata{Version: 1})
	require.Error(t, err)
}

func TestSaveMetadata_MetadataPathDirectory_Error(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(
		filepath.Join(root, MetaFileName),
		0o755,
	))

	err := SaveMetadata(root, WorkspaceMetadata{Version: 1})

	require.Error(t, err)
	matches, globErr := filepath.Glob(filepath.Join(
		root,
		metadataTmpPrefix+"*"+metadataTmpSuffix,
	))
	require.NoError(t, globErr)
	require.Empty(t, matches)
}

func TestWithWorkspaceMetadataLock_NilContextAndEmptyRoot(t *testing.T) {
	called := false
	err := WithWorkspaceMetadataLock(
		nil,
		" ",
		func(ctx context.Context) error {
			called = true
			require.NotNil(t, ctx)
			return nil
		},
	)

	require.NoError(t, err)
	require.True(t, called)
}

func TestWithWorkspaceMetadataLock_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WithWorkspaceMetadataLock(
		ctx,
		t.TempDir(),
		func(context.Context) error {
			t.Fatal("callback must not run for a canceled context")
			return nil
		},
	)

	require.ErrorIs(t, err, context.Canceled)
}
