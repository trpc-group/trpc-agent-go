//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/internal/testutil"
)

func TestWithChannelBufferSize(t *testing.T) {
	testutil.TestWithChannelBufferSizeHelper(
		t,
		func(size int) interface{} {
			return WithChannelBufferSize(size)
		},
		func(opt interface{}) int {
			options := &Options{}
			opt.(Option)(options)
			return options.ChannelBufferSize
		},
		agent.DefaultChannelBufferSize,
	)
}
