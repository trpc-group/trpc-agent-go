//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithBaseDir(t *testing.T) {
	f := &fileToolSet{}
	opt := WithBaseDir("/tmp/testdir")
	opt(f)
	assert.Equal(t, "/tmp/testdir", f.baseDir)
	assert.NoError(t, f.Close())
}

func TestWithSaveFileEnabled(t *testing.T) {
	f := &fileToolSet{}
	opt := WithSaveFileEnabled(false)
	opt(f)
	assert.False(t, f.saveFileEnabled)
}

func TestWithReadFileEnabled(t *testing.T) {
	f := &fileToolSet{}
	opt := WithReadFileEnabled(false)
	opt(f)
	assert.False(t, f.readFileEnabled)
}

func TestWithListFileEnabled(t *testing.T) {
	f := &fileToolSet{}
	opt := WithListFileEnabled(false)
	opt(f)
	assert.False(t, f.listFileEnabled)
}

func TestWithSearchFileEnabled(t *testing.T) {
	f := &fileToolSet{}
	opt := WithSearchFileEnabled(false)
	opt(f)
	assert.False(t, f.searchFileEnabled)
}

func TestWithSearchContentEnabled(t *testing.T) {
	f := &fileToolSet{}
	opt := WithSearchContentEnabled(false)
	opt(f)
	assert.False(t, f.searchContentEnabled)
}

func TestWithCreateDirMode(t *testing.T) {
	f := &fileToolSet{}
	opt := WithCreateDirMode(0700)
	opt(f)
	assert.Equal(t, os.FileMode(0700), f.createDirMode)
}

func TestWithCreateFileMode(t *testing.T) {
	f := &fileToolSet{}
	opt := WithCreateFileMode(0600)
	opt(f)
	assert.Equal(t, os.FileMode(0600), f.createFileMode)
}

func TestNewToolSet_Default(t *testing.T) {
	set, err := NewToolSet()
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	assert.Equal(t, defaultBaseDir, fts.baseDir)
	assert.Equal(t, true, fts.saveFileEnabled)
	assert.Equal(t, true, fts.readFileEnabled)
	assert.Equal(t, true, fts.listFileEnabled)
	assert.Equal(t, true, fts.searchFileEnabled)
	assert.Equal(t, defaultCreateDirMode, fts.createDirMode)
	assert.Equal(t, defaultCreateFileMode, fts.createFileMode)
}

func TestNewToolSet_CustomOptions(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(
		WithBaseDir(dir),
		WithSaveFileEnabled(false),
		WithReadFileEnabled(false),
		WithListFileEnabled(false),
		WithSearchFileEnabled(false),
		WithCreateDirMode(0700),
		WithCreateFileMode(0600),
	)
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	assert.Equal(t, filepath.Clean(dir), fts.baseDir)
	assert.False(t, fts.saveFileEnabled)
	assert.False(t, fts.readFileEnabled)
	assert.False(t, fts.listFileEnabled)
	assert.False(t, fts.searchFileEnabled)
	assert.Equal(t, os.FileMode(0700), fts.createDirMode)
	assert.Equal(t, os.FileMode(0600), fts.createFileMode)
}

func TestNewToolSet_BaseDirNotExist(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "not-exist-dir-for-test")
	set, err := NewToolSet(WithBaseDir(dir))
	assert.Nil(t, set)
	assert.Error(t, err)
}

func TestNewToolSet_FeatureSwitch(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(
		WithBaseDir(dir),
		WithSaveFileEnabled(true),
		WithReadFileEnabled(false),
		WithListFileEnabled(false),
		WithSearchFileEnabled(false),
		WithSearchContentEnabled(false),
		WithReplaceContentEnabled(false),
		WithReadMultipleFilesEnabled(false),
	)
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	assert.True(t, fts.saveFileEnabled)
	assert.False(t, fts.readFileEnabled)
	assert.False(t, fts.listFileEnabled)
	assert.False(t, fts.searchFileEnabled)
	tools := fts.Tools(context.Background())
	assert.Len(t, tools, 1)
}

func TestResolvePath_Normal(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(dir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	p, err := fts.resolvePath("a.txt")
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "a.txt"), p)
}

func TestResolvePath_InputsPrefixAlias(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(dir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	p, err := fts.resolvePath("inputs/a.txt")
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "a.txt"), p)
}

func TestResolvePath_InputsPrefixRealDir(t *testing.T) {
	dir := t.TempDir()
	err := os.Mkdir(filepath.Join(dir, "inputs"), 0755)
	assert.NoError(t, err)
	set, err := NewToolSet(WithBaseDir(dir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	p, err := fts.resolvePath("inputs/a.txt")
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "inputs", "a.txt"), p)
}

func TestResolvePath_DirTraversal(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(dir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	_, err = fts.resolvePath("../a.txt")
	assert.Error(t, err)
	_, err = fts.resolvePath("../../etc/passwd")
	assert.Error(t, err)
}

func TestResolvePath_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(dir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	_, err = fts.resolvePath("/tmp/a.txt")
	assert.Error(t, err)
}

func TestResolvePath_Empty(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(dir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	_, err = fts.resolvePath("")
	assert.NoError(t, err)
}

func TestTools_MultipleCall(t *testing.T) {
	dir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(dir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	t1 := fts.Tools(context.Background())
	t2 := fts.Tools(context.Background())
	assert.Equal(t, t1, t2)
}

func TestTools_InvalidBaseDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	err := os.WriteFile(file, []byte(""), 0644)
	assert.NoError(t, err)
	_, err = NewToolSet(WithBaseDir(file))
	assert.Error(t, err)
}
