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

func TestNewToolRecordHandlesPlainArgumentsAndResultFallbacks(t *testing.T) {
	plainArgumentsTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:        "call-plain",
		Name:      "echo",
		Arguments: []byte("plain args"),
		Result:    map[string]any{"ok": true},
	})
	require.Equal(t, "plain args", plainArgumentsTool.Arguments)
	require.Equal(t, map[string]any{"ok": true}, plainArgumentsTool.Result)
	emptyResultTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:   "call-empty",
		Name: "empty",
	})
	require.Equal(t, map[string]any{}, emptyResultTool.Arguments)
	require.Nil(t, emptyResultTool.Result)
	unmarshalable := make(chan int)
	unmarshalableTool := tracecapture.NewToolRecord(tracecapture.ToolRecordInput{
		ID:     "call-channel",
		Name:   "bad",
		Result: unmarshalable,
	})
	result, ok := unmarshalableTool.Result.(chan int)
	require.True(t, ok)
	require.Equal(t, unmarshalable, result)
}

func TestLoadedSkillFromToolRecordHandlesMissingAndTypedArguments(t *testing.T) {
	_, ok := tracecapture.LoadedSkillFromToolRecord(atrace.Tool{
		Name:      "skill_load",
		Arguments: map[string]any{"other": "research"},
	})
	require.False(t, ok)
	skill, ok := tracecapture.LoadedSkillFromToolRecord(atrace.Tool{
		Name:      "skill_load",
		Arguments: map[string]string{"skill": "research"},
	})
	require.True(t, ok)
	require.Equal(t, atrace.Skill{Name: "research"}, skill)
	_, ok = tracecapture.LoadedSkillFromToolRecord(atrace.Tool{
		Name:      "skill_load",
		Arguments: "plain args",
	})
	require.False(t, ok)
}
