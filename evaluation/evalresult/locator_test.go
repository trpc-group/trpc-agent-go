//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalresult

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResultLocatorBuild(t *testing.T) {
	loc := &locator{}
	path := loc.Build("/tmp/base", "app", "result-1")
	expected := filepath.Join("/tmp/base", "app", "result-1"+defaultResultFileSuffix)
	assert.Equal(t, expected, path)
}

func TestResultLocatorList(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "demo")
	err := os.MkdirAll(appDir, 0o755)
	assert.NoError(t, err)

	valid := filepath.Join(appDir, "res-1"+defaultResultFileSuffix)
	err = os.WriteFile(valid, []byte("{}"), 0o644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(appDir, "ignore.txt"), []byte("x"), 0o644)
	assert.NoError(t, err)
	err = os.Mkdir(filepath.Join(appDir, "nested"+defaultResultFileSuffix), 0o755)
	assert.NoError(t, err)

	loc := &locator{}
	results, err := loc.List(dir, "demo")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"res-1"}, results)
}

func TestResultLocatorListMissingDir(t *testing.T) {
	loc := &locator{}
	results, err := loc.List(t.TempDir(), "missing")
	assert.NoError(t, err)
	assert.Empty(t, results)
}
