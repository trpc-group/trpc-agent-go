//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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

const (
	otherSkill = "other"
)

func createListTestSkill(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	sdir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	data := "---\nname: " + name + "\n" +
		"description: d\n---\nbody\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, skillMdName), []byte(data), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, usageDoc), []byte("use"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, extraDoc), []byte("x"), 0o644,
	))
	return dir
}

func TestListDocsTool_Lists(t *testing.T) {
	root := createListTestSkill(t, demoSkill)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	lt := NewListDocsTool(repo)

	out, err := lt.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`"}`,
	))
	require.NoError(t, err)

	b, _ := json.Marshal(out)
	var arr []string
	require.NoError(t, json.Unmarshal(b, &arr))
	got := map[string]bool{}
	for _, n := range arr {
		got[n] = true
	}
	require.True(t, got[usageDoc])
	require.True(t, got[extraDoc])
}

func TestListDocsTool_Declaration(t *testing.T) {
	// Declaration should describe input and output schemas.
	lt := NewListDocsTool(nil)
	d := lt.Declaration()
	require.NotNil(t, d)
	require.Equal(t, listDocsToolName, d.Name)
	require.NotNil(t, d.InputSchema)
	require.Contains(t, d.InputSchema.Required, "skill")
	require.Contains(t, d.InputSchema.Properties, "skill")
	require.NotNil(t, d.OutputSchema)
	require.Equal(t, "array", d.OutputSchema.Type)
}

func TestListDocsTool_InvalidJSON(t *testing.T) {
	lt := NewListDocsTool(nil)
	_, err := lt.Call(context.Background(), []byte("{"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid args")
}

func TestListDocsTool_MissingSkill(t *testing.T) {
	lt := NewListDocsTool(nil)
	_, err := lt.Call(context.Background(), []byte("{}"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "skill is required")
}

func TestListDocsTool_NilRepo_ReturnsEmpty(t *testing.T) {
	// When repo is nil, it should return an empty list.
	lt := NewListDocsTool(nil)
	out, err := lt.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`"}`,
	))
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	var arr []string
	require.NoError(t, json.Unmarshal(b, &arr))
	require.Len(t, arr, 0)
}

func TestListDocsTool_UnknownSkill(t *testing.T) {
	root := createListTestSkill(t, demoSkill)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	lt := NewListDocsTool(repo)
	_, err = lt.Call(context.Background(), []byte(
		`{"skill":"`+otherSkill+`"}`,
	))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown skill: ")
	require.Contains(t, err.Error(), otherSkill)
}
