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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type mockRepo struct{ ok map[string]bool }

func (m *mockRepo) Summaries() []skill.Summary { return nil }
func (m *mockRepo) Get(name string) (*skill.Skill, error) {
	if m.ok[name] {
		return &skill.Skill{Summary: skill.Summary{Name: name}}, nil
	}
	return nil, errors.New("not found")
}
func (m *mockRepo) Path(name string) (string, error) { return "", nil }

func TestLoadTool_Call_ValidatesAndDelta(t *testing.T) {
	repo := &mockRepo{ok: map[string]bool{"calc": true}}
	lt := NewLoadTool(repo)

	// include_all_docs path
	args := loadInput{Skill: "calc", IncludeAllDocs: true}
	b, _ := json.Marshal(args)
	res, err := lt.Call(context.Background(), b)
	require.NoError(t, err)
	require.Equal(t, "loaded: calc", res)

	delta := lt.StateDelta(b, nil)
	require.Equal(t, []byte("1"),
		delta[skill.StateKeyLoadedPrefix+"calc"])
	require.Equal(t, []byte("*"),
		delta[skill.StateKeyDocsPrefix+"calc"])

	// docs array path
	args = loadInput{Skill: "calc", Docs: []string{"A.md"}}
	b, _ = json.Marshal(args)
	delta = lt.StateDelta(b, nil)
	require.NotNil(t, delta[skill.StateKeyDocsPrefix+"calc"])

	// only loaded, no docs selection
	args = loadInput{Skill: "calc"}
	b, _ = json.Marshal(args)
	delta = lt.StateDelta(b, nil)
	require.Equal(t, []byte("1"),
		delta[skill.StateKeyLoadedPrefix+"calc"])
	_, ok := delta[skill.StateKeyDocsPrefix+"calc"]
	require.False(t, ok)
}

func TestLoadTool_Call_Errors(t *testing.T) {
	lt := NewLoadTool(&mockRepo{ok: map[string]bool{}})

	// missing skill
	_, err := lt.Call(context.Background(), []byte(`{"skill":""}`))
	require.Error(t, err)

	// unknown skill
	_, err = lt.Call(context.Background(), []byte(`{"skill":"x"}`))
	require.Error(t, err)
}

func TestLoadTool_Declaration(t *testing.T) {
	lt := NewLoadTool(nil)
	d := lt.Declaration()
	require.Equal(t, "skill_load", d.Name)
	require.NotNil(t, d.InputSchema)
	require.NotNil(t, d.OutputSchema)
}

func TestLoadTool_StateDelta_InvalidArgs(t *testing.T) {
	lt := NewLoadTool(nil)
	// invalid json should return nil delta
	delta := lt.StateDelta([]byte("{"), nil)
	require.Nil(t, delta)
}

func TestLoadTool_Call_NoRepoSkipsValidation(t *testing.T) {
	lt := NewLoadTool(nil)
	// unknown skill is accepted when repo is nil
	out, err := lt.Call(context.Background(), []byte(
		`{"skill":"x"}`,
	))
	require.NoError(t, err)
	require.Equal(t, "loaded: x", out)
}
