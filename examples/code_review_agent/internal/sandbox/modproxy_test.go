//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewModuleProxyDefaults(t *testing.T) {
	mp := NewModuleProxy("")
	assert.NotEmpty(t, mp.cacheDir)
	assert.Contains(t, mp.cacheDir, "cr-mod-cache")
}

func TestNewModuleProxyCustom(t *testing.T) {
	tmp := t.TempDir()
	mp := NewModuleProxy(tmp)
	assert.Equal(t, tmp, mp.cacheDir)
}

func TestModuleProxySetup(t *testing.T) {
	tmp := t.TempDir()

	goMod := filepath.Join(tmp, "go.mod")
	err := os.WriteFile(goMod, []byte("module test\n\ngo 1.21\n"), 0o644)
	require.NoError(t, err)

	mainGo := filepath.Join(tmp, "main.go")
	err = os.WriteFile(mainGo, []byte("package main\n\nfunc main() {}\n"), 0o644)
	require.NoError(t, err)

	mp := NewModuleProxy(filepath.Join(t.TempDir(), "modcache"))
	err = mp.Setup(tmp)
	assert.NoError(t, err)
}

func TestModuleProxyEnv(t *testing.T) {
	mp := NewModuleProxy("/tmp/cache")
	env := mp.Env()
	assert.Equal(t, "/tmp/cache", env["GOMODCACHE"])
	assert.Equal(t, "off", env["GOPROXY"])
	assert.Equal(t, "-mod=mod", env["GOFLAGS"])
}

func TestModuleProxyEnvSlice(t *testing.T) {
	mp := NewModuleProxy("/cache")
	env := mp.EnvSlice()
	assert.Contains(t, env, "GOMODCACHE=/cache")
	assert.Contains(t, env, "GOPROXY=off")
}

func TestModuleProxySetupNoMod(t *testing.T) {
	tmp := t.TempDir()
	mp := NewModuleProxy(filepath.Join(t.TempDir(), "modcache"))
	err := mp.Setup(tmp)
	assert.Error(t, err)
}
