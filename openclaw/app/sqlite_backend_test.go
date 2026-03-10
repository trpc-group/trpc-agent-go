//go:build cgo

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

func TestDefaultSQLiteSessionConfigNode(t *testing.T) {
	t.Parallel()

	t.Run("non_sqlite_backend", func(t *testing.T) {
		t.Parallel()

		cfg := &yaml.Node{Kind: yaml.MappingNode}
		got := defaultSQLiteSessionConfigNode(
			sessionBackendInMemory,
			t.TempDir(),
			cfg,
		)
		require.True(t, got == cfg)
	})

	t.Run("keeps_existing_config", func(t *testing.T) {
		t.Parallel()

		cfg := &yaml.Node{Kind: yaml.MappingNode}
		got := defaultSQLiteSessionConfigNode(
			sessionBackendSQLite,
			t.TempDir(),
			cfg,
		)
		require.True(t, got == cfg)
	})

	t.Run("builds_default_path", func(t *testing.T) {
		t.Parallel()

		stateDir := t.TempDir()
		got := defaultSQLiteSessionConfigNode(
			sessionBackendSQLite,
			stateDir,
			nil,
		)
		require.NotNil(t, got)
		require.Equal(t, yaml.MappingNode, got.Kind)
		require.Len(t, got.Content, 2)
		require.Equal(
			t,
			sqliteSessionConfigKeyPath,
			got.Content[0].Value,
		)
		require.Equal(
			t,
			filepath.Join(stateDir, defaultSQLiteSessionDBFile),
			got.Content[1].Value,
		)
	})
}

func TestDefaultSQLiteMemoryConfigNode(t *testing.T) {
	t.Parallel()

	t.Run("non_sqlite_backend", func(t *testing.T) {
		t.Parallel()

		cfg := &yaml.Node{Kind: yaml.MappingNode}
		got := defaultSQLiteMemoryConfigNode(
			memoryBackendInMemory,
			t.TempDir(),
			cfg,
		)
		require.True(t, got == cfg)
	})

	t.Run("keeps_existing_config", func(t *testing.T) {
		t.Parallel()

		cfg := &yaml.Node{Kind: yaml.MappingNode}
		got := defaultSQLiteMemoryConfigNode(
			memoryBackendSQLite,
			t.TempDir(),
			cfg,
		)
		require.True(t, got == cfg)
	})

	t.Run("builds_sqlite_default_path", func(t *testing.T) {
		t.Parallel()

		stateDir := t.TempDir()
		got := defaultSQLiteMemoryConfigNode(
			memoryBackendSQLite,
			stateDir,
			nil,
		)
		require.NotNil(t, got)
		require.Equal(t, yaml.MappingNode, got.Kind)
		require.Len(t, got.Content, 2)
		require.Equal(
			t,
			sqliteSessionConfigKeyPath,
			got.Content[0].Value,
		)
		require.Equal(
			t,
			filepath.Join(stateDir, defaultSQLiteMemoryDBFile),
			got.Content[1].Value,
		)
	})

	t.Run("builds_sqlitevec_default_path", func(t *testing.T) {
		t.Parallel()

		stateDir := t.TempDir()
		got := defaultSQLiteMemoryConfigNode(
			memoryBackendSQLiteVec,
			stateDir,
			nil,
		)
		require.NotNil(t, got)
		require.Equal(t, yaml.MappingNode, got.Kind)
		require.Len(t, got.Content, 2)
		require.Equal(
			t,
			sqliteSessionConfigKeyPath,
			got.Content[0].Value,
		)
		require.Equal(
			t,
			filepath.Join(stateDir, defaultSQLiteVecDBFile),
			got.Content[1].Value,
		)
	})
}

func TestNewSQLiteSessionBackend_WithPathSucceeds(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n",
		dbPath,
	)), &node))

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{
			Config: &node,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewSQLiteSessionBackend_MissingPath(t *testing.T) {
	t.Parallel()

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), sqliteSessionConfigErrMissingPath)
	require.Nil(t, svc)
}

func TestNewSQLiteSessionBackend_CreatesDir(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nested", "sessions.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n",
		dbPath,
	)), &node))

	dir := filepath.Dir(dbPath)
	_, err := os.Stat(dir)
	require.Error(t, err)

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{
			Config: &node,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())

	st, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, st.IsDir())
}

func TestNewSQLiteSessionBackend_BadTablePrefix(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n"+
			"table_prefix: %q\n",
		dbPath,
		"bad-prefix",
	)), &node))

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{
			Config: &node,
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid sqlite table prefix")
	require.Contains(t, err.Error(), "bad-prefix")
	require.Nil(t, svc)
}

func TestNewSQLiteSessionBackend_DecodeStrictFails(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n"+
			"unknown_field: true\n",
		dbPath,
	)), &node))

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{Config: &node},
	)
	require.Error(t, err)
	require.Nil(t, svc)
}

