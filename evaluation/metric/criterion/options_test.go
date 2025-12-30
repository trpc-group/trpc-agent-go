//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package criterion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.NotNil(t, opts.toolTrajectory)
}

func TestWithToolTrajectory(t *testing.T) {
	custom := tooltrajectory.New(tooltrajectory.WithOrderSensitive(true))
	opts := newOptions(WithToolTrajectory(custom))
	assert.Equal(t, custom, opts.toolTrajectory)
}

func TestWithLlmJudge(t *testing.T) {
	llmJudge := llm.New("p", "m")
	opts := newOptions(WithLLMJudge(llmJudge))
	assert.Equal(t, llmJudge, opts.llmJudge)
}
