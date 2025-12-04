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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	demoSkill   = "demo"
	usageDoc    = "USAGE.md"
	extraDoc    = "EXTRA.txt"
	skillMdName = "SKILL.md"
)

func createDocsTestSkill(t *testing.T, name string) string {
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
		filepath.Join(sdir, usageDoc), []byte("use me"), 0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sdir, extraDoc), []byte("x"), 0o644,
	))
	return dir
}

func TestSelectDocsTool_ReplaceAndAll(t *testing.T) {
	root := createDocsTestSkill(t, demoSkill)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	sd := NewSelectDocsTool(repo)

	// replace with specific doc
	out, err := sd.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`","docs":["`+usageDoc+`"],`+
			`"mode":"replace"}`,
	))
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	arr := m["selected_docs"].([]any)
	require.Equal(t, 1, len(arr))
	require.Equal(t, usageDoc, arr[0].(string))

	delta := sd.StateDelta(nil, b)
	require.NotNil(t, delta)
	require.Contains(t,
		string(delta[skill.StateKeyDocsPrefix+demoSkill]), usageDoc)

	// include all
	out, err = sd.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`","include_all_docs":true}`,
	))
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	delta = sd.StateDelta(nil, b)
	require.Equal(t, []byte("*"),
		delta[skill.StateKeyDocsPrefix+demoSkill])
}

func TestSelectDocsTool_AddAndClear(t *testing.T) {
	root := createDocsTestSkill(t, demoSkill)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	sd := NewSelectDocsTool(repo)

	// Prepare context with previous selection
	inv := &agent.Invocation{
		Session: &session.Session{State: session.StateMap{}},
	}
	key := skill.StateKeyDocsPrefix + demoSkill
	inv.Session.State[key] = []byte(`["` + usageDoc + `"]`)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	// add EXTRA.txt
	out, err := sd.Call(ctx, []byte(
		`{"skill":"`+demoSkill+`","docs":["`+extraDoc+`"],`+
			`"mode":"add"}`,
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
	require.True(t, got[usageDoc])
	require.True(t, got[extraDoc])

	// clear selection
	out, err = sd.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`","mode":"clear"}`,
	))
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	delta := sd.StateDelta(nil, b)
	require.Equal(t, "[]",
		string(delta[skill.StateKeyDocsPrefix+demoSkill]))
}

// stubRepo returns error for any Get. Others are unused in tests.
type stubRepo struct{}

func (s stubRepo) Summaries() []skill.Summary       { return nil }
func (s stubRepo) Path(name string) (string, error) { return "", errors.New("x") }
func (s stubRepo) Get(name string) (*skill.Skill, error) {
	return nil, errors.New("not found")
}

func TestSelectDocsTool_DeclarationSchema(t *testing.T) {
	sd := NewSelectDocsTool(nil)
	d := sd.Declaration()
	require.NotNil(t, d)
	require.Equal(t, selectDocsToolName, d.Name)
	require.NotNil(t, d.InputSchema)
	require.NotNil(t, d.OutputSchema)
	// Ensure key properties are defined
	inProps := d.InputSchema.Properties
	require.Contains(t, inProps, "skill")
	require.Contains(t, inProps, "docs")
	require.Contains(t, inProps, "include_all_docs")
	require.Contains(t, inProps, "mode")
}

func TestSelectDocsTool_ParseErrors(t *testing.T) {
	sd := NewSelectDocsTool(stubRepo{})

	// invalid JSON
	_, err := sd.Call(context.Background(), []byte("{"))
	require.Error(t, err)

	// empty skill
	_, err = sd.Call(context.Background(), []byte(
		`{"skill":"   "}`,
	))
	require.Error(t, err)

	// unknown skill (repo.Get error)
	_, err = sd.Call(context.Background(), []byte(
		`{"skill":"nope"}`,
	))
	require.Error(t, err)
}

func TestSelectDocsTool_ModeNormalization(t *testing.T) {
	root := createDocsTestSkill(t, demoSkill)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	sd := NewSelectDocsTool(repo)

	// default/invalid -> replace
	out, err := sd.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`","docs":["`+usageDoc+`"],`+
			`"mode":"invalid"}`,
	))
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	require.Equal(t, "replace", m["mode"])

	// case-insensitive and trimmed add
	out, err = sd.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`","docs":["`+extraDoc+`"],`+
			`"mode":"  ADD  "}`,
	))
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	m = map[string]any{}
	require.NoError(t, json.Unmarshal(b, &m))
	require.Equal(t, "add", m["mode"])
}

