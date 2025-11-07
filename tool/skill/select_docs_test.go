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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func createDocsTestSkill(t *testing.T, name string) string {
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
		filepath.Join(sdir, "USAGE.md"), []byte("use me"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, "EXTRA.txt"), []byte("x"), 0o644,
	))
	return dir
}

func TestSelectDocsTool_ReplaceAndAll(t *testing.T) {
	root := createDocsTestSkill(t, "demo")
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	sd := NewSelectDocsTool(repo)

	// replace with specific doc
	out, err := sd.Call(context.Background(), []byte(
		`{"skill":"demo","docs":["USAGE.md"],"mode":"replace"}`,
	))
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	arr := m["selected_docs"].([]any)
	require.Equal(t, 1, len(arr))
	require.Equal(t, "USAGE.md", arr[0].(string))

	delta := sd.StateDelta(nil, b)
	require.NotNil(t, delta)
	require.Contains(t, string(delta[skill.StateKeyDocsPrefix+"demo"]),
		"USAGE.md")

	// include all
	out, err = sd.Call(context.Background(), []byte(
		`{"skill":"demo","include_all_docs":true}`,
	))
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	delta = sd.StateDelta(nil, b)
	require.Equal(t, []byte("*"),
		delta[skill.StateKeyDocsPrefix+"demo"])
}

func TestSelectDocsTool_AddAndClear(t *testing.T) {
	root := createDocsTestSkill(t, "demo")
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	sd := NewSelectDocsTool(repo)

	// Prepare context with previous selection
	inv := &agent.Invocation{
		Session: &session.Session{State: session.StateMap{}},
	}
	key := skill.StateKeyDocsPrefix + "demo"
	inv.Session.State[key] = []byte(`["USAGE.md"]`)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	// add EXTRA.txt
	out, err := sd.Call(ctx, []byte(
		`{"skill":"demo","docs":["EXTRA.txt"],"mode":"add"}`,
	))
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	arr := m["selected_docs"].([]any)
	// order is not guaranteed; check membership
	got := map[string]bool{}
	for _, v := range arr {
		got[v.(string)] = true
	}
	require.True(t, got["USAGE.md"])
	require.True(t, got["EXTRA.txt"])

	// clear selection
	out, err = sd.Call(context.Background(), []byte(
		`{"skill":"demo","mode":"clear"}`,
	))
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	delta := sd.StateDelta(nil, b)
	require.Equal(t, "[]", string(delta[skill.StateKeyDocsPrefix+"demo"]))
}
