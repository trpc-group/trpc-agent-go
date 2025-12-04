//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildBlockSpec_PythonAndBash(t *testing.T) {
	f, m, c, a, err := BuildBlockSpec(
		1, CodeBlock{Language: "python"},
	)
	require.NoError(t, err)
	require.Equal(t, "code_1.py", f)
	require.Equal(t, uint32(DefaultScriptFileMode), m)
	require.Equal(t, "python3", c)
	require.Nil(t, a)

	f2, m2, c2, a2, err2 := BuildBlockSpec(
		2, CodeBlock{Language: "bash"},
	)
	require.NoError(t, err2)
	require.Equal(t, "code_2.sh", f2)
	require.Equal(t, uint32(DefaultExecFileMode), m2)
	require.Equal(t, "bash", c2)
	require.Nil(t, a2)
}

func TestBuildBlockSpec_Unsupported(t *testing.T) {
	_, _, _, _, err := BuildBlockSpec(
		0, CodeBlock{Language: "Go"},
	)
	require.Error(t, err)
}

func TestNormalizeGlobs_EnvPrefixes(t *testing.T) {
	out := NormalizeGlobs([]string{
		"$OUTPUT_DIR/a.txt",
		"${WORK_DIR}/x/**",
		"$SKILLS_DIR/tool",
		"$WORKSPACE_DIR/out/b.txt",
	})
	require.Equal(t, []string{
		"out/a.txt",
		"work/x/**",
		"skills/tool",
		"out/b.txt",
	}, out)
}

func TestNormalizeGlobs_EmptyAndUnknown(t *testing.T) {
	out := NormalizeGlobs([]string{
		"",
		"  ",
		"out/c.txt",
		"$UNKNOWN_DIR/d.txt",
	})
	require.Equal(t, []string{
		"out/c.txt",
		"$UNKNOWN_DIR/d.txt",
	}, out)
}
