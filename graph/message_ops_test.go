//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestAppendMessages(t *testing.T) {
	base := []model.Message{model.NewUserMessage("a")}
	op := AppendMessages{Items: []model.Message{model.NewAssistantMessage("b")}}
	out := op.Apply(base)
	require.Len(t, out, 2)
	require.Equal(t, model.RoleUser, out[0].Role)
	require.Equal(t, model.RoleAssistant, out[1].Role)
}

func TestReplaceLastUser(t *testing.T) {
	messages := []model.Message{
		model.NewUserMessage("u1"),
		model.NewAssistantMessage("a1"),
		model.NewUserMessage("u2"),
	}
	out := (ReplaceLastUser{Content: "u2-new"}).Apply(messages)
	require.Len(t, out, 3)
	require.Equal(t, model.RoleUser, out[2].Role)
	require.Equal(t, "u2-new", out[2].Content)
}

func TestReplaceLastUserNoUserAppends(t *testing.T) {
	messages := []model.Message{model.NewAssistantMessage("a1")}
	out := (ReplaceLastUser{Content: "u-new"}).Apply(messages)
	require.Len(t, out, 2)
	require.Equal(t, model.RoleUser, out[1].Role)
	require.Equal(t, "u-new", out[1].Content)
}

func TestRemoveAllMessages(t *testing.T) {
	base := []model.Message{model.NewUserMessage("x")}
	out := (RemoveAllMessages{}).Apply(base)
	require.Nil(t, out)
}