func TestSelectDocsTool_PreviousSelectionBranches(t *testing.T) {
	root := createDocsTestSkill(t, demoSkill)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	sd := NewSelectDocsTool(repo)

	// no invocation in context
	out, err := sd.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`","docs":["`+usageDoc+`"],`+
			`"mode":"add"}`,
	))
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	arr, _ := m["selected_docs"].([]any)
	require.Equal(t, 1, len(arr))

	// invocation present but nil
	ctx := agent.NewInvocationContext(context.Background(), nil)
	_, err = sd.Call(ctx, []byte(
		`{"skill":"`+demoSkill+`","docs":["`+usageDoc+`"],`+
			`"mode":"add"}`,
	))
	require.NoError(t, err)

	// session nil
	inv := &agent.Invocation{}
	ctx = agent.NewInvocationContext(context.Background(), inv)
	_, err = sd.Call(ctx, []byte(
		`{"skill":"`+demoSkill+`","docs":["`+extraDoc+`"],`+
			`"mode":"add"}`,
	))
	require.NoError(t, err)

	// empty state value
	inv = &agent.Invocation{Session: &session.Session{State: map[string][]byte{}}}
	key := skill.StateKeyDocsPrefix + demoSkill
	inv.Session.State[key] = []byte("")
	ctx = agent.NewInvocationContext(context.Background(), inv)
	_, err = sd.Call(ctx, []byte(
		`{"skill":"`+demoSkill+`","docs":["`+extraDoc+`"],`+
			`"mode":"add"}`,
	))
	require.NoError(t, err)

	// invalid JSON in state
	inv = &agent.Invocation{Session: &session.Session{State: map[string][]byte{}}}
	inv.Session.State[key] = []byte("not-json")
	ctx = agent.NewInvocationContext(context.Background(), inv)
	_, err = sd.Call(ctx, []byte(
		`{"skill":"`+demoSkill+`","docs":["`+extraDoc+`"],`+
			`"mode":"add"}`,
	))
	require.NoError(t, err)

	// star means include-all already selected; non-clear should early-return
	inv = &agent.Invocation{Session: &session.Session{State: map[string][]byte{}}}
	inv.Session.State[key] = []byte("*")
	ctx = agent.NewInvocationContext(context.Background(), inv)
	out, err = sd.Call(ctx, []byte(
		`{"skill":"`+demoSkill+`","docs":["`+extraDoc+`"],`+
			`"mode":"add"}`,
	))
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	m = map[string]any{}
	require.NoError(t, json.Unmarshal(b, &m))
	require.True(t, m["include_all_docs"].(bool))

	// but clear should not be blocked by star
	out, err = sd.Call(ctx, []byte(
		`{"skill":"`+demoSkill+`","mode":"clear"}`,
	))
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	m = map[string]any{}
	require.NoError(t, json.Unmarshal(b, &m))
	require.Equal(t, "clear", m["mode"])
}

func TestSelectDocsTool_StateDeltaEdges(t *testing.T) {
	sd := NewSelectDocsTool(nil)

	// invalid JSON -> nil
	delta := sd.StateDelta(nil, []byte("{"))
	require.Nil(t, delta)

	// empty skill -> nil
	delta = sd.StateDelta(nil, []byte(`{"skill":""}`))
	require.Nil(t, delta)

	// selected is null and include_all_docs false -> []
	// We craft the JSON directly to hit this branch.
	key := skill.StateKeyDocsPrefix + demoSkill
	delta = sd.StateDelta(nil, []byte(
		`{"skill":"`+demoSkill+`","selected_docs":null,`+
			`"include_all_docs":false}`,
	))
	require.NotNil(t, delta)
	require.Equal(t, "[]", string(delta[key]))

	// include_all_docs true -> "*"
	delta = sd.StateDelta(nil, []byte(
		`{"skill":"`+demoSkill+`","include_all_docs":true}`,
	))
	require.Equal(t, []byte("*"), delta[key])
}

func TestSelectDocsTool_AddWithIncludeAll(t *testing.T) {
	root := createDocsTestSkill(t, demoSkill)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	sd := NewSelectDocsTool(repo)

	out, err := sd.Call(context.Background(), []byte(
		`{"skill":"`+demoSkill+`","docs":["`+usageDoc+`"],`+
			`"mode":"add","include_all_docs":true}`,
	))
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	// When include_all_docs is true, selected should be omitted (nil).
	_, ok := m["selected_docs"]
	require.False(t, ok)
}
