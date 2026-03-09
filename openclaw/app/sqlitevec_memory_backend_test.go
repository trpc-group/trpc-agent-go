//go:build cgo && openclaw_sqlitevec

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestNewSQLiteVecMemoryBackend_WithPathSucceeds(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memories_vec.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n"+
			"table_name: %q\n"+
			"skip_db_init: true\n"+
			"soft_delete: false\n"+
			"max_results: 5\n"+
			"index_dimension: 1536\n"+
			"embedder:\n"+
			"  type: %q\n"+
			"  model: %q\n",
		dbPath,
		"memories",
		"openai",
		"text-embedding-3-small",
	)), &node))

	svc, err := newSQLiteVecMemoryBackend(
		registry.MemoryDeps{Extractor: &stubExtractor{}},
		registry.MemoryBackendSpec{
			Limit:  3,
			Config: &node,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewMemoryService_SQLiteVecUsesDefaultPath(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	svc, err := newMemoryService(nil, runOptions{
		AppName:       "demo",
		StateDir:      stateDir,
		MemoryBackend: memoryBackendSQLiteVec,
	})
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())

	st, err := os.Stat(filepath.Join(stateDir, defaultSQLiteVecDBFile))
	require.NoError(t, err)
	require.False(t, st.IsDir())
}
