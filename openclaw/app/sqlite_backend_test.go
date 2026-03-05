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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

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