func TestNewSQLiteSessionBackend_EnsureSQLiteDirFails(t *testing.T) {
	t.Parallel()

	const badPath = "/dev/null/sessions.sqlite"
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n",
		badPath,
	)), &node))

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{Config: &node},
	)
	require.Error(t, err)
	require.Nil(t, svc)
}

func TestNewSQLiteSessionBackend_SkipInitAndSummarizer(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n"+
			"skip_db_init: true\n"+
			"table_prefix: %q\n",
		dbPath,
		"good_prefix",
	)), &node))

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{Summarizer: &stubSummarizer{}},
		registry.SessionBackendSpec{Config: &node},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewSQLiteSessionBackend_NewServiceFails(t *testing.T) {
	t.Parallel()

	const badPath = "/dev/null"
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n",
		badPath,
	)), &node))

	svc, err := newSQLiteSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{Config: &node},
	)
	require.Error(t, err)
	require.Nil(t, svc)
}

func TestNewSQLiteMemoryBackend_WithPathSucceeds(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "memories.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n"+
			"table_name: %q\n"+
			"soft_delete: true\n",
		dbPath,
		"memories",
	)), &node))

	svc, err := newSQLiteMemoryBackend(
		registry.MemoryDeps{Extractor: &stubExtractor{}},
		registry.MemoryBackendSpec{
			Limit:  7,
			Config: &node,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewSQLiteMemoryBackend_CreatesDir(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nested", "memories.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n",
		dbPath,
	)), &node))

	dir := filepath.Dir(dbPath)
	_, err := os.Stat(dir)
	require.Error(t, err)

	svc, err := newSQLiteMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{Config: &node},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())

	st, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, st.IsDir())
}

func TestNewSQLiteMemoryBackend_WithDSNSucceeds(t *testing.T) {
	t.Parallel()

	const sqliteMemoryDSN = ":memory:"
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"dsn: %q\n"+
			"table_name: %q\n"+
			"soft_delete: true\n",
		sqliteMemoryDSN,
		"memories",
	)), &node))

	svc, err := newSQLiteMemoryBackend(
		registry.MemoryDeps{Extractor: &stubExtractor{}},
		registry.MemoryBackendSpec{
			Limit:  7,
			Config: &node,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())
}

func TestNewMemoryService_SQLiteUsesDefaultPath(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	svc, err := newMemoryService(nil, runOptions{
		AppName:       "demo",
		StateDir:      stateDir,
		MemoryBackend: memoryBackendSQLite,
	})
	require.NoError(t, err)
	require.NotNil(t, svc)
	require.NoError(t, svc.Close())

	st, err := os.Stat(filepath.Join(stateDir, defaultSQLiteMemoryDBFile))
	require.NoError(t, err)
	require.False(t, st.IsDir())
}

func TestNewSQLiteVecMemoryBackend_RequiresBuildTag(t *testing.T) {
	t.Parallel()
	if sqliteVecMemoryBackendEnabled {
		t.Skip("sqlitevec backend is enabled in this build")
	}

	dbPath := filepath.Join(t.TempDir(), "memories_vec.sqlite")
	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(fmt.Sprintf(
		"path: %q\n"+
			"embedder:\n"+
			"  type: %q\n"+
			"  model: %q\n",
		dbPath,
		"openai",
		"text-embedding-3-small",
	)), &node))

	svc, err := newSQLiteVecMemoryBackend(
		registry.MemoryDeps{Extractor: &stubExtractor{}},
		registry.MemoryBackendSpec{Config: &node},
	)
	require.Error(t, err)
	require.Nil(t, svc)
	require.Contains(t, err.Error(),
		sqliteVecMemoryBackendErrBuildTagRequired)
}

func TestNewMemoryService_SQLiteVecRequiresBuildTag(t *testing.T) {
	t.Parallel()
	if sqliteVecMemoryBackendEnabled {
		t.Skip("sqlitevec backend is enabled in this build")
	}

	svc, err := newMemoryService(nil, runOptions{
		AppName:       "demo",
		StateDir:      t.TempDir(),
		MemoryBackend: memoryBackendSQLiteVec,
	})
	require.Error(t, err)
	require.Nil(t, svc)
	require.Contains(t, err.Error(),
		sqliteVecMemoryBackendErrBuildTagRequired)
}

func TestEnsureSQLiteDir_SpecialCases(t *testing.T) {
	t.Parallel()

	require.NoError(t, ensureSQLiteDir(""))
	require.NoError(t, ensureSQLiteDir(":memory:"))
	require.NoError(t, ensureSQLiteDir("sessions.sqlite"))
}

func TestEnsureSQLiteDir_MkdirFails(t *testing.T) {
	t.Parallel()

	err := ensureSQLiteDir("/dev/null/sessions.sqlite")
	require.Error(t, err)
	require.Contains(t, err.Error(), "mkdir session sqlite dir")
}
