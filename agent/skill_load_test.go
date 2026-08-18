//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestWithSkillLoadsCopiesInputAndCloneDoesNotInherit(t *testing.T) {
	docs := []string{"guide.md"}
	loads := []skill.LoadRequest{{
		Name: "review",
		Docs: docs,
	}}
	var opts RunOptions
	WithSkillLoads(loads...)(&opts)

	docs[0] = "changed.md"
	loads[0].Name = "changed"
	require.Equal(t, "review", opts.SkillLoads[0].Name)
	require.Equal(t, []string{"guide.md"}, opts.SkillLoads[0].Docs)

	inv := NewInvocation(WithInvocationRunOptions(opts))
	child := inv.Clone()
	require.Empty(t, child.RunOptions.SkillLoads)
	require.Equal(t, "review", inv.RunOptions.SkillLoads[0].Name)
}

func TestWithSkillLoadsEmptyIsNoOp(t *testing.T) {
	existing := []skill.LoadRequest{{Name: "review"}}
	opts := RunOptions{SkillLoads: existing}

	WithSkillLoads()(&opts)

	require.Equal(t, existing, opts.SkillLoads)
}

func TestWithSkillLoadsAppends(t *testing.T) {
	var opts RunOptions

	WithSkillLoads(skill.LoadRequest{Name: "review"})(&opts)
	WithSkillLoads(skill.LoadRequest{Name: "security"})(&opts)

	require.Equal(t, []skill.LoadRequest{
		{Name: "review"},
		{Name: "security"},
	}, opts.SkillLoads)
}
