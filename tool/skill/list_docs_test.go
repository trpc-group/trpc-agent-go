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
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func createListTestSkill(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	sdir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: d\n---\nbody\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "SKILL.md"), []byte(data), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "USAGE.md"), []byte("use"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "EXTRA.txt"), []byte("x"), 0o644,
	))
	return dir
}

func TestListDocsTool_Lists(t *testing.T) {
	root := createListTestSkill(t, "demo")
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	lt := NewListDocsTool(repo)

	out, err := lt.Call(context.Background(), []byte(
		`{"skill":"demo"}`,
	))
	require.NoError(t, err)

	b, _ := json.Marshal(out)
	var arr []string
	require.NoError(t, json.Unmarshal(b, &arr))
	got := map[string]bool{}
	for _, n := range arr {
		got[n] = true
	}
	require.True(t, got["USAGE.md"])
	require.True(t, got["EXTRA.txt"])
}
