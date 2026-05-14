//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestWithSwarmHandoffInputBuilder_ConfiguresBuilder(t *testing.T) {
	inputBuilder := func(context.Context, SwarmHandoffInputArgs) (model.Message, error) {
		return model.NewUserMessage("input"), nil
	}
	opts := defaultOptions("team")
	WithSwarmHandoffInputBuilder(inputBuilder)(&opts)
	require.NotNil(t, opts.swarmHandoffInput)
	msg, err := opts.swarmHandoffInput(context.Background(), SwarmHandoffInputArgs{})
	require.NoError(t, err)
	require.Equal(t, "input", msg.Content)
}
