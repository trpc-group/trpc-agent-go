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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

func TestCriterionNewDefaults(t *testing.T) {
	c := New()
	assert.NotNil(t, c.ToolTrajectory)
}

func TestCriterionWithToolTrajectory(t *testing.T) {
	custom := tooltrajectory.New()
	c := New(WithToolTrajectory(custom))
	assert.Equal(t, custom, c.ToolTrajectory)
}
