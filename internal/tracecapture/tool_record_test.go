//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracecapture_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
)

func TestNewToolRecordParsesArgumentsResultAndLoadedSkill(t *testing.T) {
	recordedTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:            "call-1",
		Name:          "lookup",
		Arguments:     []byte(`{"query":"docs"}`),
		ResultContent: []byte(`{"ok":true}`),
	})
	require.Equal(t, atrace.Tool{
		ID:        "call-1",
		Name:      "lookup",
		Arguments: map[string]any{"query": "docs"},
		Result:    map[string]any{"ok": true},
	}, recordedTool)
	_, ok := tracecapture.LoadedSkillFromToolRecord(recordedTool)
	require.False(t, ok)
	plainTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:            "call-2",
		Name:          "echo",
		ResultContent: []byte("plain text"),
	})
	require.Equal(t, map[string]any{}, plainTool.Arguments)
	require.Equal(t, "plain text", plainTool.Result)
	errorTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:            "call-3",
		Name:          "lookup",
		Arguments:     []byte(`{"query":"docs"}`),
		ResultContent: []byte(`{"ignored":true}`),
		Error:         "tool failed",
	})
	require.Equal(t, "tool failed", errorTool.Error)
	require.Nil(t, errorTool.Result)
	skillTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:            "call-4",
		Name:          "skill_load",
		Arguments:     []byte(`{"skill":"research"}`),
		ResultContent: []byte(`{"loaded":true}`),
	})
	skill, ok := tracecapture.LoadedSkillFromToolRecord(skillTool)
	require.True(t, ok)
	require.Equal(t, atrace.Skill{Name: "research"}, skill)
}
