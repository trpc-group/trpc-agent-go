//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeSkill(t *testing.T, dir, name string) string {
	t.Helper()
	sdir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: d\n---\nbody\n"
	err := os.WriteFile(filepath.Join(sdir, skillFile),
		[]byte(data), 0o644)
	require.NoError(t, err)
	return sdir
}

func TestFSRepository_Path(t *testing.T) {
	root := t.TempDir()
	sdir := writeSkill(t, root, "alpha")

	r, err := NewFSRepository(root)
	require.NoError(t, err)

	p, err := r.Path("alpha")
	require.NoError(t, err)
	require.Equal(t, sdir, p)

	_, err = r.Path("missing")
	require.Error(t, err)
}
