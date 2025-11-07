//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultLocatorBuild(t *testing.T) {
	loc := &locator{}
	path := loc.Build("/tmp/base", "app", "set-123")
	expected := filepath.Join("/tmp/base", "app", "set-123"+defaultEvalSetFileSuffix)
	assert.Equal(t, expected, path)
}

func TestDefaultLocatorList(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "demo")
	err := os.Mkdir(appDir, 0o755)
	assert.NoError(t, err)

	validFile := filepath.Join(appDir, "set-1"+defaultEvalSetFileSuffix)
	err = os.WriteFile(validFile, []byte("{}"), 0o644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(appDir, "ignore.txt"), []byte("x"), 0o644)
	assert.NoError(t, err)
	err = os.Mkdir(filepath.Join(appDir, "nested"), 0o755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(appDir, "nested", "set-2"+defaultEvalSetFileSuffix), []byte("{}"), 0o644)
	assert.NoError(t, err)

	loc := &locator{}
	results, err := loc.List(dir, "demo")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"set-1"}, results)
}

func TestDefaultLocatorListMissingDir(t *testing.T) {
	loc := &locator{}
	results, err := loc.List(t.TempDir(), "missing")
	assert.NoError(t, err)
	assert.Empty(t, results)
}
