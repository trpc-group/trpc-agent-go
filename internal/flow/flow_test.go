//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestContinueOnToolErrorEnabled(t *testing.T) {
	assert.True(t, ContinueOnToolErrorEnabled(nil, true))
	assert.False(t, ContinueOnToolErrorEnabled(nil, false))

	inv := &agent.Invocation{}
	assert.True(t, ContinueOnToolErrorEnabled(inv, true))
	assert.False(t, ContinueOnToolErrorEnabled(inv, false))

	v := true
	inv.RunOptions.ContinueOnToolError = &v
	assert.True(t, ContinueOnToolErrorEnabled(inv, false))

	v = false
	inv.RunOptions.ContinueOnToolError = &v
	assert.False(t, ContinueOnToolErrorEnabled(inv, true))
}
